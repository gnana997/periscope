package k8s

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListNamespacesArgs struct {
	Cluster clusters.Cluster
}

func ListNamespaces(ctx context.Context, p credentials.Provider, args ListNamespacesArgs) (NamespaceList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return NamespaceList{}, fmt.Errorf("build clientset: %w", err)
	}

	raw, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return NamespaceList{}, fmt.Errorf("list namespaces: %w", err)
	}

	out := NamespaceList{Namespaces: make([]Namespace, 0, len(raw.Items))}
	for _, ns := range raw.Items {
		out.Namespaces = append(out.Namespaces, namespaceSummary(&ns))
	}
	return out, nil
}

type GetNamespaceArgs struct {
	Cluster clusters.Cluster
	Name    string
}

func GetNamespace(ctx context.Context, p credentials.Provider, args GetNamespaceArgs) (NamespaceDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return NamespaceDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Namespaces().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return NamespaceDetail{}, fmt.Errorf("get namespace %s: %w", args.Name, err)
	}
	return NamespaceDetail{
		Namespace:   namespaceSummary(raw),
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
	}, nil
}

func GetNamespaceYAML(ctx context.Context, p credentials.Provider, args GetNamespaceArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Namespaces().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get namespace %s: %w", args.Name, err)
	}
	raw.APIVersion = "v1"
	raw.Kind = "Namespace"
	return formatYAML(raw)
}

func namespaceSummary(ns *corev1.Namespace) Namespace {
	return Namespace{
		Name:      ns.Name,
		Phase:     string(ns.Status.Phase),
		CreatedAt: ns.CreationTimestamp.Time,
	}
}
