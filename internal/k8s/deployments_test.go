package k8s

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

func TestListDeployments(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	replicas := int32(3)

	fakeCS := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web", Namespace: "default", CreationTimestamp: metav1.NewTime(now),
			},
			Spec: appsv1.DeploymentSpec{Replicas: &replicas},
			Status: appsv1.DeploymentStatus{
				ReadyReplicas:     2,
				UpdatedReplicas:   3,
				AvailableReplicas: 2,
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "worker", Namespace: "kube-system", CreationTimestamp: metav1.NewTime(now),
			},
			Spec: appsv1.DeploymentSpec{},
		},
	)

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return fakeCS, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	cluster := clusters.Cluster{
		Name: "test", ARN: "arn:aws:eks:us-east-1:1:cluster/test", Region: "us-east-1",
	}

	t.Run("all namespaces", func(t *testing.T) {
		result, err := ListDeployments(context.Background(), stubProvider{}, ListDeploymentsArgs{Cluster: cluster})
		if err != nil {
			t.Fatalf("ListDeployments: %v", err)
		}
		if got, want := len(result.Deployments), 2; got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
	})

	t.Run("namespace filter and field mapping", func(t *testing.T) {
		result, err := ListDeployments(context.Background(), stubProvider{}, ListDeploymentsArgs{
			Cluster: cluster, Namespace: "default",
		})
		if err != nil {
			t.Fatalf("ListDeployments: %v", err)
		}
		if got, want := len(result.Deployments), 1; got != want {
			t.Fatalf("got %d, want %d", got, want)
		}
		d := result.Deployments[0]
		if d.Replicas != 3 {
			t.Errorf("Replicas = %d, want 3", d.Replicas)
		}
		if d.ReadyReplicas != 2 {
			t.Errorf("ReadyReplicas = %d, want 2", d.ReadyReplicas)
		}
		if d.UpdatedReplicas != 3 {
			t.Errorf("UpdatedReplicas = %d, want 3", d.UpdatedReplicas)
		}
		if d.AvailableReplicas != 2 {
			t.Errorf("AvailableReplicas = %d, want 2", d.AvailableReplicas)
		}
	})

	t.Run("nil spec.replicas defaults to 0", func(t *testing.T) {
		result, err := ListDeployments(context.Background(), stubProvider{}, ListDeploymentsArgs{
			Cluster: cluster, Namespace: "kube-system",
		})
		if err != nil {
			t.Fatalf("ListDeployments: %v", err)
		}
		if result.Deployments[0].Replicas != 0 {
			t.Errorf("Replicas = %d, want 0 (nil spec.replicas)", result.Deployments[0].Replicas)
		}
	})
}
