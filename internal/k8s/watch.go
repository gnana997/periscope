package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// WatchEventType identifies the kind of WatchEvent. The string values
// match the SSE event names emitted by the watch handlers, so consumers
// can pass them straight through to sse.Writer.Event without translation.
type WatchEventType string

const (
	// WatchSnapshot is the initial event for a stream. Items holds the
	// full list and ResourceVersion holds the list's RV.
	WatchSnapshot WatchEventType = "snapshot"

	// WatchAdded/Modified/Deleted carry a single Object (a list-view DTO,
	// e.g. *Pod) and the ResourceVersion of that event.
	WatchAdded    WatchEventType = "added"
	WatchModified WatchEventType = "modified"
	WatchDeleted  WatchEventType = "deleted"

	// WatchRelist tells the consumer to discard its cache; the next event
	// will be a fresh Snapshot. Emitted when the apiserver returns 410
	// Gone (the watcher's resourceVersion is no longer in the cache).
	WatchRelist WatchEventType = "relist"
)

// WatchEvent is one delivery to a WatchSink.
//
// Field validity by Type:
//
//	Snapshot                   → ResourceVersion + Items
//	Added / Modified / Deleted → ResourceVersion + Object
//	Relist                     → no fields
//
// Object and Items are typed any so the same shape carries Pods,
// Events, ReplicaSets, Jobs, and any future kinds. The watch handler
// owns JSON marshalling.
type WatchEvent struct {
	Type            WatchEventType
	ResourceVersion string
	Object          any
	Items           any
}

// WatchSink receives events from a watch loop. Send must be non-blocking
// or near-non-blocking — the watch loop must not be pinned by a slow
// consumer. Returning false signals the loop to abort cleanly (typically
// because backpressure detected the consumer is not keeping up).
//
// Implementations are called from the watch loop's single goroutine and
// need not be safe for concurrent Send.
type WatchSink interface {
	Send(ev WatchEvent) bool
}

// WatchArgs is the common shape for every Watch* primitive: a cluster
// reference plus a namespace (empty for cluster-scoped or all-namespace
// queries). The shape mirrors the existing List*Args structs across the
// k8s package.
//
// ResumeFrom is the optional resourceVersion to resume from when a
// client reconnects with Last-Event-ID. Empty means "fresh List+Watch
// (and emit a Snapshot)"; non-empty means "skip the List, open Watch
// directly at that RV, do not emit a Snapshot — only deltas". This
// preserves the client's React Query cache across transient blips,
// avoiding the row-flicker that a fresh snapshot causes on reconnect.
//
// If the apiserver rejects the RV with 410 Gone (cache too old),
// watchKind falls back to a fresh List+Watch one time before giving
// up, so a stale Last-Event-ID never causes a hard failure.
type WatchArgs struct {
	Cluster    clusters.Cluster
	Namespace  string
	ResumeFrom string
}

// watchSpec is the kind-specific surface that watchKind needs in order
// to run a generic list-then-watch loop. Each Watch* exported function
// is a thin wrapper that builds a watchSpec from its clientset method
// chain and the appropriate summary helper.
//
// Type parameters:
//
//	T = the K8s API type (e.g. corev1.Pod, corev1.Event)
//	S = the list-view DTO emitted to the sink (e.g. Pod, ClusterEvent)
type watchSpec[T any, S any] struct {
	// Kind labels the resource for error messages and observability.
	Kind string

	// List runs an apiserver List against the typed client and returns
	// the items + the list's resourceVersion. Both errors and empty
	// results are valid; the watcher decides what to do.
	List func(ctx context.Context, opts metav1.ListOptions) ([]T, string, error)

	// Watch opens an apiserver watch starting at opts.ResourceVersion
	// (set by watchKind) with allowWatchBookmarks already requested.
	Watch func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)

	// Summary projects the API type to the list-view DTO. Same function
	// is used for the initial snapshot and per-event deltas, so the
	// frontend cache patches against shape-identical objects.
	Summary func(*T) S

	// PostList is an optional transform on the snapshot's summarized
	// items before they're sent. Used by Events to sort newest-first
	// and cap; pod-shaped resources leave it nil.
	PostList func([]S) []S
}

// watchKind runs the canonical k8s list-then-watch loop using a
// kind-specific watchSpec.
//
// Lifecycle:
//
//  1. If resumeFrom is non-empty: skip the List, open Watch at that
//     RV, do NOT emit a Snapshot. If the Watch open fails with 410
//     Gone (RV expired), fall through to the fresh path — the stale
//     Last-Event-ID gracefully degrades.
//  2. Otherwise (or after a 410 fall-through): List with no RV →
//     emit Snapshot.
//  3. Watch from the latest RV with allowWatchBookmarks=true.
//  4. ADDED/MODIFIED/DELETED → emit the corresponding event.
//  5. BOOKMARK → no emit; resource version is implicitly refreshed by
//     the next list on relist.
//  6. apiserver Status with code 410 Gone → emit Relist, list again,
//     watch again.
//  7. ctx cancelled or sink.Send returned false → return nil.
//  8. Any other error → return it. The caller (SSE handler) decides
//     whether to surface it as event:error or close silently.
//
// watchKind does not retry transient network errors itself — the SSE
// transport is the right place for that, since the browser's
// EventSource will reconnect automatically.
func watchKind[T any, S any](ctx context.Context, spec watchSpec[T, S], resumeFrom string, sink WatchSink) error {
	for {
		var watchRV string

		if resumeFrom != "" {
			// Resume path: skip the List+Snapshot. Existing client cache
			// is preserved; only deltas flow on reconnect.
			watchRV = resumeFrom
			resumeFrom = "" // single-use; on relist we go through fresh path
		} else {
			items, rv, err := spec.List(ctx, metav1.ListOptions{})
			if err != nil {
				return fmt.Errorf("list %s: %w", spec.Kind, err)
			}
			summarized := make([]S, 0, len(items))
			for i := range items {
				summarized = append(summarized, spec.Summary(&items[i]))
			}
			if spec.PostList != nil {
				summarized = spec.PostList(summarized)
			}
			if !sink.Send(WatchEvent{
				Type:            WatchSnapshot,
				ResourceVersion: rv,
				Items:           summarized,
			}) {
				return nil
			}
			watchRV = rv
		}

		watcher, err := spec.Watch(ctx, metav1.ListOptions{
			ResourceVersion:     watchRV,
			AllowWatchBookmarks: true,
		})
		if err != nil {
			// Stale Last-Event-ID can fail the initial Watch open with
			// 410 Gone. Fall through to the fresh path one time —
			// resumeFrom was already cleared above, so the next loop
			// iteration runs List+Snapshot.
			if apierrors.IsGone(err) || apierrors.IsResourceExpired(err) {
				continue
			}
			return fmt.Errorf("watch %s: %w", spec.Kind, err)
		}

		relist, err := drainWatcher(ctx, watcher, spec.Summary, sink)
		watcher.Stop()
		if err != nil {
			return err
		}
		if !relist {
			return nil
		}
	}
}

// drainWatcher consumes events from a single watcher until ctx is
// cancelled, sink.Send returns false, the apiserver signals 410 Gone
// (returns relist=true), or another apiserver error fires.
//
// The summary function is identical to watchSpec.Summary; it lives as
// a parameter rather than re-passing the whole spec because that's all
// drainWatcher needs.
func drainWatcher[T any, S any](
	ctx context.Context,
	watcher watch.Interface,
	summary func(*T) S,
	sink WatchSink,
) (bool, error) {
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				// Apiserver closed the channel. Treat as a need to relist;
				// the next list call will return a fresh resourceVersion.
				return true, nil
			}

			switch event.Type {
			case watch.Bookmark:
				// Resource version is implicitly fresh after the next
				// relist; no need to track it explicitly here.
				continue
			case watch.Error:
				if status, ok := event.Object.(*metav1.Status); ok {
					if status.Reason == metav1.StatusReasonGone || status.Code == 410 {
						if !sink.Send(WatchEvent{Type: WatchRelist}) {
							return false, nil
						}
						return true, nil
					}
					return false, fmt.Errorf("watch error: %s", status.Message)
				}
				return false, errors.New("watch error: unknown status object")
			}

			// Generic type assertion: event.Object is runtime.Object,
			// and *T does not statically satisfy runtime.Object inside
			// a generic body (Go type-system limitation). Cast through
			// any and assert to *T concretely.
			obj, ok := any(event.Object).(*T)
			if !ok {
				continue
			}
			acc, err := meta.Accessor(event.Object)
			if err != nil {
				continue
			}

			var t WatchEventType
			switch event.Type {
			case watch.Added:
				t = WatchAdded
			case watch.Modified:
				t = WatchModified
			case watch.Deleted:
				t = WatchDeleted
			default:
				continue
			}

			if !sink.Send(WatchEvent{
				Type:            t,
				ResourceVersion: acc.GetResourceVersion(),
				Object:          summary(obj),
			}) {
				return false, nil
			}
		}
	}
}

// WatchPods runs a list-then-watch loop on Pods in the given namespace.
// See watchKind for lifecycle details.
func WatchPods(ctx context.Context, p credentials.Provider, args WatchArgs, sink WatchSink) error {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return fmt.Errorf("build clientset: %w", err)
	}
	return watchKind(ctx, watchSpec[corev1.Pod, Pod]{
		Kind: "pods",
		List: func(ctx context.Context, opts metav1.ListOptions) ([]corev1.Pod, string, error) {
			list, err := cs.CoreV1().Pods(args.Namespace).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return list.Items, list.ResourceVersion, nil
		},
		Watch: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			return cs.CoreV1().Pods(args.Namespace).Watch(ctx, opts)
		},
		Summary: podSummary,
	}, args.ResumeFrom, sink)
}

// WatchReplicaSets runs a list-then-watch loop on ReplicaSets in the
// given namespace. See watchKind for lifecycle details.
func WatchReplicaSets(ctx context.Context, p credentials.Provider, args WatchArgs, sink WatchSink) error {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return fmt.Errorf("build clientset: %w", err)
	}
	return watchKind(ctx, watchSpec[appsv1.ReplicaSet, ReplicaSet]{
		Kind: "replicasets",
		List: func(ctx context.Context, opts metav1.ListOptions) ([]appsv1.ReplicaSet, string, error) {
			list, err := cs.AppsV1().ReplicaSets(args.Namespace).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return list.Items, list.ResourceVersion, nil
		},
		Watch: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			return cs.AppsV1().ReplicaSets(args.Namespace).Watch(ctx, opts)
		},
		Summary: replicaSetSummary,
	}, args.ResumeFrom, sink)
}

// WatchJobs runs a list-then-watch loop on Jobs in the given namespace.
// See watchKind for lifecycle details.
func WatchJobs(ctx context.Context, p credentials.Provider, args WatchArgs, sink WatchSink) error {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return fmt.Errorf("build clientset: %w", err)
	}
	return watchKind(ctx, watchSpec[batchv1.Job, Job]{
		Kind: "jobs",
		List: func(ctx context.Context, opts metav1.ListOptions) ([]batchv1.Job, string, error) {
			list, err := cs.BatchV1().Jobs(args.Namespace).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return list.Items, list.ResourceVersion, nil
		},
		Watch: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			return cs.BatchV1().Jobs(args.Namespace).Watch(ctx, opts)
		},
		Summary: jobSummary,
	}, args.ResumeFrom, sink)
}

// WatchEvents runs a list-then-watch loop on cluster Events. The
// initial snapshot is sorted newest-Last first and capped at
// clusterEventCap to match the existing ListClusterEvents semantics —
// frontend cache patches stay shape-identical to polled list responses.
//
// Delta events emit raw eventSummary'd objects with no cap or sort —
// the frontend's cache patcher reconciles them into the capped list.
// In the rare case a MODIFIED arrives for an event that was outside
// the snapshot's top-N, the frontend treats it as ADDED (typical
// patchRowInList semantics).
func WatchEvents(ctx context.Context, p credentials.Provider, args WatchArgs, sink WatchSink) error {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return fmt.Errorf("build clientset: %w", err)
	}
	return watchKind(ctx, watchSpec[corev1.Event, ClusterEvent]{
		Kind: "events",
		List: func(ctx context.Context, opts metav1.ListOptions) ([]corev1.Event, string, error) {
			list, err := cs.CoreV1().Events(args.Namespace).List(ctx, opts)
			if err != nil {
				return nil, "", err
			}
			return list.Items, list.ResourceVersion, nil
		},
		Watch: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			return cs.CoreV1().Events(args.Namespace).Watch(ctx, opts)
		},
		Summary: eventSummary,
		PostList: func(events []ClusterEvent) []ClusterEvent {
			sort.Slice(events, func(i, j int) bool {
				return events[i].Last.After(events[j].Last)
			})
			if len(events) > clusterEventCap {
				events = events[:clusterEventCap]
			}
			return events
		},
	}, args.ResumeFrom, sink)
}
