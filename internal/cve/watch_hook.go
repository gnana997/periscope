package cve

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/gnana997/periscope/internal/clusters"
)

// podInformerResyncPeriod is the SharedInformer resync interval. We
// intentionally pick a long value (30m) — Informers' own resyncs
// re-deliver every cached pod as a Modified event, which would
// hammer Inspector with redundant Refreshes if set too short. The
// real freshness floor is the 6h TTL loop; the informer is only
// here for live deltas between TTL ticks.
const podInformerResyncPeriod = 30 * time.Minute

// startPodInformer spins up a long-lived cluster-scoped Pod informer
// for delta refresh. Each Add/Update/Delete event is funneled into
// the CVE hook which diffs imageIDs and triggers async refreshes.
//
// The informer runs until the per-cluster context is cancelled
// (Manager.Stop) — survives independent of the per-HTTP SSE pod
// watches in internal/k8s/watch.go, which are scoped to a single
// request.
func (m *Manager) startPodInformer(parent context.Context, cluster clusters.Cluster, cs kubernetes.Interface, st *clusterState) error {
	ctx, cancel := context.WithCancel(parent)
	st.podInformerCancel = cancel

	factory := informers.NewSharedInformerFactory(cs, podInformerResyncPeriod)
	podInformer := factory.Core().V1().Pods().Informer()

	hook := newPodHook(ctx, m, cluster, st)
	_, err := podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    hook.onAdd,
		UpdateFunc: hook.onUpdate,
		DeleteFunc: hook.onDelete,
	})
	if err != nil {
		cancel()
		st.podInformerCancel = nil
		return err
	}

	factory.Start(ctx.Done())
	// Don't block hydrate on the initial sync — the cold path has
	// already pre-populated the digest set + ref counts. The
	// informer's purpose from here on is deltas; missing the first
	// sync is harmless because hydratePods already did one
	// equivalent.
	go factory.WaitForCacheSync(ctx.Done())

	m.log.Info("cve pod informer started", "cluster", cluster.Name)
	return nil
}

// podHook is the bridge between informer events and the manager's
// fetch path. Carries the cluster + store so the event handlers can
// fire async refresh without router-style plumbing.
type podHook struct {
	ctx     context.Context
	mgr     *Manager
	cluster clusters.Cluster
	store   *Store
}

func newPodHook(ctx context.Context, mgr *Manager, cluster clusters.Cluster, st *clusterState) *podHook {
	return &podHook{ctx: ctx, mgr: mgr, cluster: cluster, store: st.store}
}

// onAdd: every container's imageID gets a Ref bump. If we already
// hold findings for that digest, the existing entry is reused; if
// not, we kick off an async refresh so the next read sees data.
func (h *podHook) onAdd(obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	for _, d := range PodImageDigests(pod) {
		h.store.IncDigestRef(d)
		if h.store.GetDigest(d) == nil || len(h.store.GetDigest(d).Findings) == 0 {
			h.enqueueDigest(d)
		}
	}
}

// onUpdate: diff old vs new imageIDs. New digests get ref-incremented
// and async-fetched; departed digests get decremented. CRITICALLY,
// we compare imageIDs (resolved digest) not images (operator-written
// tag) — a rolling :latest tag whose digest churns is the exact
// case where the watch hook must fire even though spec.image is
// unchanged. See epic #163 "why imageID, not image".
func (h *podHook) onUpdate(oldObj, newObj any) {
	oldPod, _ := oldObj.(*corev1.Pod)
	newPod, _ := newObj.(*corev1.Pod)
	old := digestSet(PodImageDigests(oldPod))
	new := digestSet(PodImageDigests(newPod))
	for d := range new {
		if _, kept := old[d]; kept {
			continue
		}
		h.store.IncDigestRef(d)
		h.enqueueDigest(d)
	}
	for d := range old {
		if _, kept := new[d]; kept {
			continue
		}
		h.store.DecDigestRef(d)
	}
}

// onDelete: every digest the pod was using loses one ref. Cache
// entries with zero refs get cleaned up by the eviction sweeper after
// EvictAfter; we don't drop them eagerly because a re-create is
// common (rolling deploy ends with old pod deletion + new pod add
// of the same digest).
func (h *podHook) onDelete(obj any) {
	// Cache.DeletedFinalStateUnknown can wrap the pod when the
	// informer missed the delete event itself; unwrap it.
	if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tomb.Obj
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	for _, d := range PodImageDigests(pod) {
		h.store.DecDigestRef(d)
	}
}

// enqueueDigest fires an async Refresh for a single digest. Errors
// are logged and swallowed — the TTL loop will retry eventually.
func (h *podHook) enqueueDigest(d string) {
	go func() {
		if err := h.mgr.fetchDigest(h.ctx, h.cluster, h.store, d); err != nil {
			h.mgr.log.Debug("cve delta refresh", "cluster", h.cluster.Name, "digest", d, "err", err)
		}
	}()
}

func digestSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		out[s] = struct{}{}
	}
	return out
}
