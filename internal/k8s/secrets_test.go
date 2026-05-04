package k8s

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

func TestGetSecretYAMLWithKeys(t *testing.T) {
	fakeCS := fake.NewSimpleClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "default"},
			Type:       corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"password": []byte("hunter2"),
				"username": []byte("alice"),
				"token":    []byte("xyz"),
			},
		},
	)

	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return fakeCS, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	yaml, keys, err := GetSecretYAMLWithKeys(context.Background(), stubProvider{}, GetSecretArgs{
		Cluster: clusters.Cluster{Name: "test"}, Namespace: "default", Name: "creds",
	})
	if err != nil {
		t.Fatalf("GetSecretYAMLWithKeys: %v", err)
	}

	// Keys must be sorted — the audit row depends on stable ordering
	// so duplicate calls produce identical Extra payloads.
	want := []string{"password", "token", "username"}
	if len(keys) != len(want) {
		t.Fatalf("keys len: got %v, want %v", keys, want)
	}
	for i, k := range want {
		if keys[i] != k {
			t.Fatalf("keys[%d]: got %q, want %q (full %v)", i, keys[i], k, keys)
		}
	}

	if !strings.Contains(yaml, "kind: Secret") {
		t.Fatalf("yaml missing kind: Secret\n%s", yaml)
	}
	if !strings.Contains(yaml, "apiVersion: v1") {
		t.Fatalf("yaml missing apiVersion: v1\n%s", yaml)
	}
}

func TestGetSecretYAMLWithKeysEmpty(t *testing.T) {
	fakeCS := fake.NewSimpleClientset(
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "empty", Namespace: "default"},
			Type:       corev1.SecretTypeOpaque,
		},
	)
	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return fakeCS, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	_, keys, err := GetSecretYAMLWithKeys(context.Background(), stubProvider{}, GetSecretArgs{
		Cluster: clusters.Cluster{Name: "test"}, Namespace: "default", Name: "empty",
	})
	if err != nil {
		t.Fatalf("GetSecretYAMLWithKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("expected empty keys, got %v", keys)
	}
}
