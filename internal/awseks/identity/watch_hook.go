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

// StartSAInformer starts a long-lived ServiceAccount informer for
// the cluster and wires it into the manager. The informer:
//
//   - Provides the manager's IRSALister (cache walk extracting
//     eks.amazonaws.com/role-arn annotations).
//   - Invalidates the manager's store when an SA's IRSA annotation
//     is added, removed, or changed — lazy refresh on next Ensure.
//
// The returned cancel function stops the informer (and is also
// triggered by ctx cancellation). Callers in cmd/periscope wire
// cancel into the shutdown sequence so the informer stops before
// the server exits.
//
// The informer runs in a goroutine. Cache sync completes
// asynchronously; until it does, Manager.Ensure returns
// ErrIRSAListerNotReady which the handler translates to 503.
func StartSAInformer(ctx context.Context, cs kubernetes.Interface, m *Manager) (cancel context.CancelFunc, err error) {
	ctx, cancel = context.WithCancel(ctx)

	factory := informers.NewSharedInformerFactory(cs, saInformerResyncPeriod)
	saIface := factory.Core().V1().ServiceAccounts()
	informer := saIface.Informer()
	lister := saIface.Lister()

	hook := &saHook{m: m}
	if _, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    hook.onAdd,
		UpdateFunc: hook.onUpdate,
		DeleteFunc: hook.onDelete,
	}); err != nil {
		cancel()
		return nil, fmt.Errorf("attach SA event handler: %w", err)
	}

	factory.Start(ctx.Done())

	// Wire the IRSA lister now (synchronously). The lister returns
	// an empty result until the cache is synced, but the manager
	// gates Ensure on Hook.syncDone before that — once syncDone
	// flips true, the lister's snapshot is authoritative.
	m.SetIRSALister(func() (map[SAKey]string, error) {
		if !hook.syncDone.Load() {
			return nil, ErrIRSAListerNotReady
		}
		return listerIRSASnapshot(lister)
	})

	go func() {
		if cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
			hook.syncDone.Store(true)
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
// syncDone reports whether the initial cache sync has completed —
// before that, the lister returns an empty list and Manager.Ensure
// soft-fails with ErrIRSAListerNotReady so the handler can serve a
// Retry-After.
type saHook struct {
	m        *Manager
	syncDone atomic.Bool
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
