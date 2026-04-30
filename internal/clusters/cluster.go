// Package clusters owns the dashboard's cluster registry — the passive
// list of EKS clusters Periscope knows about. Per GROUND_RULES, the
// registry never grants access; access is enforced by IAM + aws-auth /
// EKS Access Entries + Kubernetes RBAC.
package clusters

import "strings"

// Cluster identifies an EKS cluster the dashboard can talk to.
type Cluster struct {
	// Name is the human-readable identifier used in URLs and the UI.
	// Must be unique within the registry. May or may not match the
	// AWS-side EKS cluster name (use EKSName for that).
	Name string `yaml:"name" json:"name"`

	// ARN is the full EKS cluster ARN. Required.
	ARN string `yaml:"arn" json:"arn"`

	// Region is the AWS region the cluster lives in.
	Region string `yaml:"region" json:"region"`
}

// EKSName returns the AWS-side cluster name parsed from the ARN
// (the segment after ":cluster/"). Used for eks:DescribeCluster calls
// and the x-k8s-aws-id header during EKS token minting.
//
// Returns "" if the ARN is malformed; the registry validates this at load.
func (c Cluster) EKSName() string {
	const sep = ":cluster/"
	if i := strings.Index(c.ARN, sep); i != -1 {
		return c.ARN[i+len(sep):]
	}
	return ""
}
