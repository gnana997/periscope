package k8s

// cani.go — thin wrappers over the apiserver's authorization API.
//
// CheckSAR issues a SelfSubjectAccessReview for a single (verb, resource,
// namespace[, subresource]) attribute set. ListSSRR issues a
// SelfSubjectRulesReview for one namespace and returns the full rule set
// the apiserver evaluates for the impersonated identity.
//
// Both functions use NewClientset (which already applies impersonation
// from the Provider), so a single call site works for shared / tier /
// raw authz modes — the only difference is what the apiserver sees on
// the Impersonate-User / Impersonate-Group headers, which the rest of
// the package handles.

import (
	"context"
	"fmt"

	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// CheckSAR issues a SelfSubjectAccessReview against the cluster under
// the Provider's impersonation. Returns (allowed, reason, err) where
// reason is the apiserver-supplied explanation when populated; callers
// should fall back to a generic per-verb message when empty.
func CheckSAR(ctx context.Context, p credentials.Provider, c clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error) {
	cs, err := newClientFn(ctx, p, c)
	if err != nil {
		return false, "", fmt.Errorf("build clientset: %w", err)
	}
	review := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &attr,
		},
	}
	out, err := cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return false, "", fmt.Errorf("create SelfSubjectAccessReview: %w", err)
	}
	return out.Status.Allowed, out.Status.Reason, nil
}

// ListSSRR issues a SelfSubjectRulesReview for a single namespace.
// SSRR is namespaced — the returned rules cover only the supplied
// namespace, plus the user's cluster-scoped non-resource rules. Callers
// must fall back to per-check SAR for cluster-scoped resources.
func ListSSRR(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace string) (*authv1.SubjectRulesReviewStatus, error) {
	if namespace == "" {
		return nil, fmt.Errorf("ListSSRR: namespace is required (SSRR is namespaced)")
	}
	cs, err := newClientFn(ctx, p, c)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	review := &authv1.SelfSubjectRulesReview{
		Spec: authv1.SelfSubjectRulesReviewSpec{Namespace: namespace},
	}
	out, err := cs.AuthorizationV1().SelfSubjectRulesReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("create SelfSubjectRulesReview: %w", err)
	}
	return &out.Status, nil
}

// SSRRCheck is the minimal attribute set EvaluateSSRR needs. Mirrors
// the call-site shape so handlers don't have to round-trip through
// authv1 types just to evaluate a rule.
type SSRRCheck struct {
	Verb      string
	Group     string
	Resource  string
	Name      string
}

// EvaluateSSRR reports whether the rules returned by SSRR permit the
// supplied check. K8s rule semantics: an empty list in a rule field
// means "no match" for that field; "*" means wildcard. A rule grants
// the action when verb, group, and resource all match (and name, if
// present in the rule, matches too).
//
// The apiserver-side evaluator already implements this; we re-implement
// it client-side because SSRR returns the full rule set rather than
// pre-evaluating each tuple. Faithfulness to k8s semantics is the
// invariant — when in doubt, mirror what the kubectl auth can-i logic
// does.
func EvaluateSSRR(rules *authv1.SubjectRulesReviewStatus, check SSRRCheck) bool {
	if rules == nil {
		return false
	}
	for _, r := range rules.ResourceRules {
		if !ruleMatch(r.Verbs, check.Verb) {
			continue
		}
		if !ruleMatch(r.APIGroups, check.Group) {
			continue
		}
		if !ruleMatch(r.Resources, check.Resource) {
			continue
		}
		// ResourceNames empty in the rule = applies to all names.
		// Non-empty = restricted to that set.
		if len(r.ResourceNames) > 0 && check.Name != "" && !ruleMatchExact(r.ResourceNames, check.Name) {
			continue
		}
		return true
	}
	return false
}

// ruleMatch reports whether haystack contains needle, treating "*" in
// haystack as a wildcard match (k8s RBAC rule semantics).
func ruleMatch(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == "*" || h == needle {
			return true
		}
	}
	return false
}

// ruleMatchExact is ruleMatch without wildcard semantics — k8s
// resourceNames don't honor "*".
func ruleMatchExact(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
