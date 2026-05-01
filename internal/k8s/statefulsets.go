package k8s

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListStatefulSetsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListStatefulSets(ctx context.Context, p credentials.Provider, args ListStatefulSetsArgs) (StatefulSetList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return StatefulSetList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().StatefulSets(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return StatefulSetList{}, fmt.Errorf("list statefulsets: %w", err)
	}

	out := StatefulSetList{StatefulSets: make([]StatefulSet, 0, len(raw.Items))}
	for _, s := range raw.Items {
		out.StatefulSets = append(out.StatefulSets, statefulSetSummary(&s))
	}
	return out, nil
}

type GetStatefulSetArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetStatefulSet(ctx context.Context, p credentials.Provider, args GetStatefulSetArgs) (StatefulSetDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return StatefulSetDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().StatefulSets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return StatefulSetDetail{}, fmt.Errorf("get statefulset %s/%s: %w", args.Namespace, args.Name, err)
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

	pods, podErr := childPodsBySelector(ctx, cs, args.Namespace, raw.Spec.Selector)
	if podErr != nil {
		return StatefulSetDetail{}, fmt.Errorf("list statefulset pods: %w", podErr)
	}
	return StatefulSetDetail{
		StatefulSet:    statefulSetSummary(raw),
		ServiceName:    raw.Spec.ServiceName,
		UpdateStrategy: string(raw.Spec.UpdateStrategy.Type),
		Selector:       selector,
		Containers:     containers,
		Conditions:     conds,
		Pods:           pods,
		Labels:         raw.Labels,
		Annotations:    raw.Annotations,
	}, nil
}

func GetStatefulSetYAML(ctx context.Context, p credentials.Provider, args GetStatefulSetArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().StatefulSets(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get statefulset %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "apps/v1"
	raw.Kind = "StatefulSet"
	return formatYAML(raw)
}

func statefulSetSummary(s *appsv1.StatefulSet) StatefulSet {
	var replicas int32
	if s.Spec.Replicas != nil {
		replicas = *s.Spec.Replicas
	}
	return StatefulSet{
		Name:            s.Name,
		Namespace:       s.Namespace,
		Replicas:        replicas,
		ReadyReplicas:   s.Status.ReadyReplicas,
		UpdatedReplicas: s.Status.UpdatedReplicas,
		CurrentReplicas: s.Status.CurrentReplicas,
		CreatedAt:       s.CreationTimestamp.Time,
	}
}
