// Package clusters owns the dashboard's cluster registry — the passive
// list of clusters Periscope knows about. Per GROUND_RULES, the
// registry never grants access; access is enforced by IAM + aws-auth /
// EKS Access Entries + Kubernetes RBAC for EKS clusters, and by whatever
// the kubeconfig grants for kubeconfig-backed clusters.
package clusters

import "strings"

// Cluster backend identifiers. EKS clusters authenticate via the
// dashboard's Provider (AWS IAM); kubeconfig clusters authenticate via
// a kubeconfig file (useful for local dev — KIND, minikube — and for
// non-AWS clusters).
//
// Note: kubeconfig-backed clusters do not participate in v2's per-user
// identity pass-through. The kubeconfig is one identity, applied to all
// users of the dashboard.
const (
	BackendEKS        = "eks"
	BackendKubeconfig = "kubeconfig"
)

// Cluster identifies a Kubernetes cluster the dashboard can talk to.
type Cluster struct {
	// Name is the human-readable identifier used in URLs and the UI.
	// Must be unique within the registry.
	Name string `yaml:"name" json:"name"`

	// Backend selects the auth path. "eks" (default) or "kubeconfig".
	Backend string `yaml:"backend,omitempty" json:"backend"`

	// EKS backend fields:

	// ARN is the full EKS cluster ARN.
	ARN string `yaml:"arn,omitempty" json:"arn,omitempty"`

	// Region is the AWS region the cluster lives in.
	Region string `yaml:"region,omitempty" json:"region,omitempty"`

	// Kubeconfig backend fields:

	// KubeconfigPath is the absolute path to a kubeconfig file.
	KubeconfigPath string `yaml:"kubeconfigPath,omitempty" json:"kubeconfigPath,omitempty"`

	// KubeconfigContext is the name of the context within the kubeconfig
	// to use. Empty means "use the kubeconfig's current-context".
	KubeconfigContext string `yaml:"kubeconfigContext,omitempty" json:"kubeconfigContext,omitempty"`
}

// EKSName returns the AWS-side cluster name parsed from the ARN
// (the segment after ":cluster/"). Used for eks:DescribeCluster calls
// and the x-k8s-aws-id header during EKS token minting.
//
// Returns "" if the ARN is malformed; the registry validates this at
// load time for EKS-backed clusters.
func (c Cluster) EKSName() string {
	const sep = ":cluster/"
	if i := strings.Index(c.ARN, sep); i != -1 {
		return c.ARN[i+len(sep):]
	}
	return ""
}
