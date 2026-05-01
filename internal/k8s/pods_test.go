package k8s

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

func TestListPods(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	fakeCS := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "web", Namespace: "default", CreationTimestamp: metav1.NewTime(now),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app"}, {Name: "sidecar"},
				},
				NodeName: "node-1",
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				PodIP: "10.0.0.5",
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "app", Ready: true, RestartCount: 1},
					{Name: "sidecar", Ready: false, RestartCount: 3},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "worker", Namespace: "kube-system", CreationTimestamp: metav1.NewTime(now),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "main"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodPending},
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
		result, err := ListPods(context.Background(), stubProvider{}, ListPodsArgs{Cluster: cluster})
		if err != nil {
			t.Fatalf("ListPods: %v", err)
		}
		if got, want := len(result.Pods), 2; got != want {
			t.Fatalf("got %d pods, want %d", got, want)
		}
	})

	t.Run("namespace filter and field mapping", func(t *testing.T) {
		result, err := ListPods(context.Background(), stubProvider{}, ListPodsArgs{
			Cluster: cluster, Namespace: "default",
		})
		if err != nil {
			t.Fatalf("ListPods: %v", err)
		}
		if got, want := len(result.Pods), 1; got != want {
			t.Fatalf("got %d pods, want %d", got, want)
		}
		p := result.Pods[0]
		if p.Name != "web" {
			t.Errorf("Name = %q, want web", p.Name)
		}
		if p.Phase != "Running" {
			t.Errorf("Phase = %q, want Running", p.Phase)
		}
		if p.Ready != "1/2" {
			t.Errorf("Ready = %q, want 1/2", p.Ready)
		}
		if p.Restarts != 4 {
			t.Errorf("Restarts = %d, want 4", p.Restarts)
		}
		if p.NodeName != "node-1" {
			t.Errorf("NodeName = %q, want node-1", p.NodeName)
		}
		if p.PodIP != "10.0.0.5" {
			t.Errorf("PodIP = %q, want 10.0.0.5", p.PodIP)
		}
	})
}
