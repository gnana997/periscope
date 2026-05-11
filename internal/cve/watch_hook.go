package cve

import (
	"context"
	"sync/atomic"
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
	podIface := factory.Core().V1().Pods()
	rsIface := factory.Apps().V1().ReplicaSets()
	rsLister := rsIface.Lister()
	rsInformer := rsIface.Informer()
	lister := podIface.Lister()
	podInformer := podIface.Informer()

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

	m.mu.Lock()
	st.podLister = lister
	st.rsLister = rsLister
	m.mu.Unlock()
	factory.Start(ctx.Done())
	// Cold-path hydrate already populated digest entries + ref
	// counts from a direct pod list. The informer's initial sync
	// will then replay an Add for every existing pod; we set
	// hook.syncDone only AFTER that replay completes so the Add
	// handler can suppress its double-count of refs during replay.
	// Once syncDone flips true, post-startup Adds (new pods) bump
	// refs normally.
	go func() {
		if cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced, rsInformer.HasSynced) {
			hook.syncDone.Store(true)
		}
	}()

	m.log.Info("cve pod informer started", "cluster", cluster.Name)
	return nil
}

// podHook is the bridge between informer events and the manager's
// fetch path. Carries the cluster + store so the event handlers can
// fire async refresh without router-style plumbing.
//
// syncDone flips to true once the SharedInformer's initial cache
// sync finishes (see startPodInformer). Until then, Add events are
// replay of pods the cold-path hydrate already counted, so the Add
// handler must NOT bump refs again.
type podHook struct {
	state    *clusterState
	ctx      context.Context
	mgr      *Manager
	cluster  clusters.Cluster
	store    *Store
	syncDone atomic.Bool
}

func newPodHook(ctx context.Context, mgr *Manager, cluster clusters.Cluster, st *clusterState) *podHook {
	return &podHook{ctx: ctx, mgr: mgr, cluster: cluster, store: st.store, state: st}
}

// onAdd: every container's imageID gets a Ref bump and an async
// refresh kicked off if findings are missing. During the initial
// informer sync (syncDone == false), the ref bump is suppressed —
// hydratePods already counted these pods, and double-counting would
// off-by-one every digest until the next pod delete.
func (h *podHook) onAdd(obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	skipInc := !h.syncDone.Load()
	for _, d := range PodImageDigests(pod) {
		if !skipInc {
			h.store.IncDigestRef(d)
		}
		if e := h.store.GetDigest(d); e == nil || len(e.Findings) == 0 {
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

// enqueueDigest fires an async refresh for a single digest. Runs
// under the manager-wide delta semaphore so a rolling deploy of
// many pods does not spawn unbounded goroutines, each with an
// Inspector socket. Errors are logged and swallowed — the TTL
// loop will retry eventually.
func (h *podHook) enqueueDigest(d string) {
	go h.mgr.runDelta(h.ctx, func() {
		if err := h.mgr.fetchDigests(h.ctx, h.state, []string{d}); err != nil {
			h.mgr.log.Debug("cve delta refresh", "cluster", h.cluster.Name, "digest", d, "err", err)
		}
	})
}

func digestSet(in []string) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for _, s := range in {
		out[s] = struct{}{}
	}
	return out
}
