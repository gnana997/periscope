package identity

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

// saInformerResyncPeriod is the SharedInformer resync interval. A
// long value (30m) is intentional: resyncs replay every cached SA as
// a Modified event, which would force the manager into a refresh
// storm on a busy cluster. The real freshness floor is the 5-min
// index TTL; the informer is only here for live IRSA-annotation
// edits between TTL ticks.
const saInformerResyncPeriod = 30 * time.Minute

// podSAIndexName is the indexer name used to bucket Pods by
// (namespace, serviceAccountName). PodsForSA reads via ByIndex so
// per-SA lookups are O(pods-for-this-SA), not O(all-pods).
const podSAIndexName = "saName"

// podSAIndexKey builds the indexer key. Empty saName is normalized
// to "default" to match k8s implicit-default-SA semantics on pod
// admission.
func podSAIndexKey(namespace, saName string) string {
	if saName == "" {
		saName = "default"
	}
	return namespace + "/" + saName
}

// StartSAInformer starts a long-lived ServiceAccount + Pod
// informer pair for the cluster and wires both into the manager.
// One shared informer factory hosts both — same cancel, same
// resync cadence — so identity surfaces (#178) and the AWS Access
// surface (#188) share one lifecycle.
//
// The SA informer:
//
//   - Provides the manager's IRSALister (cache walk extracting
//     eks.amazonaws.com/role-arn annotations).
//   - Invalidates the manager's store when an SA's IRSA annotation
//     is added, removed, or changed — lazy refresh on next Ensure.
//
// The Pod informer:
//
//   - Provides the manager's PodLister via a (namespace, saName)
//     indexer so per-SA lookups are O(pods-for-this-SA).
//   - Does NOT invalidate the SA→Role index on pod churn — pod
//     adds/removes don't change which IAM role a SA is bound to.
//     Reverse-lookup result staleness is bounded by the informer
//     resync period (30m) plus event latency (sub-second).
//
// The returned cancel function stops both informers (and is also
// triggered by ctx cancellation). Callers in cmd/periscope wire
// cancel into the shutdown sequence so the informers stop before
// the server exits.
//
// Cache sync completes asynchronously. Until SA cache is synced,
// Manager.Ensure returns ErrIRSAListerNotReady; until Pod cache is
// synced, Manager.PodsForSA returns ErrPodListerNotReady. Both map
// to 503 with Retry-After at the handler layer.
func StartSAInformer(ctx context.Context, cs kubernetes.Interface, m *Manager) (cancel context.CancelFunc, err error) {
	ctx, cancel = context.WithCancel(ctx)

	factory := informers.NewSharedInformerFactory(cs, saInformerResyncPeriod)

	saIface := factory.Core().V1().ServiceAccounts()
	saInformer := saIface.Informer()
	saLister := saIface.Lister()

	hook := &saHook{m: m}
	if _, err := saInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    hook.onAdd,
		UpdateFunc: hook.onUpdate,
		DeleteFunc: hook.onDelete,
	}); err != nil {
		cancel()
		return nil, fmt.Errorf("attach SA event handler: %w", err)
	}

	podIface := factory.Core().V1().Pods()
	podInformer := podIface.Informer()
	podIndexer := podInformer.GetIndexer()
	if err := podIndexer.AddIndexers(cache.Indexers{
		podSAIndexName: func(obj any) ([]string, error) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return nil, nil
			}
			return []string{podSAIndexKey(pod.Namespace, pod.Spec.ServiceAccountName)}, nil
		},
	}); err != nil {
		cancel()
		return nil, fmt.Errorf("attach pod SA indexer: %w", err)
	}

	factory.Start(ctx.Done())

	// Wire the IRSA lister synchronously. Lister gates on syncDone;
	// before sync, returns ErrIRSAListerNotReady so the handler can
	// serve a Retry-After.
	m.SetIRSALister(func() (map[SAKey]string, error) {
		if !hook.syncDone.Load() {
			return nil, ErrIRSAListerNotReady
		}
		return listerIRSASnapshot(saLister)
	})

	// Wire the Pod lister synchronously with its own sync gate.
	// Pod sync is independent of SA sync — either may complete
	// first; surfaces that don't need pods (sa-roles index) won't
	// block on pod sync.
	m.SetPodLister(func(namespace, saName string) ([]PodRef, int, error) {
		if !hook.podSyncDone.Load() {
			return nil, 0, ErrPodListerNotReady
		}
		objs, err := podIndexer.ByIndex(podSAIndexName, podSAIndexKey(namespace, saName))
		if err != nil {
			return nil, 0, fmt.Errorf("pod indexer ByIndex: %w", err)
		}
		refs := make([]PodRef, 0, len(objs))
		for _, obj := range objs {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				continue
			}
			refs = append(refs, PodRef{
				Namespace: pod.Namespace,
				Name:      pod.Name,
				NodeName:  pod.Spec.NodeName,
			})
		}
		return refs, len(refs), nil
	})

	go func() {
		if cache.WaitForCacheSync(ctx.Done(), saInformer.HasSynced) {
			hook.syncDone.Store(true)
		}
	}()
	go func() {
		if cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced) {
			hook.podSyncDone.Store(true)
		}
	}()

	return cancel, nil
}

// listerIRSASnapshot walks the informer's cache and returns the
// (namespace, saName) → IRSA annotation map. SAs without the
// annotation are present with an empty-string value so dual-source
// detection in UnifySARoles works regardless of map presence.
func listerIRSASnapshot(lister corelisters.ServiceAccountLister) (map[SAKey]string, error) {
	sas, err := lister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("lister.List: %w", err)
	}
	out := map[SAKey]string{}
	for _, sa := range sas {
		k := SAKey{Namespace: sa.Namespace, Name: sa.Name}
		out[k] = sa.Annotations[IrsaAnnotation]
	}
	return out, nil
}

// saHook bridges informer events to the manager's invalidation API.
// syncDone reports whether the initial SA cache sync has completed;
// podSyncDone tracks the Pod informer's separate sync state. Both
// gate their respective listers; before sync, listers return their
// not-ready sentinel and the handler serves a Retry-After.
type saHook struct {
	m           *Manager
	syncDone    atomic.Bool
	podSyncDone atomic.Bool
}

func (h *saHook) onAdd(obj any) {
	sa, ok := obj.(*corev1.ServiceAccount)
	if !ok || !h.syncDone.Load() {
		// Suppress invalidations during the initial sync replay —
		// the cold-path Ensure will rebuild from the now-synced cache.
		return
	}
	if sa.Annotations[IrsaAnnotation] != "" {
		h.m.Store().Invalidate(sa.Namespace, sa.Name)
	}
}

func (h *saHook) onUpdate(oldObj, newObj any) {
	oldSA, _ := oldObj.(*corev1.ServiceAccount)
	newSA, _ := newObj.(*corev1.ServiceAccount)
	if newSA == nil {
		return
	}
	oldArn := ""
	if oldSA != nil {
		oldArn = oldSA.Annotations[IrsaAnnotation]
	}
	newArn := newSA.Annotations[IrsaAnnotation]
	if oldArn == newArn {
		// Pure status / non-IRSA update — index doesn't change.
		return
	}
	h.m.Store().Invalidate(newSA.Namespace, newSA.Name)
}

func (h *saHook) onDelete(obj any) {
	// Cache.DeletedFinalStateUnknown wraps the SA when the informer
	// missed the delete event; unwrap before reading.
	if tomb, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tomb.Obj
	}
	sa, ok := obj.(*corev1.ServiceAccount)
	if !ok {
		return
	}
	h.m.Store().Invalidate(sa.Namespace, sa.Name)
}
