package k8s

import (
	"context"
	"errors"
	"testing"

	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

func TestEvaluateSSRR(t *testing.T) {
	rules := &authv1.SubjectRulesReviewStatus{
		ResourceRules: []authv1.ResourceRule{
			// Anything in apps/deployments.
			{Verbs: []string{"get", "list", "watch", "patch", "update"}, APIGroups: []string{"apps"}, Resources: []string{"deployments"}},
			// Read-only on core pods.
			{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{""}, Resources: []string{"pods"}},
			// Specific named secret only.
			{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"secrets"}, ResourceNames: []string{"my-secret"}},
			// Wildcard verbs on configmaps.
			{Verbs: []string{"*"}, APIGroups: []string{""}, Resources: []string{"configmaps"}},
		},
	}

	cases := []struct {
		name    string
		check   SSRRCheck
		want    bool
	}{
		{"deployments patch ok", SSRRCheck{Verb: "patch", Group: "apps", Resource: "deployments"}, true},
		{"deployments delete denied (verb missing)", SSRRCheck{Verb: "delete", Group: "apps", Resource: "deployments"}, false},
		{"pods get ok", SSRRCheck{Verb: "get", Group: "", Resource: "pods"}, true},
		{"pods delete denied", SSRRCheck{Verb: "delete", Group: "", Resource: "pods"}, false},
		{"named secret get ok", SSRRCheck{Verb: "get", Group: "", Resource: "secrets", Name: "my-secret"}, true},
		{"other secret get denied (resourceName mismatch)", SSRRCheck{Verb: "get", Group: "", Resource: "secrets", Name: "other"}, false},
		{"configmaps wildcard verb allows delete", SSRRCheck{Verb: "delete", Group: "", Resource: "configmaps"}, true},
		{"unrelated resource denied", SSRRCheck{Verb: "get", Group: "", Resource: "services"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateSSRR(rules, tc.check)
			if got != tc.want {
				t.Errorf("EvaluateSSRR(%+v) = %v, want %v", tc.check, got, tc.want)
			}
		})
	}
}

func TestEvaluateSSRR_NilRules(t *testing.T) {
	if EvaluateSSRR(nil, SSRRCheck{Verb: "get", Resource: "pods"}) {
		t.Error("nil rules should return false")
	}
}

func TestEvaluateSSRR_WildcardGroup(t *testing.T) {
	// Cluster-admin-style wildcard: any verb, any group, any resource.
	rules := &authv1.SubjectRulesReviewStatus{
		ResourceRules: []authv1.ResourceRule{
			{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}},
		},
	}
	if !EvaluateSSRR(rules, SSRRCheck{Verb: "delete", Group: "apps", Resource: "deployments"}) {
		t.Error("wildcard rule should allow delete deployments")
	}
	if !EvaluateSSRR(rules, SSRRCheck{Verb: "create", Group: "", Resource: "pods"}) {
		t.Error("wildcard rule should allow create pods")
	}
}

func TestCheckSAR_PassesThroughResult(t *testing.T) {
	fakeCS := fake.NewSimpleClientset()
	fakeCS.Fake.PrependReactor("create", "selfsubjectaccessreviews",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, &authv1.SelfSubjectAccessReview{
				Status: authv1.SubjectAccessReviewStatus{
					Allowed: true,
					Reason:  "RBAC: allowed by ClusterRoleBinding admin",
				},
			}, nil
		})

	swap := func(t *testing.T, cs kubernetes.Interface) {
		orig := newClientFn
		newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
			return cs, nil
		}
		t.Cleanup(func() { newClientFn = orig })
	}
	swap(t, fakeCS)

	allowed, reason, err := CheckSAR(context.Background(), stubProvider{}, clusters.Cluster{Name: "c"}, authv1.ResourceAttributes{
		Verb: "delete", Resource: "pods", Namespace: "default",
	})
	if err != nil {
		t.Fatalf("CheckSAR err: %v", err)
	}
	if !allowed {
		t.Errorf("allowed = false, want true")
	}
	if reason == "" {
		t.Error("reason empty; expected reactor-supplied string")
	}
}

func TestCheckSAR_PropagatesError(t *testing.T) {
	fakeCS := fake.NewSimpleClientset()
	fakeCS.Fake.PrependReactor("create", "selfsubjectaccessreviews",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("apiserver kapow")
		})
	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return fakeCS, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	_, _, err := CheckSAR(context.Background(), stubProvider{}, clusters.Cluster{Name: "c"}, authv1.ResourceAttributes{Verb: "get", Resource: "pods"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestListSSRR_RejectsEmptyNamespace(t *testing.T) {
	_, err := ListSSRR(context.Background(), stubProvider{}, clusters.Cluster{Name: "c"}, "")
	if err == nil {
		t.Fatal("expected error for empty namespace")
	}
}

func TestListSSRR_ReturnsRules(t *testing.T) {
	fakeCS := fake.NewSimpleClientset()
	fakeCS.Fake.PrependReactor("create", "selfsubjectrulesreviews",
		func(action k8stesting.Action) (bool, runtime.Object, error) {
			return true, &authv1.SelfSubjectRulesReview{
				Status: authv1.SubjectRulesReviewStatus{
					ResourceRules: []authv1.ResourceRule{
						{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods"}},
					},
				},
			}, nil
		})
	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return fakeCS, nil
	}
	t.Cleanup(func() { newClientFn = orig })

	st, err := ListSSRR(context.Background(), stubProvider{}, clusters.Cluster{Name: "c"}, "default")
	if err != nil {
		t.Fatalf("ListSSRR err: %v", err)
	}
	if len(st.ResourceRules) != 1 {
		t.Fatalf("got %d rules, want 1", len(st.ResourceRules))
	}
	if !EvaluateSSRR(st, SSRRCheck{Verb: "get", Resource: "pods"}) {
		t.Error("returned rules do not allow get pods (round-trip failed)")
	}
}
