package k8s

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// fakeDeployment is a minimal Deployment object stamped with two
// managedFields entries: one from kustomize-controller (GitOps) and one
// from Periscope itself. The test asserts the response shape preserves
// both — that's what powers the SPA's per-field ownership UI.
func fakeDeployment() *unstructured.Unstructured {
	now := metav1.NewTime(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":            "nginx-app",
				"namespace":       "default",
				"resourceVersion": "8429103",
				"generation":      int64(7),
				"managedFields": []interface{}{
					map[string]interface{}{
						"manager":    "kustomize-controller",
						"operation":  "Apply",
						"apiVersion": "apps/v1",
						"time":       now.Format(time.RFC3339),
						"fieldsType": "FieldsV1",
						"fieldsV1": map[string]interface{}{
							"f:spec": map[string]interface{}{
								"f:replicas": map[string]interface{}{},
							},
						},
					},
					map[string]interface{}{
						"manager":    "periscope-spa",
						"operation":  "Apply",
						"apiVersion": "apps/v1",
						"time":       now.Format(time.RFC3339),
						"fieldsType": "FieldsV1",
						"fieldsV1": map[string]interface{}{
							"f:metadata": map[string]interface{}{
								"f:labels": map[string]interface{}{
									"f:app.kubernetes.io/version": map[string]interface{}{},
								},
							},
						},
					},
				},
			},
			"spec": map[string]interface{}{
				"replicas": int64(3),
			},
		},
	}
	obj.SetResourceVersion("8429103")
	obj.SetGeneration(7)
	return obj
}

func TestGetResourceMeta_Namespaced(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		gvr: "DeploymentList",
	}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, fakeDeployment())

	orig := newDynamicClientForMeta
	newDynamicClientForMeta = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (dynamic.Interface, error) {
		return fake, nil
	}
	t.Cleanup(func() { newDynamicClientForMeta = orig })

	cluster := clusters.Cluster{Name: "test", ARN: "arn:aws:eks:us-east-1:1:cluster/test", Region: "us-east-1"}
	got, err := GetResourceMeta(context.Background(), stubProvider{}, MetaArgs{
		Cluster: cluster, Group: "apps", Version: "v1", Resource: "deployments",
		Namespace: "default", Name: "nginx-app",
	})
	if err != nil {
		t.Fatalf("GetResourceMeta: %v", err)
	}

	if got.ResourceVersion != "8429103" {
		t.Errorf("ResourceVersion = %q, want %q", got.ResourceVersion, "8429103")
	}
	if got.Generation != 7 {
		t.Errorf("Generation = %d, want 7", got.Generation)
	}
	if len(got.ManagedFields) != 2 {
		t.Fatalf("ManagedFields = %d entries, want 2", len(got.ManagedFields))
	}

	managers := map[string]bool{}
	for _, m := range got.ManagedFields {
		managers[m.Manager] = true
	}
	if !managers["kustomize-controller"] {
		t.Errorf("expected kustomize-controller in ManagedFields, got %v", managers)
	}
	if !managers["periscope-spa"] {
		t.Errorf("expected periscope-spa in ManagedFields, got %v", managers)
	}
}

func TestGetResourceMeta_NotFound(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{gvr: "DeploymentList"}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds)

	orig := newDynamicClientForMeta
	newDynamicClientForMeta = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (dynamic.Interface, error) {
		return fake, nil
	}
	t.Cleanup(func() { newDynamicClientForMeta = orig })

	cluster := clusters.Cluster{Name: "test"}
	_, err := GetResourceMeta(context.Background(), stubProvider{}, MetaArgs{
		Cluster: cluster, Group: "apps", Version: "v1", Resource: "deployments",
		Namespace: "default", Name: "missing",
	})
	if err == nil {
		t.Fatal("expected NotFound error, got nil")
	}
}

func TestGetResourceMeta_ClusterScoped(t *testing.T) {
	// Namespaces are cluster-scoped — Namespace field on MetaArgs is empty.
	gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{gvr: "NamespaceList"}

	ns := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Namespace",
			"metadata": map[string]interface{}{
				"name":            "kube-system",
				"resourceVersion": "1234",
				"generation":      int64(1),
				"managedFields": []interface{}{
					map[string]interface{}{
						"manager":    "kube-apiserver",
						"operation":  "Update",
						"apiVersion": "v1",
						"fieldsType": "FieldsV1",
					},
				},
			},
		},
	}
	ns.SetResourceVersion("1234")
	ns.SetGeneration(1)

	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, ns)
	orig := newDynamicClientForMeta
	newDynamicClientForMeta = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (dynamic.Interface, error) {
		return fake, nil
	}
	t.Cleanup(func() { newDynamicClientForMeta = orig })

	got, err := GetResourceMeta(context.Background(), stubProvider{}, MetaArgs{
		Cluster: clusters.Cluster{Name: "test"},
		Group:   "", Version: "v1", Resource: "namespaces",
		Namespace: "", // cluster-scoped
		Name:      "kube-system",
	})
	if err != nil {
		t.Fatalf("GetResourceMeta cluster-scoped: %v", err)
	}
	if got.ResourceVersion != "1234" {
		t.Errorf("ResourceVersion = %q, want %q", got.ResourceVersion, "1234")
	}
	if len(got.ManagedFields) != 1 || got.ManagedFields[0].Manager != "kube-apiserver" {
		t.Errorf("ManagedFields = %+v, want one entry from kube-apiserver", got.ManagedFields)
	}
}
