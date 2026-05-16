// Package identity surfaces AWS-side cluster access (EKS Access
// Entries, the legacy aws-auth ConfigMap) and ServiceAccount → IAM
// Role bindings (IRSA annotations and EKS Pod Identity associations)
// for the Periscope Identity page (#178).
//
// The package contains:
//
//   - Thin SDK wrappers in client.go (eks + iam reads only).
//   - Pure-logic transformations in awsauth.go and union.go that take
//     SDK outputs and produce the JSON shapes the SPA renders.
//   - A per-cluster SA↔Role index (store.go + manager.go) with TTL
//     refresh and watch-driven invalidation on ServiceAccount events.
//
// The pure-logic functions are deliberately decoupled from the AWS
// SDK so the eventual MCP/agent layer can reuse them against the
// same wire types without re-deriving the diff/union algorithms.
package identity

import "time"

// Source identifies which mechanism binds a ServiceAccount to an IAM
// role. A single SA may have IRSA, PodIdentity, or Both — the Both
// case is a non-obvious config drift since Pod Identity wins at
// runtime and the IRSA annotation is dead config.
type Source string

const (
	SourceIRSA        Source = "IRSA"
	SourcePodIdentity Source = "PodIdentity"
	SourceBoth        Source = "Both"
)

// DiffSide identifies which side of the aws-auth vs Access Entries
// reconciliation a principal lives on.
type DiffSide string

const (
	DiffSideAwsAuthOnly       DiffSide = "aws-auth"
	DiffSideAccessEntriesOnly DiffSide = "access-entries"
	DiffSideBoth              DiffSide = "both"
)

// AccessEntry is the EKS-side access-entry record reconciled against
// aws-auth for the Cluster Access view.
type AccessEntry struct {
	PrincipalArn     string             `json:"principalArn"`
	Type             string             `json:"type,omitempty"`
	KubernetesGroups []string           `json:"kubernetesGroups,omitempty"`
	AccessPolicies   []AccessPolicyAssoc `json:"accessPolicies,omitempty"`
	ModifiedAt       *time.Time         `json:"modifiedAt,omitempty"`
}

// AccessPolicyAssoc is one row of ListAssociatedAccessPolicies output
// for a given access entry.
type AccessPolicyAssoc struct {
	PolicyArn   string   `json:"policyArn"`
	AccessScope string   `json:"accessScope,omitempty"`
	Namespaces  []string `json:"namespaces,omitempty"`
	ModifiedAt  *time.Time `json:"modifiedAt,omitempty"`
}

// AwsAuthEntry is one parsed row from the kube-system/aws-auth
// ConfigMap's mapRoles / mapUsers blob.
type AwsAuthEntry struct {
	PrincipalArn     string   `json:"principalArn"`
	Username         string   `json:"username,omitempty"`
	KubernetesGroups []string `json:"kubernetesGroups,omitempty"`
}

// AwsAuthDiffEntry is one row of the reconciled view: where the
// principal lives (aws-auth, access-entries, or both) and the union
// of K8s groups across sources.
type AwsAuthDiffEntry struct {
	In               DiffSide `json:"in"`
	PrincipalArn     string   `json:"principalArn"`
	KubernetesGroups []string `json:"kubernetesGroups,omitempty"`
}

// AwsAuthDiffHealth is the count summary that powers the migration
// chip at the top of the Identity page. Three buckets sum to the
// distinct principal-ARN count across both sources.
type AwsAuthDiffHealth struct {
	AwsAuthOnly       int `json:"awsAuthOnly"`
	Dual              int `json:"dual"`
	AccessEntriesOnly int `json:"accessEntriesOnly"`
}

// AwsAuthDiffResponse is the wire shape for GET .../aws-auth-diff.
type AwsAuthDiffResponse struct {
	Entries []AwsAuthDiffEntry `json:"entries"`
	Health  AwsAuthDiffHealth  `json:"health"`
}

// SARoleBinding records a single (SA → role) edge with the mechanism
// that produced it and whether the role still exists in IAM.
type SARoleBinding struct {
	Source                   Source `json:"source"`
	RoleArn                  string `json:"roleArn"`
	RoleExists               bool   `json:"roleExists"`
	PodIdentityAssociationId string `json:"podIdentityAssociationId,omitempty"`
	IRSAAnnotationValue      string `json:"irsaAnnotationValue,omitempty"`
}

// SARoleIndexEntry is one row of the unified SA→Role index: a
// ServiceAccount with all its IAM role bindings. DualSource is set
// when a single SA has both an IRSA annotation and a Pod Identity
// association (Pod Identity wins at runtime; the IRSA annotation is
// shadowed dead config worth flagging).
type SARoleIndexEntry struct {
	Cluster    string          `json:"cluster"`
	Namespace  string          `json:"namespace"`
	SAName     string          `json:"saName"`
	Bindings   []SARoleBinding `json:"bindings"`
	DualSource bool            `json:"dualSource"`
}

// PodIdentityAssoc is the EKS-side Pod Identity association record.
// Used both for the raw role-centric view on the Identity page and
// as input to UnifySARoles.
type PodIdentityAssoc struct {
	AssociationId  string `json:"associationId"`
	RoleArn        string `json:"roleArn"`
	Namespace      string `json:"namespace"`
	ServiceAccount string `json:"serviceAccount"`
	ClusterName    string `json:"clusterName,omitempty"`
}

// PodRef is one pod row returned by Manager.PodsForSA — the
// minimal shape the AWS Access surface (#188) renders. NodeName
// is best-effort (empty for pending pods) but useful for SPA
// hover and forensic correlation.
type PodRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	NodeName  string `json:"nodeName,omitempty"`
}
