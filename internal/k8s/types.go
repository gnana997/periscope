// Package k8s implements typed operations against an EKS-backed Kubernetes
// API server. Per GROUND_RULES, every operation has the signature
// (ctx, p Provider, args) → (result, error). Operations return
// Periscope-defined DTOs (this file), not raw Kubernetes API types,
// so the API surface is stable and easy to expose later as MCP tools.
package k8s

import "time"

// Namespace is Periscope's projection of a Kubernetes namespace.
type Namespace struct {
	Name      string    `json:"name"`
	Phase     string    `json:"phase"`
	CreatedAt time.Time `json:"createdAt"`
}

// NamespaceList is the result of listing namespaces in a cluster.
type NamespaceList struct {
	Namespaces []Namespace `json:"namespaces"`
}
