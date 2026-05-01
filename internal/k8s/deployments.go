package k8s

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListDeploymentsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListDeployments(ctx context.Context, p credentials.Provider, args ListDeploymentsArgs) (DeploymentList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return DeploymentList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().Deployments(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return DeploymentList{}, fmt.Errorf("list deployments: %w", err)
	}

	out := DeploymentList{Deployments: make([]Deployment, 0, len(raw.Items))}
	for _, d := range raw.Items {
		out.Deployments = append(out.Deployments, deploymentSummary(&d))
	}
	return out, nil
}

type GetDeploymentArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetDeployment(ctx context.Context, p credentials.Provider, args GetDeploymentArgs) (DeploymentDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return DeploymentDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().Deployments(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return DeploymentDetail{}, fmt.Errorf("get deployment %s/%s: %w", args.Namespace, args.Name, err)
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
		return DeploymentDetail{}, fmt.Errorf("list deployment pods: %w", podErr)
	}
	return DeploymentDetail{
		Deployment:  deploymentSummary(raw),
		Strategy:    string(raw.Spec.Strategy.Type),
		Selector:    selector,
		Containers:  containers,
		Conditions:  conds,
		Pods:        pods,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetDeploymentYAML(ctx context.Context, p credentials.Provider, args GetDeploymentArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.AppsV1().Deployments(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get deployment %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "apps/v1"
	raw.Kind = "Deployment"
	return formatYAML(raw)
}

func deploymentSummary(d *appsv1.Deployment) Deployment {
	var replicas int32
	if d.Spec.Replicas != nil {
		replicas = *d.Spec.Replicas
	}
	return Deployment{
		Name:              d.Name,
		Namespace:         d.Namespace,
		Replicas:          replicas,
		ReadyReplicas:     d.Status.ReadyReplicas,
		UpdatedReplicas:   d.Status.UpdatedReplicas,
		AvailableReplicas: d.Status.AvailableReplicas,
		CreatedAt:         d.CreationTimestamp.Time,
	}
}
