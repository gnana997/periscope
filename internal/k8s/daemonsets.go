package k8s

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListDaemonSetsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListDaemonSets(ctx context.Context, p credentials.Provider, args ListDaemonSetsArgs) (DaemonSetList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return DaemonSetList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().DaemonSets(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return DaemonSetList{}, fmt.Errorf("list daemonsets: %w", err)
	}

	out := DaemonSetList{DaemonSets: make([]DaemonSet, 0, len(raw.Items))}
	for _, d := range raw.Items {
		out.DaemonSets = append(out.DaemonSets, daemonSetSummary(&d))
	}
	return out, nil
}

type GetDaemonSetArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetDaemonSet(ctx context.Context, p credentials.Provider, args GetDaemonSetArgs) (DaemonSetDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return DaemonSetDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().DaemonSets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return DaemonSetDetail{}, fmt.Errorf("get daemonset %s/%s: %w", args.Namespace, args.Name, err)
	}

	containers := make([]ContainerSpec, 0, len(raw.Spec.Template.Spec.Containers))
	for _, c := range raw.Spec.Template.Spec.Containers {
		containers = append(containers, ContainerSpec{Name: c.Name, Image: c.Image})
	}

	conds := make([]DeploymentCondition, 0, len(raw.Status.Conditions))
	for _, c := range raw.Status.Conditions {
		conds = append(conds, DeploymentCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	var selector map[string]string
	if raw.Spec.Selector != nil {
		selector = raw.Spec.Selector.MatchLabels
	}

	return DaemonSetDetail{
		DaemonSet:      daemonSetSummary(raw),
		UpdateStrategy: string(raw.Spec.UpdateStrategy.Type),
		Selector:       selector,
		NodeSelector:   raw.Spec.Template.Spec.NodeSelector,
		Containers:     containers,
		Conditions:     conds,
		Labels:         raw.Labels,
		Annotations:    raw.Annotations,
	}, nil
}

func GetDaemonSetYAML(ctx context.Context, p credentials.Provider, args GetDaemonSetArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().DaemonSets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get daemonset %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "apps/v1"
	raw.Kind = "DaemonSet"
	return formatYAML(raw)
}

func daemonSetSummary(d *appsv1.DaemonSet) DaemonSet {
	return DaemonSet{
		Name:                   d.Name,
		Namespace:              d.Namespace,
		DesiredNumberScheduled: d.Status.DesiredNumberScheduled,
		NumberReady:            d.Status.NumberReady,
		UpdatedNumberScheduled: d.Status.UpdatedNumberScheduled,
		NumberAvailable:        d.Status.NumberAvailable,
		NumberMisscheduled:     d.Status.NumberMisscheduled,
		CreatedAt:              d.CreationTimestamp.Time,
	}
}
