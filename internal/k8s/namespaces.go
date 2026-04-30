package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// ListNamespacesArgs are the arguments to ListNamespaces.
type ListNamespacesArgs struct {
	Cluster clusters.Cluster
}

// ListNamespaces returns the namespaces visible to the Provider's
// credentials in the target cluster. The caller's RBAC scopes the result.
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
		out.Namespaces = append(out.Namespaces, Namespace{
			Name:      ns.Name,
			Phase:     string(ns.Status.Phase),
			CreatedAt: ns.CreationTimestamp.Time,
		})
	}
	return out, nil
}
