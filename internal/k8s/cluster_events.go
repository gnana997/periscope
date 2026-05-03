package k8s

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// clusterEventCap is the maximum number of events returned. Events are
// sorted newest-Last first and truncated here — not in the frontend.
// 500 covers typical cluster triage without overwhelming the payload.
const clusterEventCap = 500

type ListClusterEventsArgs struct {
	Cluster   clusters.Cluster
	Namespace string // empty = all namespaces
}

func ListClusterEvents(ctx context.Context, p credentials.Provider, args ListClusterEventsArgs) (ClusterEventList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ClusterEventList{}, fmt.Errorf("build clientset: %w", err)
	}

	raw, err := cs.CoreV1().Events(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterEventList{}, fmt.Errorf("list events: %w", err)
	}

	out := make([]ClusterEvent, 0, len(raw.Items))
	for i := range raw.Items {
		out = append(out, eventSummary(&raw.Items[i]))
	}

	// Newest last-occurrence first.
	sort.Slice(out, func(i, j int) bool {
		return out[i].Last.After(out[j].Last)
	})

	if len(out) > clusterEventCap {
		out = out[:clusterEventCap]
	}

	return ClusterEventList{Events: out}, nil
}

// eventSummary builds the cluster-events list-view DTO from a corev1.Event.
// Shared between ListClusterEvents (one-shot list) and WatchEvents
// (streaming snapshots + delta events).
func eventSummary(e *corev1.Event) ClusterEvent {
	source := e.Source.Component
	if e.Source.Host != "" {
		source += "/" + e.Source.Host
	}
	// ReportingController supersedes the legacy Source field when set.
	if e.ReportingController != "" {
		source = e.ReportingController
	}

	count := e.Count
	if count == 0 {
		count = 1
	}

	return ClusterEvent{
		UID:       string(e.UID),
		Namespace: e.Namespace,
		Kind:      e.InvolvedObject.Kind,
		Name:      e.InvolvedObject.Name,
		Type:      e.Type,
		Reason:    e.Reason,
		Message:   e.Message,
		Count:     count,
		First:     e.FirstTimestamp.Time,
		Last:      e.LastTimestamp.Time,
		Source:    source,
	}
}
