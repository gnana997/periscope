// Package clusters owns the dashboard's cluster registry — the passive
// list of clusters Periscope knows about. Per GROUND_RULES, the
// registry never grants access; access is enforced by IAM + aws-auth /
// EKS Access Entries + Kubernetes RBAC for EKS clusters, and by whatever
// the kubeconfig grants for kubeconfig-backed clusters.
package clusters

import "strings"

// Cluster backend identifiers.
//
// All three backends do per-user identity pass-through via K8s
// impersonation: the underlying credentials (AWS IAM token / kubeconfig
// user / in-cluster SA token) are one identity that asserts "do this
// as alice" via the Impersonate-User / Impersonate-Group headers,
// so the apiserver evaluates RBAC under the user, not the dashboard.
//
//   eks         AWS IAM token via the Provider; for EKS clusters managed
//               from a periscope deployed elsewhere.
//   kubeconfig  Identity loaded from a kubeconfig file at a path on the
//               pod's filesystem; useful for local dev (kind, minikube)
//               or non-AWS clusters reached via a static kubeconfig.
//   in-cluster  In-pod ServiceAccount token (rest.InClusterConfig());
//               used when periscope manages the cluster it's running
//               in. The single-cluster install pattern.
const (
	BackendEKS        = "eks"
	BackendKubeconfig = "kubeconfig"
	BackendInCluster  = "in-cluster"
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

	// Environment is an optional, free-form environment label (prod, staging,
	// dev, ...) used for grouping in the fleet view. Empty string is fine
	// and bucketed under "other" by the UI.
	Environment string `yaml:"environment,omitempty" json:"environment,omitempty"`

	// Tags is an optional bag of operator-supplied key/value labels (team,
	// owner, costcenter, ...). Surfaced verbatim by /api/fleet for richer
	// grouping and filtering. Never used for access decisions.
	Tags map[string]string `yaml:"tags,omitempty" json:"tags,omitempty"`

	// Exec carries per-cluster overrides for pod-exec lifecycle and
	// caps. Any field left nil/zero falls back to the global default.
	// Omitted entirely from JSON to avoid leaking config-shape changes
	// into the API; the listClusters handler emits a computed
	// `execEnabled` boolean instead.
	Exec *ExecConfig `yaml:"exec,omitempty" json:"-"`
}

// ExecConfig is the per-cluster override block. Pointer-typed scalars
// distinguish "operator omitted this knob" (use global default) from
// "operator set it to zero" (which would be a nonsensical config and is
// validated against at load time).
type ExecConfig struct {
	// Enabled, if false, hides the Open Shell action and rejects exec
	// requests with HTTP 403 / E_EXEC_DISABLED. Defaults to true (exec
	// is allowed on every registered cluster unless explicitly opted
	// out).
	Enabled *bool `yaml:"enabled,omitempty"`

	// IdleSeconds overrides PERISCOPE_EXEC_IDLE_SECONDS for this
	// cluster. Useful when prod debugging needs a 30-minute timeout
	// while dev clusters keep the 10-minute default.
	IdleSeconds *int `yaml:"serverIdleSeconds,omitempty"`

	// IdleWarnSeconds overrides PERISCOPE_EXEC_IDLE_WARN_SECONDS.
	IdleWarnSeconds *int `yaml:"idleWarnSeconds,omitempty"`

	// HeartbeatSeconds overrides PERISCOPE_EXEC_HEARTBEAT_SECONDS.
	HeartbeatSeconds *int `yaml:"heartbeatSeconds,omitempty"`

	// MaxSessionsPerUser overrides the global per-user concurrent cap.
	MaxSessionsPerUser *int `yaml:"maxSessionsPerUser,omitempty"`

	// MaxSessionsTotal overrides the global per-cluster total cap.
	MaxSessionsTotal *int `yaml:"maxSessionsTotal,omitempty"`
}

// ExecEnabled reports whether pod exec is enabled for this cluster
// after applying the default. Defaults to true when Exec is nil or
// Exec.Enabled is nil — exec ships on by default and operators opt out
// per-cluster.
func (c Cluster) ExecEnabled() bool {
	if c.Exec == nil || c.Exec.Enabled == nil {
		return true
	}
	return *c.Exec.Enabled
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
