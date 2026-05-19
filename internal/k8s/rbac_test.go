package k8s

// rbac_test.go — regression coverage for the RBAC + ServiceAccount
// YAML handlers. Each test asserts that the produced YAML carries
// `apiVersion:` and `kind:` lines — these were missing in v1.1.0
// because client-go's typed Get() returns objects with empty
// TypeMeta, and the handlers (unlike their peers in this package)
// weren't setting it explicitly. The blank TypeMeta surfaced in the
// SPA as a blank Monaco editor on Edit: parseIdentityFromYaml saw
// no apiVersion/kind, returned null, gvk stayed null, and the
// editor mount short-circuited before creating the Monaco model.
// Fixed in v1.1.1; this file locks the fix in.

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/clusters"
)

// swapNewClientFn is declared in watch_test.go; reused here.

func TestGetRoleYAMLSetsTypeMeta(t *testing.T) {
	cs := fake.NewSimpleClientset(&rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "reader", Namespace: "team-a"},
		Rules: []rbacv1.PolicyRule{
			{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}},
		},
	})
	swapNewClientFn(t, cs)

	yaml, err := GetRoleYAML(context.Background(), stubProvider{}, GetRoleArgs{
		Cluster: clusters.Cluster{Name: "test"}, Namespace: "team-a", Name: "reader",
	})
	if err != nil {
		t.Fatalf("GetRoleYAML: %v", err)
	}
	if !strings.Contains(yaml, "apiVersion: rbac.authorization.k8s.io/v1") {
		t.Fatalf("yaml missing apiVersion: rbac.authorization.k8s.io/v1\n%s", yaml)
	}
	if !strings.Contains(yaml, "kind: Role") {
		t.Fatalf("yaml missing kind: Role\n%s", yaml)
	}
}

func TestGetClusterRoleYAMLSetsTypeMeta(t *testing.T) {
	cs := fake.NewSimpleClientset(&rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "viewer"},
		Rules: []rbacv1.PolicyRule{
			{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods"}},
		},
	})
	swapNewClientFn(t, cs)

	yaml, err := GetClusterRoleYAML(context.Background(), stubProvider{}, GetClusterRoleArgs{
		Cluster: clusters.Cluster{Name: "test"}, Name: "viewer",
	})
	if err != nil {
		t.Fatalf("GetClusterRoleYAML: %v", err)
	}
	if !strings.Contains(yaml, "apiVersion: rbac.authorization.k8s.io/v1") {
		t.Fatalf("yaml missing apiVersion: rbac.authorization.k8s.io/v1\n%s", yaml)
	}
	if !strings.Contains(yaml, "kind: ClusterRole") {
		t.Fatalf("yaml missing kind: ClusterRole\n%s", yaml)
	}
}

func TestGetRoleBindingYAMLSetsTypeMeta(t *testing.T) {
	cs := fake.NewSimpleClientset(&rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "alice-reader", Namespace: "team-a"},
		Subjects:   []rbacv1.Subject{{Kind: "User", Name: "alice", APIGroup: "rbac.authorization.k8s.io"}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "reader",
		},
	})
	swapNewClientFn(t, cs)

	yaml, err := GetRoleBindingYAML(context.Background(), stubProvider{}, GetRoleBindingArgs{
		Cluster: clusters.Cluster{Name: "test"}, Namespace: "team-a", Name: "alice-reader",
	})
	if err != nil {
		t.Fatalf("GetRoleBindingYAML: %v", err)
	}
	if !strings.Contains(yaml, "apiVersion: rbac.authorization.k8s.io/v1") {
		t.Fatalf("yaml missing apiVersion: rbac.authorization.k8s.io/v1\n%s", yaml)
	}
	if !strings.Contains(yaml, "kind: RoleBinding") {
		t.Fatalf("yaml missing kind: RoleBinding\n%s", yaml)
	}
}

func TestGetClusterRoleBindingYAMLSetsTypeMeta(t *testing.T) {
	cs := fake.NewSimpleClientset(&rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "viewers"},
		Subjects:   []rbacv1.Subject{{Kind: "Group", Name: "viewers", APIGroup: "rbac.authorization.k8s.io"}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     "viewer",
		},
	})
	swapNewClientFn(t, cs)

	yaml, err := GetClusterRoleBindingYAML(context.Background(), stubProvider{}, GetClusterRoleBindingArgs{
		Cluster: clusters.Cluster{Name: "test"}, Name: "viewers",
	})
	if err != nil {
		t.Fatalf("GetClusterRoleBindingYAML: %v", err)
	}
	if !strings.Contains(yaml, "apiVersion: rbac.authorization.k8s.io/v1") {
		t.Fatalf("yaml missing apiVersion: rbac.authorization.k8s.io/v1\n%s", yaml)
	}
	if !strings.Contains(yaml, "kind: ClusterRoleBinding") {
		t.Fatalf("yaml missing kind: ClusterRoleBinding\n%s", yaml)
	}
}

func TestGetServiceAccountYAMLSetsTypeMeta(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "alloy", Namespace: "certwatch-monitoring"},
	})
	swapNewClientFn(t, cs)

	yaml, err := GetServiceAccountYAML(context.Background(), stubProvider{}, GetServiceAccountArgs{
		Cluster: clusters.Cluster{Name: "test"}, Namespace: "certwatch-monitoring", Name: "alloy",
	})
	if err != nil {
		t.Fatalf("GetServiceAccountYAML: %v", err)
	}
	if !strings.Contains(yaml, "apiVersion: v1") {
		t.Fatalf("yaml missing apiVersion: v1\n%s", yaml)
	}
	if !strings.Contains(yaml, "kind: ServiceAccount") {
		t.Fatalf("yaml missing kind: ServiceAccount\n%s", yaml)
	}
}
