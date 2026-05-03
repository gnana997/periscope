package k8s

import (
	"context"
	"errors"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
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
// Object and Items are typed any so the same shape carries Pods today
// and Events / ReplicaSets / Jobs in later phases. The watch handler
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
type WatchArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

// WatchPods runs a list-then-watch loop on Pods in the given namespace,
// translating apiserver events into WatchEvents and delivering them to
// sink.
//
// Lifecycle:
//
//  1. List with no resourceVersion → emit Snapshot.
//  2. Watch from that resourceVersion with allowWatchBookmarks=true.
//  3. ADDED/MODIFIED/DELETED → emit the corresponding event.
//  4. BOOKMARK → no emit; resource version is implicitly refreshed by
//     the next list on relist.
//  5. apiserver Status with code 410 Gone → emit Relist, list again,
//     watch again.
//  6. ctx cancelled or sink.Send returned false → return nil.
//  7. Any other error → return it. The caller (SSE handler) decides
//     whether to surface it as event:error or close silently.
//
// WatchPods does not retry transient network errors itself — the SSE
// transport is the right place for that, since the browser's
// EventSource will reconnect automatically.
func WatchPods(ctx context.Context, p credentials.Provider, args WatchArgs, sink WatchSink) error {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return fmt.Errorf("build clientset: %w", err)
	}

	for {
		list, err := cs.CoreV1().Pods(args.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list pods: %w", err)
		}
		items := make([]Pod, 0, len(list.Items))
		for i := range list.Items {
			items = append(items, podSummary(&list.Items[i]))
		}
		if !sink.Send(WatchEvent{
			Type:            WatchSnapshot,
			ResourceVersion: list.ResourceVersion,
			Items:           items,
		}) {
			return nil
		}

		watcher, err := cs.CoreV1().Pods(args.Namespace).Watch(ctx, metav1.ListOptions{
			ResourceVersion:     list.ResourceVersion,
			AllowWatchBookmarks: true,
		})
		if err != nil {
			return fmt.Errorf("watch pods: %w", err)
		}

		relist, err := drainPodWatcher(ctx, watcher, sink)
		watcher.Stop()
		if err != nil {
			return err
		}
		if !relist {
			return nil
		}
	}
}

// drainPodWatcher consumes events from a single watcher.
// Returns (relist=true) when the apiserver signals 410 Gone or closes
// the watch channel cleanly; the caller should then list again and
// reopen the watch.
// Returns (relist=false, err=nil) when ctx is cancelled or sink.Send
// returns false.
// Returns (relist=false, err=non-nil) on apiserver-side errors that
// should propagate.
func drainPodWatcher(ctx context.Context, watcher watch.Interface, sink WatchSink) (bool, error) {
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

			pod, ok := event.Object.(*corev1.Pod)
			if !ok {
				continue
			}
			summary := podSummary(pod)

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
				ResourceVersion: pod.ResourceVersion,
				Object:          summary,
			}) {
				return false, nil
			}
		}
	}
}

// WatchEvents runs a list-then-watch loop on cluster Events. The
// initial snapshot is sorted newest-Last first and capped at
// clusterEventCap to match the existing ListClusterEvents semantics —
// frontend cache patches stay shape-identical to polled list responses.
//
// Lifecycle is identical to WatchPods (see that doc for details). The
// only kind-specific differences are the clientset method chain
// (CoreV1().Events vs CoreV1().Pods) and the summary conversion
// (eventSummary vs podSummary).
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

	for {
		list, err := cs.CoreV1().Events(args.Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("list events: %w", err)
		}
		items := make([]ClusterEvent, 0, len(list.Items))
		for i := range list.Items {
			items = append(items, eventSummary(&list.Items[i]))
		}
		sort.Slice(items, func(i, j int) bool {
			return items[i].Last.After(items[j].Last)
		})
		if len(items) > clusterEventCap {
			items = items[:clusterEventCap]
		}
		if !sink.Send(WatchEvent{
			Type:            WatchSnapshot,
			ResourceVersion: list.ResourceVersion,
			Items:           items,
		}) {
			return nil
		}

		watcher, err := cs.CoreV1().Events(args.Namespace).Watch(ctx, metav1.ListOptions{
			ResourceVersion:     list.ResourceVersion,
			AllowWatchBookmarks: true,
		})
		if err != nil {
			return fmt.Errorf("watch events: %w", err)
		}

		relist, err := drainEventWatcher(ctx, watcher, sink)
		watcher.Stop()
		if err != nil {
			return err
		}
		if !relist {
			return nil
		}
	}
}

// drainEventWatcher is the Event variant of drainPodWatcher. The
// duplication is intentional and short-lived: phase 5 factors a
// generic drainWatcher[T,S] once we have three concrete drains to
// observe the right shape from.
func drainEventWatcher(ctx context.Context, watcher watch.Interface, sink WatchSink) (bool, error) {
	for {
		select {
		case <-ctx.Done():
			return false, nil
		case event, ok := <-watcher.ResultChan():
			if !ok {
				return true, nil
			}
			switch event.Type {
			case watch.Bookmark:
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

			ev, ok := event.Object.(*corev1.Event)
			if !ok {
				continue
			}
			summary := eventSummary(ev)

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
				ResourceVersion: ev.ResourceVersion,
				Object:          summary,
			}) {
				return false, nil
			}
		}
	}
}
