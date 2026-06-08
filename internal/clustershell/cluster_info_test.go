package clustershell

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/clusters"
)

const fakeAgentCA = "-----BEGIN CERTIFICATE-----\nMIICfakeFakeCAForTestingOnly\n-----END CERTIFICATE-----\n"

// kubeconfigPayload returns a stripped cluster-info kubeconfig
// matching the shape kubeadm produces. Used to fake the
// kube-public/cluster-info ConfigMap in tests.
func kubeconfigPayload(t *testing.T, ca []byte) string {
	t.Helper()
	return "apiVersion: v1\nkind: Config\nclusters:\n- name: kubernetes\n  cluster:\n    server: https://10.0.0.1:6443\n    certificate-authority-data: " +
		base64.StdEncoding.EncodeToString(ca) + "\ncontexts: []\nusers: []\n"
}

func TestCAReader_AgentBackend_ReadsClusterInfoConfigMap(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ClusterInfoName,
			Namespace: ClusterInfoNamespace,
		},
		Data: map[string]string{
			clusterInfoDataKey: kubeconfigPayload(t, []byte(fakeAgentCA)),
		},
	})
	r := NewCAReader()
	cluster := clusters.Cluster{Name: "agent-east", Backend: clusters.BackendAgent}

	ca, err := r.Read(context.Background(), cs, cluster)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(ca) != fakeAgentCA {
		t.Errorf("CA mismatch:\n got: %q\nwant: %q", ca, fakeAgentCA)
	}
}

func TestCAReader_AgentBackend_CachesAcrossCalls(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ClusterInfoName, Namespace: ClusterInfoNamespace},
		Data:       map[string]string{clusterInfoDataKey: kubeconfigPayload(t, []byte(fakeAgentCA))},
	})
	r := NewCAReader()
	cluster := clusters.Cluster{Name: "agent-east", Backend: clusters.BackendAgent}

	if _, err := r.Read(context.Background(), cs, cluster); err != nil {
		t.Fatalf("first Read: %v", err)
	}

	// Swap to an empty clientset — a non-cached read would fail
	// (ConfigMap NotFound). A cached read should succeed and return
	// the same CA bytes.
	emptyCS := fake.NewSimpleClientset()
	ca, err := r.Read(context.Background(), emptyCS, cluster)
	if err != nil {
		t.Fatalf("cached Read: %v (cache did not satisfy the second call)", err)
	}
	if string(ca) != fakeAgentCA {
		t.Errorf("cached CA mismatch:\n got: %q\nwant: %q", ca, fakeAgentCA)
	}
}

func TestCAReader_AgentBackend_MissingConfigMapReturnsNoCA(t *testing.T) {
	cs := fake.NewSimpleClientset()
	r := NewCAReader()
	_, err := r.Read(context.Background(), cs, clusters.Cluster{Name: "agent-east", Backend: clusters.BackendAgent})
	if err == nil {
		t.Fatal("expected error when cluster-info ConfigMap is missing")
	}
	if !errors.Is(err, ErrNoClusterCA) {
		t.Errorf("expected ErrNoClusterCA, got: %v", err)
	}
	if !strings.Contains(err.Error(), "agent backend") {
		t.Errorf("error message should mention 'agent backend', got: %v", err)
	}
}

func TestCAReader_AgentBackend_ConfigMapWithoutCAData(t *testing.T) {
	// kubeconfig is parseable but has no clusters carrying CA data.
	bare := "apiVersion: v1\nkind: Config\nclusters: []\ncontexts: []\nusers: []\n"
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ClusterInfoName, Namespace: ClusterInfoNamespace},
		Data:       map[string]string{clusterInfoDataKey: bare},
	})
	r := NewCAReader()
	_, err := r.Read(context.Background(), cs, clusters.Cluster{Name: "x", Backend: clusters.BackendAgent})
	if err == nil {
		t.Fatal("expected error when cluster-info has no clusters with CA data")
	}
	if !errors.Is(err, ErrNoClusterCA) {
		t.Errorf("expected ErrNoClusterCA, got: %v", err)
	}
}

func TestCAReader_UnsupportedBackend(t *testing.T) {
	r := NewCAReader()
	_, err := r.Read(context.Background(), nil, clusters.Cluster{Name: "eks-prod", Backend: clusters.BackendEKS})
	if err == nil {
		t.Fatal("expected error for unsupported backend")
	}
	if !strings.Contains(err.Error(), "does not support backend") {
		t.Errorf("error should explain unsupported backend, got: %v", err)
	}
	if errors.Is(err, ErrNoClusterCA) {
		t.Error("backend-unsupported error should NOT wrap ErrNoClusterCA (distinct error class for the handler)")
	}
}

func TestCAReader_Invalidate(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: ClusterInfoName, Namespace: ClusterInfoNamespace},
		Data:       map[string]string{clusterInfoDataKey: kubeconfigPayload(t, []byte(fakeAgentCA))},
	})
	r := NewCAReader()
	cluster := clusters.Cluster{Name: "agent-east", Backend: clusters.BackendAgent}

	if _, err := r.Read(context.Background(), cs, cluster); err != nil {
		t.Fatalf("first Read: %v", err)
	}
	r.Invalidate(cluster.Name)

	// After invalidate, a read against an empty clientset should fail
	// because the cache no longer covers it.
	emptyCS := fake.NewSimpleClientset()
	if _, err := r.Read(context.Background(), emptyCS, cluster); err == nil {
		t.Fatal("expected error after invalidate — cache should have been dropped")
	}
}
