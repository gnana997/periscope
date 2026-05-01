package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// --- Args ---

type ListRolesArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetRoleArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

type ListClusterRolesArgs struct {
	Cluster clusters.Cluster
}

type GetClusterRoleArgs struct {
	Cluster clusters.Cluster
	Name    string
}

type ListRoleBindingsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetRoleBindingArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

type ListClusterRoleBindingsArgs struct {
	Cluster clusters.Cluster
}

type GetClusterRoleBindingArgs struct {
	Cluster clusters.Cluster
	Name    string
}

type ListServiceAccountsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

type GetServiceAccountArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

// --- List ---

func ListRoles(ctx context.Context, p credentials.Provider, args ListRolesArgs) (RoleList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return RoleList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().Roles(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return RoleList{}, fmt.Errorf("list roles: %w", err)
	}
	out := RoleList{Roles: make([]Role, 0, len(raw.Items))}
	for _, r := range raw.Items {
		out.Roles = append(out.Roles, Role{
			Name:      r.Name,
			Namespace: r.Namespace,
			RuleCount: len(r.Rules),
			CreatedAt: r.CreationTimestamp.Time,
		})
	}
	return out, nil
}

func ListClusterRoles(ctx context.Context, p credentials.Provider, args ListClusterRolesArgs) (ClusterRoleList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ClusterRoleList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterRoleList{}, fmt.Errorf("list clusterroles: %w", err)
	}
	out := ClusterRoleList{ClusterRoles: make([]ClusterRole, 0, len(raw.Items))}
	for _, r := range raw.Items {
		out.ClusterRoles = append(out.ClusterRoles, ClusterRole{
			Name:      r.Name,
			RuleCount: len(r.Rules),
			CreatedAt: r.CreationTimestamp.Time,
		})
	}
	return out, nil
}

func ListRoleBindings(ctx context.Context, p credentials.Provider, args ListRoleBindingsArgs) (RoleBindingList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return RoleBindingList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().RoleBindings(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return RoleBindingList{}, fmt.Errorf("list rolebindings: %w", err)
	}
	out := RoleBindingList{RoleBindings: make([]RoleBinding, 0, len(raw.Items))}
	for _, rb := range raw.Items {
		out.RoleBindings = append(out.RoleBindings, RoleBinding{
			Name:         rb.Name,
			Namespace:    rb.Namespace,
			RoleRef:      rb.RoleRef.Kind + "/" + rb.RoleRef.Name,
			SubjectCount: len(rb.Subjects),
			CreatedAt:    rb.CreationTimestamp.Time,
		})
	}
	return out, nil
}

func ListClusterRoleBindings(ctx context.Context, p credentials.Provider, args ListClusterRoleBindingsArgs) (ClusterRoleBindingList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ClusterRoleBindingList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterRoleBindingList{}, fmt.Errorf("list clusterrolebindings: %w", err)
	}
	out := ClusterRoleBindingList{ClusterRoleBindings: make([]ClusterRoleBinding, 0, len(raw.Items))}
	for _, crb := range raw.Items {
		out.ClusterRoleBindings = append(out.ClusterRoleBindings, ClusterRoleBinding{
			Name:         crb.Name,
			RoleRef:      crb.RoleRef.Kind + "/" + crb.RoleRef.Name,
			SubjectCount: len(crb.Subjects),
			CreatedAt:    crb.CreationTimestamp.Time,
		})
	}
	return out, nil
}

func ListServiceAccounts(ctx context.Context, p credentials.Provider, args ListServiceAccountsArgs) (ServiceAccountList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ServiceAccountList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().ServiceAccounts(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ServiceAccountList{}, fmt.Errorf("list serviceaccounts: %w", err)
	}
	out := ServiceAccountList{ServiceAccounts: make([]ServiceAccount, 0, len(raw.Items))}
	for _, sa := range raw.Items {
		out.ServiceAccounts = append(out.ServiceAccounts, ServiceAccount{
			Name:      sa.Name,
			Namespace: sa.Namespace,
			Secrets:   len(sa.Secrets),
			CreatedAt: sa.CreationTimestamp.Time,
		})
	}
	return out, nil
}

// --- Get detail ---

func GetRole(ctx context.Context, p credentials.Provider, args GetRoleArgs) (RoleDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return RoleDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().Roles(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return RoleDetail{}, fmt.Errorf("get role %s/%s: %w", args.Namespace, args.Name, err)
	}
	return RoleDetail{
		Role: Role{
			Name:      raw.Name,
			Namespace: raw.Namespace,
			RuleCount: len(raw.Rules),
			CreatedAt: raw.CreationTimestamp.Time,
		},
		Rules:       convertRules(raw.Rules),
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetClusterRole(ctx context.Context, p credentials.Provider, args GetClusterRoleArgs) (ClusterRoleDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ClusterRoleDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().ClusterRoles().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return ClusterRoleDetail{}, fmt.Errorf("get clusterrole %s: %w", args.Name, err)
	}

	var aggLabels []string
	if raw.AggregationRule != nil {
		for _, sel := range raw.AggregationRule.ClusterRoleSelectors {
			parts := make([]string, 0, len(sel.MatchLabels))
			for k, v := range sel.MatchLabels {
				parts = append(parts, k+"="+v)
			}
			sort.Strings(parts)
			aggLabels = append(aggLabels, strings.Join(parts, ", "))
		}
	}

	return ClusterRoleDetail{
		ClusterRole: ClusterRole{
			Name:      raw.Name,
			RuleCount: len(raw.Rules),
			CreatedAt: raw.CreationTimestamp.Time,
		},
		Rules:             convertRules(raw.Rules),
		AggregationLabels: aggLabels,
		Labels:            raw.Labels,
		Annotations:       raw.Annotations,
	}, nil
}

func GetRoleBinding(ctx context.Context, p credentials.Provider, args GetRoleBindingArgs) (RoleBindingDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return RoleBindingDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().RoleBindings(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return RoleBindingDetail{}, fmt.Errorf("get rolebinding %s/%s: %w", args.Namespace, args.Name, err)
	}
	return RoleBindingDetail{
		Name:      raw.Name,
		Namespace: raw.Namespace,
		CreatedAt: raw.CreationTimestamp.Time,
		RoleRef:   RoleRef{Kind: raw.RoleRef.Kind, Name: raw.RoleRef.Name},
		Subjects:  convertSubjects(raw.Subjects),
		Labels:    raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetClusterRoleBinding(ctx context.Context, p credentials.Provider, args GetClusterRoleBindingArgs) (ClusterRoleBindingDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ClusterRoleBindingDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return ClusterRoleBindingDetail{}, fmt.Errorf("get clusterrolebinding %s: %w", args.Name, err)
	}
	return ClusterRoleBindingDetail{
		Name:      raw.Name,
		CreatedAt: raw.CreationTimestamp.Time,
		RoleRef:   RoleRef{Kind: raw.RoleRef.Kind, Name: raw.RoleRef.Name},
		Subjects:  convertSubjects(raw.Subjects),
		Labels:    raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetServiceAccount(ctx context.Context, p credentials.Provider, args GetServiceAccountArgs) (ServiceAccountDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ServiceAccountDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().ServiceAccounts(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return ServiceAccountDetail{}, fmt.Errorf("get serviceaccount %s/%s: %w", args.Namespace, args.Name, err)
	}
	secrets := make([]string, 0, len(raw.Secrets))
	for _, s := range raw.Secrets {
		secrets = append(secrets, s.Name)
	}
	return ServiceAccountDetail{
		ServiceAccount: ServiceAccount{
			Name:      raw.Name,
			Namespace: raw.Namespace,
			Secrets:   len(raw.Secrets),
			CreatedAt: raw.CreationTimestamp.Time,
		},
		SecretNames: secrets,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

// --- YAML ---

func GetRoleYAML(ctx context.Context, p credentials.Provider, args GetRoleArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().Roles(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get role: %w", err)
	}
	return formatYAML(raw)
}

func GetClusterRoleYAML(ctx context.Context, p credentials.Provider, args GetClusterRoleArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().ClusterRoles().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get clusterrole: %w", err)
	}
	return formatYAML(raw)
}

func GetRoleBindingYAML(ctx context.Context, p credentials.Provider, args GetRoleBindingArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().RoleBindings(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get rolebinding: %w", err)
	}
	return formatYAML(raw)
}

func GetClusterRoleBindingYAML(ctx context.Context, p credentials.Provider, args GetClusterRoleBindingArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get clusterrolebinding: %w", err)
	}
	return formatYAML(raw)
}

func GetServiceAccountYAML(ctx context.Context, p credentials.Provider, args GetServiceAccountArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().ServiceAccounts(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get serviceaccount: %w", err)
	}
	return formatYAML(raw)
}

// --- Helpers ---

func convertRules(rules []rbacv1.PolicyRule) []PolicyRule {
	out := make([]PolicyRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, PolicyRule{
			Verbs:           r.Verbs,
			APIGroups:       r.APIGroups,
			Resources:       r.Resources,
			ResourceNames:   r.ResourceNames,
			NonResourceURLs: r.NonResourceURLs,
		})
	}
	return out
}

func convertSubjects(subjects []rbacv1.Subject) []RBACSubject {
	out := make([]RBACSubject, 0, len(subjects))
	for _, s := range subjects {
		out = append(out, RBACSubject{
			Kind:      s.Kind,
			Name:      s.Name,
			Namespace: s.Namespace,
		})
	}
	return out
}
