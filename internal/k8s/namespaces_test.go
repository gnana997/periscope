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

func TestListNamespaces(t *testing.T) {
	now := time.Date(2026, 4, 30, 12, 0, 0, 0, time.UTC)
	fakeCS := fake.NewSimpleClientset(
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "default", CreationTimestamp: metav1.NewTime(now)},
			Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: "kube-system", CreationTimestamp: metav1.NewTime(now)},
			Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
		},
	)

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return fakeCS, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	result, err := ListNamespaces(context.Background(), stubProvider{}, ListNamespacesArgs{
		Cluster: clusters.Cluster{
			Name:   "demo",
			ARN:    "arn:aws:eks:us-east-1:123456789012:cluster/demo",
			Region: "us-east-1",
		},
	})
	if err != nil {
		t.Fatalf("ListNamespaces: %v", err)
	}
	if got, want := len(result.Namespaces), 2; got != want {
		t.Fatalf("got %d namespaces, want %d", got, want)
	}

	got := map[string]Namespace{}
	for _, n := range result.Namespaces {
		got[n.Name] = n
	}
	for _, want := range []string{"default", "kube-system"} {
		ns, ok := got[want]
		if !ok {
			t.Errorf("missing namespace %q", want)
			continue
		}
		if ns.Phase != "Active" {
			t.Errorf("namespace %q phase = %q, want Active", want, ns.Phase)
		}
		if !ns.CreatedAt.Equal(now) {
			t.Errorf("namespace %q createdAt = %v, want %v", want, ns.CreatedAt, now)
		}
	}
}

func TestListNamespaces_clientError(t *testing.T) {
	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return nil, errStub
	}
	t.Cleanup(func() { newClientFn = orig })

	_, err := ListNamespaces(context.Background(), stubProvider{}, ListNamespacesArgs{
		Cluster: clusters.Cluster{Name: "x", ARN: "arn:aws:eks:us-east-1:1:cluster/x", Region: "us-east-1"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

var errStub = stubError("client build failed")

type stubError string

func (e stubError) Error() string { return string(e) }
