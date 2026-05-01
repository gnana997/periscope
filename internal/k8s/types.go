// Package k8s implements typed operations against an EKS-backed (or
// kubeconfig-backed) Kubernetes API server. Per GROUND_RULES, every
// operation has the signature (ctx, p Provider, args) → (result, error).
// Operations return Periscope-defined DTOs (this file), not raw
// Kubernetes API types — stable surface for v3 MCP exposure.
package k8s

import "time"

// --- Namespace ---

type Namespace struct {
	Name      string    `json:"name"`
	Phase     string    `json:"phase"`
	CreatedAt time.Time `json:"createdAt"`
}

type NamespaceList struct {
	Namespaces []Namespace `json:"namespaces"`
}

// --- Pod (list view) ---

type Pod struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Phase     string    `json:"phase"`
	NodeName  string    `json:"nodeName,omitempty"`
	PodIP     string    `json:"podIP,omitempty"`
	// Ready is the kubectl-style "ready/total" container count, e.g. "2/3".
	Ready     string    `json:"ready"`
	Restarts  int32     `json:"restarts"`
	CreatedAt time.Time `json:"createdAt"`
}

type PodList struct {
	Pods []Pod `json:"pods"`
}

// --- Deployment (list view) ---

type Deployment struct {
	Name              string    `json:"name"`
	Namespace         string    `json:"namespace"`
	Replicas          int32     `json:"replicas"`
	ReadyReplicas     int32     `json:"readyReplicas"`
	UpdatedReplicas   int32     `json:"updatedReplicas"`
	AvailableReplicas int32     `json:"availableReplicas"`
	CreatedAt         time.Time `json:"createdAt"`
}

type DeploymentList struct {
	Deployments []Deployment `json:"deployments"`
}

// --- Service (list view) ---

type Service struct {
	Name       string        `json:"name"`
	Namespace  string        `json:"namespace"`
	Type       string        `json:"type"`
	ClusterIP  string        `json:"clusterIP,omitempty"`
	ExternalIP string        `json:"externalIP,omitempty"`
	Ports      []ServicePort `json:"ports"`
	CreatedAt  time.Time     `json:"createdAt"`
}

type ServicePort struct {
	Name     string `json:"name,omitempty"`
	Protocol string `json:"protocol"`
	Port     int32  `json:"port"`
	// TargetPort is a string because Kubernetes uses intstr.IntOrString
	// (a port can be a port number or a named port).
	TargetPort string `json:"targetPort"`
	NodePort   int32  `json:"nodePort,omitempty"`
}

type ServiceList struct {
	Services []Service `json:"services"`
}

// --- ConfigMap (list view) ---
//
// List view exposes the key count only — never key names or values.
// Same secrets-redaction principle applied here for consistency. Key
// names land in the read (Get) view; values never appear in v1.

type ConfigMap struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	KeyCount  int       `json:"keyCount"`
	CreatedAt time.Time `json:"createdAt"`
}

type ConfigMapList struct {
	ConfigMaps []ConfigMap `json:"configMaps"`
}
