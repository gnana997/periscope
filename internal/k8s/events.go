package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// ListObjectEventsArgs identifies a single Kubernetes object whose events
// to list. Per GROUND_RULES decision: strict per-object (no walking owner
// references). For Deployment events, the user clicks through to the
// owning Pod / ReplicaSet's events explicitly.
type ListObjectEventsArgs struct {
	Cluster clusters.Cluster
	// Kind is the Kubernetes Kind of the involved object (Pod, Deployment,
	// Service, ConfigMap, Namespace). Used for the field selector.
	Kind string
	// Namespace is empty for cluster-scoped resources (Namespace itself).
	// Events are namespaced and live in the same namespace as the
	// involvedObject for namespaced resources.
	Namespace string
	Name      string
}

// ListObjectEvents returns events scoped to a single object, sorted by
// most-recent first.
func ListObjectEvents(ctx context.Context, p credentials.Provider, args ListObjectEventsArgs) (EventList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return EventList{}, fmt.Errorf("build clientset: %w", err)
	}

	selectors := []string{
		"involvedObject.kind=" + args.Kind,
		"involvedObject.name=" + args.Name,
	}
	if args.Namespace != "" {
		selectors = append(selectors, "involvedObject.namespace="+args.Namespace)
	}

	raw, err := cs.CoreV1().Events(args.Namespace).List(ctx, metav1.ListOptions{
		FieldSelector: strings.Join(selectors, ","),
	})
	if err != nil {
		return EventList{}, fmt.Errorf("list events: %w", err)
	}

	out := EventList{Events: make([]Event, 0, len(raw.Items))}
	for _, e := range raw.Items {
		last := e.LastTimestamp.Time
		if last.IsZero() {
			last = e.EventTime.Time
		}
		first := e.FirstTimestamp.Time
		if first.IsZero() {
			first = last
		}
		out.Events = append(out.Events, Event{
			Type:    e.Type,
			Reason:  e.Reason,
			Message: e.Message,
			Count:   e.Count,
			First:   first,
			Last:    last,
			Source:  e.Source.Component,
		})
	}

	sort.Slice(out.Events, func(i, j int) bool {
		return out.Events[i].Last.After(out.Events[j].Last)
	})
	return out, nil
}
