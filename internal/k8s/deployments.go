package k8s

import (
	"context"
	"fmt"

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
		var replicas int32
		if d.Spec.Replicas != nil {
			replicas = *d.Spec.Replicas
		}
		out.Deployments = append(out.Deployments, Deployment{
			Name:              d.Name,
			Namespace:         d.Namespace,
			Replicas:          replicas,
			ReadyReplicas:     d.Status.ReadyReplicas,
			UpdatedReplicas:   d.Status.UpdatedReplicas,
			AvailableReplicas: d.Status.AvailableReplicas,
			CreatedAt:         d.CreationTimestamp.Time,
		})
	}
	return out, nil
}
