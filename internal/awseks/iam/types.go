// Package iam holds the IAM policy resolution engine (#187): fetches
// a role's identity-based policies, parses them, and answers
// (action, resource) match queries. Consumed by two SPA surfaces:
// the per-Pod AWS Access tab (forward view) and the per-cluster
// reverse lookup form (both in #188).
//
// This file is the Day-1 wire-shape lock. The Go types here mirror
// 1:1 with the TypeScript interfaces in web/src/lib/identity.ts so
// the SPA can scaffold against stubs while the engine itself
// (parser, matcher, cache) is built in parallel.
//
// Scope (v1.1):
//   - Identity-based policies only — inline + attached managed.
//   - No SCPs, no permission boundaries, no resource-based policies,
//     no session policies, no Condition evaluation.
//   - NotAction / NotResource statements are NOT projected to
//     Permission rows; they surface as RawStatement entries with an
//     "open in IAM console" deep link.
//
// Versioned: SensitivePermissionsCatalogVersion in sensitive.go.
package iam

import (
	"context"
	"encoding/json"
	"time"
)

// ── Enums ─────────────────────────────────────────────────────────

// Effect mirrors IAM's policy Effect.
type Effect string

const (
	EffectAllow Effect = "Allow"
	EffectDeny  Effect = "Deny"
)

// PolicySource attributes a Permission to either an inline policy
// attached to the role, or a managed policy referenced by ARN.
type PolicySource string

const (
	PolicySourceInline  PolicySource = "inline"
	PolicySourceManaged PolicySource = "managed"
)

// SensitiveCategory groups the sensitive-permissions catalog into
// render bins. The SPA renders one chip colour / icon per category.
// Catalog content lives in sensitive.yaml; loader is in sensitive.go.
type SensitiveCategory string

const (
	SensitivePrivEsc       SensitiveCategory = "privilege-escalation"
	SensitiveData          SensitiveCategory = "data"
	SensitiveCrossAccount  SensitiveCategory = "cross-account"
	SensitiveDestructive   SensitiveCategory = "destructive"
	SensitiveCluster       SensitiveCategory = "cluster"
	SensitiveWildcard      SensitiveCategory = "wildcard"
)

// ── Wire types: shipped to the SPA ───────────────────────────────

// Permission is the projected, render-ready, matcher-ready view of
// one (statement × action × resource) tuple from a parsed IAM
// policy. One Statement expands to many Permissions (cartesian over
// the statement's Action[] and Resource[] arrays — wildcards stay
// as glob strings, NOT pre-expanded).
type Permission struct {
	// Identity — the matcher core.
	Action   string `json:"action"`   // e.g. "s3:GetObject", "s3:*", "*"
	Service  string `json:"service"`  // always lower-cased ("s3", "iam", "kms")
	Resource string `json:"resource"` // ARN or wildcard; "*" if absent
	Effect   Effect `json:"effect"`

	// Source attribution — for audit + "open in IAM console" deep links.
	PolicyArn    string       `json:"policyArn,omitempty"`     // empty for inline policies
	PolicyName   string       `json:"policyName"`              // managed policy name or inline name
	PolicySource PolicySource `json:"policySource"`            // "inline" | "managed"
	StatementSid string       `json:"statementSid,omitempty"`  // optional Sid from source
	StatementIdx int          `json:"statementIdx"`            // index into parent Statement[]

	// Pre-computed render hints — server side avoids bundling the
	// sensitive catalog in the SPA. Sensitivity reason renders the
	// chip colour; Wildcard powers an SPA filter and a matcher
	// fast-path.
	Sensitive       bool              `json:"sensitive"`
	SensitiveReason SensitiveCategory `json:"sensitiveReason,omitempty"`
	HasCondition    bool              `json:"hasCondition"` // presence-only flag (no eval in v1.1)
	Wildcard        bool              `json:"wildcard"`     // Action or Resource has "*" or "?"
}

// RawStatement is the wire-side surface for statements we can't
// safely project to []Permission — NotAction, NotResource, or
// NotPrincipal. One per problematic statement; SPA renders a
// "complex statement — see in IAM console" chip pointing at
// ConsoleURL. ConsoleURL is omitted for non-aws partitions where
// the URL format isn't supported in v1.1.
type RawStatement struct {
	PolicyArn    string       `json:"policyArn,omitempty"`
	PolicyName   string       `json:"policyName"`
	PolicySource PolicySource `json:"policySource"`
	StatementIdx int          `json:"statementIdx"`
	StatementSid string       `json:"statementSid,omitempty"`
	Reason       string       `json:"reason"`  // "NotAction" | "NotResource" | "NotPrincipal"
	Summary      string       `json:"summary"` // short human-readable text the chip shows
	ConsoleURL   string       `json:"consoleUrl,omitempty"`
}

// RolePermissionsResult is the forward-view response — per-Pod /
// per-SA / per-Deployment AWS Access tab in the SPA.
//
// Truncated + TotalCount are the soft-cap signal: when a single
// role's policies expand past the configured MaxRowsCap, the SPA
// renders "showing N of M — filter to narrow" rather than freezing
// the tab. Cap is set in Config.MaxRowsCap (default 10000).
//
// PolicyFetchPartial mirrors the snapshot-with-error pattern from
// identity.Manager: if any GetRolePolicy / GetPolicyVersion failed,
// the engine returns whatever it has plus the flag so the SPA shows
// a banner without blanking the page.
//
// CatalogVersion lets operators trace "why is this flagged?" to a
// specific version of the sensitive-perms catalog. Bump when the
// catalog changes.
type RolePermissionsResult struct {
	RoleArn            string         `json:"roleArn"`
	Permissions        []Permission   `json:"permissions"`
	RawStatements      []RawStatement `json:"rawStatements"`
	FetchedAt          time.Time      `json:"fetchedAt"`
	PolicyFetchPartial bool           `json:"policyFetchPartial"`
	CatalogVersion     string         `json:"catalogVersion"`
	Truncated          bool           `json:"truncated"`
	TotalCount         int            `json:"totalCount"` // matches len(Permissions) if !Truncated
}

// ReverseLookupQuery is the request shape for "which pods can do X?".
// Action is exact (not a pattern from the caller); Resource is
// optional (empty = match any). Namespace optionally scopes the
// SA→role iteration to a single namespace.
type ReverseLookupQuery struct {
	Action    string `json:"action"`
	Resource  string `json:"resource,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// ReverseLookupMatch is the engine-internal hit shape: one (SA,
// role, permission) tuple before pod enrichment. The cmd/periscope
// handler joins each match against the cluster's pod informer
// cache to flatten into ReverseLookupPodRow.
//
// Distinct from ReverseLookupPodRow (the wire shape) so the engine
// stays in-package — no k8s informer types leak across the iam
// package boundary.
type ReverseLookupMatch struct {
	SAName     string
	Namespace  string
	RoleArn    string
	Permission Permission
}

// ReverseLookupPodRow is the wire shape — one row per matched pod.
// Flattened from ReverseLookupMatch by the cmd/periscope handler
// after pod-cache enrichment. Source attributes the binding
// (IRSA / PodIdentity); dual-source SAs emit two rows per pod (one
// per binding) so the SPA renders the honest dual-source story.
type ReverseLookupPodRow struct {
	Pod        PodRef     `json:"pod"`
	SAName     string     `json:"saName"`
	Namespace  string     `json:"namespace"`
	RoleArn    string     `json:"roleArn"`
	Permission Permission `json:"permission"`
	Source     string     `json:"source"` // "IRSA" | "PodIdentity" | "Both"
}

// PodRef is the wire representation of a pod reference. Same
// fields as identity.PodRef; duplicated here to avoid a reverse
// import edge from iam → identity.
type PodRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	NodeName  string `json:"nodeName,omitempty"`
}

// ReverseLookupResponse is the wire shape for the reverse-lookup
// endpoint. Echoes the query for SPA convenience and carries the
// row-per-pod result list plus a server-computed truncation flag.
//
// Wire-shape change from v1.0: the previous `matches` field is
// now `rows`, carrying one entry per matched pod (was: one entry
// per SA with embedded podRefs). See CHANGELOG `Changed`.
type ReverseLookupResponse struct {
	Action    string                `json:"action"`
	Resource  string                `json:"resource,omitempty"`
	Scope     ReverseLookupScope    `json:"scope,omitempty"`
	Rows      []ReverseLookupPodRow `json:"rows"`
	Truncated bool                  `json:"truncated"`
	TotalPods int                   `json:"totalPods"`
}

// ReverseLookupScope is the optional filter context for a reverse
// lookup.
type ReverseLookupScope struct {
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

// ── Composed forward-view wire types (#188) ──────────────────────

// ServiceGroup buckets Permissions by the AWS service segment of
// the action ("s3", "iam", "kms", "*"). Server-grouped so the SPA
// renders accordions directly without re-bucketing. Sort key on
// the outer slice: sensitive-first, then service alpha.
type ServiceGroup struct {
	Service     string       `json:"service"`     // lower-cased; "*" for wildcard-action statements
	Sensitive   bool         `json:"sensitive"`   // any perm in the group is sensitive
	Count       int          `json:"count"`       // len(Permissions)
	Permissions []Permission `json:"permissions"` // already sorted by sortPermissions
}

// IdentityChainBinding is the wire-shape view of one SA→Role
// binding edge for the composed forward-view response. Same
// fields as identity.SARoleBinding; duplicated to avoid a reverse
// import from iam → identity.
type IdentityChainBinding struct {
	Source                   string `json:"source"` // "IRSA" | "PodIdentity" | "Both"
	RoleArn                  string `json:"roleArn"`
	RoleExists               bool   `json:"roleExists"`
	PodIdentityAssociationId string `json:"podIdentityAssociationId,omitempty"`
	IRSAAnnotationValue      string `json:"irsaAnnotationValue,omitempty"`
}

// IdentityChain is the resolved identity attribution for a
// workload: which SA it runs as plus every IAM role bound to that
// SA. DualSource is true when both IRSA and Pod Identity bindings
// exist on the same SA (the IRSA one is shadowed dead-config at
// runtime).
type IdentityChain struct {
	ServiceAccount string                 `json:"serviceAccount"`
	Bindings       []IdentityChainBinding `json:"bindings"`
	DualSource     bool                   `json:"dualSource"`
}

// AwsAccessWarning is one server-emitted advisory the SPA renders
// as a chip. Codes are stable identifiers an MCP tool can branch
// on; Message is human-readable.
type AwsAccessWarning struct {
	Code    string `json:"code"` // DUAL_SOURCE_IRSA_SHADOWED | ROLE_NOT_FOUND | POLICY_FETCH_PARTIAL | NO_BINDINGS
	Message string `json:"message"`
	RoleArn string `json:"roleArn,omitempty"`
}

// Warning codes — stable string keys MCP tools can branch on.
const (
	WarningDualSourceIRSAShadowed = "DUAL_SOURCE_IRSA_SHADOWED"
	WarningRoleNotFound           = "ROLE_NOT_FOUND"
	WarningPolicyFetchPartial     = "POLICY_FETCH_PARTIAL"
	WarningNoBindings             = "NO_BINDINGS"
)

// WorkloadPermissionsResponse is the composed forward-view
// response — one round-trip from the SPA returns the full AWS
// Access tab. Per backend-as-source-of-truth, every join,
// grouping, and classification is computed server-side so MCP
// tools can wrap the endpoint as a single tool call.
//
// AffectedPods is the list of running pods this composition
// applies to: for kind=Pod, just the pod itself; for controllers,
// the pods using the resolved SA; for kind=ServiceAccount, all
// pods bound to that SA. Truncated to PodRefsLimit;
// AffectedPodCount is the untruncated total.
type WorkloadPermissionsResponse struct {
	Cluster   string `json:"cluster"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`

	IdentityChain IdentityChain      `json:"identityChain"`
	Groups        []ServiceGroup     `json:"groups"`
	RawStatements []RawStatement     `json:"rawStatements"`
	Warnings      []AwsAccessWarning `json:"warnings"`

	AffectedPods     []PodRef `json:"affectedPods"`
	AffectedPodCount int      `json:"affectedPodCount"`

	PolicyFetchPartial bool `json:"policyFetchPartial"`
	Truncated          bool `json:"truncated"`
	TotalCount         int  `json:"totalCount"`

	CatalogVersion string    `json:"catalogVersion"`
	FetchedAt      time.Time `json:"fetchedAt"`
}

// ── Sensitive-catalog wire types (#188) ──────────────────────────

// SensitiveCatalogEntry is one row of the cluster-agnostic catalog
// endpoint. ReverseQuery is the pre-canned reverse-lookup query
// the SPA fires when a chip is clicked — keeps the chip→action
// mapping server-side so an MCP tool can resolve "what does this
// chip mean?" without a TS lookup table.
type SensitiveCatalogEntry struct {
	Action       string            `json:"action"`
	Category     SensitiveCategory `json:"category"`
	Pattern      bool              `json:"pattern"`
	ReverseQuery ReverseQueryHint  `json:"reverseQuery"`
}

// ReverseQueryHint is the (action, resource) pair the SPA
// pre-fills into the reverse-lookup form when a chip is clicked.
// Resource is optional and almost always empty (the chip's action
// is the whole signal); reserved for future per-category resource
// defaults.
type ReverseQueryHint struct {
	Action   string `json:"action"`
	Resource string `json:"resource,omitempty"`
}

// SensitiveCatalogResponse is the wire shape for
// GET /api/identity/sensitive-catalog. Cluster-agnostic.
type SensitiveCatalogResponse struct {
	Version string                  `json:"version"`
	Entries []SensitiveCatalogEntry `json:"entries"`
}

// ── Capabilities wire types (#188) ───────────────────────────────

// Capability feature keys — stable identifiers the SPA and MCP
// tools index into CapabilitiesResponse.Features.
const (
	FeatureAwsAccessTab     = "awsAccessTab"
	FeatureReverseLookup    = "reverseLookup"
	FeatureSensitiveCatalog = "sensitiveCatalog"
)

// Capability lock reasons — stable string keys. MCP tools branch
// on these instead of parsing the human-readable Message.
const (
	ReasonNotEKS              = "NOT_EKS"
	ReasonRBACDenied          = "RBAC_DENIED"
	ReasonMissingIAMPerms     = "MISSING_IAM_PERMS"
	ReasonNoIdentityConfigured = "NO_IDENTITY_CONFIGURED"
	ReasonInformerWarming     = "INFORMER_WARMING"
	ReasonIAMProbeDisabled    = "IAM_PROBE_DISABLED"
)

// FeatureCapability is one feature's availability + lock reason.
// Available=true means the SPA may render the feature; otherwise
// the locked-pane shows Message + Missing + DocsURL.
//
// Note is set when the feature is Available but the probe
// couldn't definitively prove it (e.g. iam:SimulatePrincipalPolicy
// itself is denied). UI surfaces the note inline so operators
// understand they're in optimistic mode.
type FeatureCapability struct {
	Available  bool     `json:"available"`
	Reason     string   `json:"reason,omitempty"`
	Message    string   `json:"message,omitempty"`
	Missing    []string `json:"missing,omitempty"`
	DocsURL    string   `json:"docsUrl,omitempty"`
	ConsoleURL string   `json:"consoleUrl,omitempty"`
	Note       string   `json:"note,omitempty"`
}

// CapabilitiesResponse is the wire shape for the per-cluster
// capabilities probe. Features keys are stable identifiers:
// "awsAccessTab", "reverseLookup", "sensitiveCatalog".
type CapabilitiesResponse struct {
	Cluster   string                       `json:"cluster"`
	Features  map[string]FeatureCapability `json:"features"`
	FetchedAt time.Time                    `json:"fetchedAt"`
	Note      string                       `json:"note,omitempty"`
}

// ── Internal types: NOT shipped to the SPA ───────────────────────

// Statement is the parser-output shape, 1:1 with one IAM policy
// document statement. Internal — projected to []Permission via
// Expand() (or to one RawStatement if it uses Not* variants).
//
// AWS JSON quirks the parser handles:
//   - Action, Resource may be string-or-array
//   - Statement (at the document level) may be object-or-array
//   - Effect is case-sensitive per AWS but we normalize defensively
//   - Condition is parsed as RawMessage; not evaluated in v1.1
type Statement struct {
	Sid          string
	Effect       Effect
	Action       []string
	NotAction    []string
	Resource     []string
	NotResource  []string
	Principal    map[string][]string // resource-based policies only — ignored in v1.1
	NotPrincipal map[string][]string // ditto
	Condition    json.RawMessage     // presence-only; not parsed deeply in v1.1
}

// PolicyDocument is the parsed top-level IAM policy JSON document.
// The parser deserializes raw policy JSON into this shape, then
// Statement.Expand() projects to []Permission / *RawStatement.
type PolicyDocument struct {
	Version   string      `json:"Version,omitempty"`
	Id        string      `json:"Id,omitempty"`
	Statement []Statement `json:"Statement"`
}

// AttachedPolicy is the SDK-seam value type for the output of
// ListAttachedRolePolicies. Mirrors aws-sdk-go-v2's
// iamtypes.AttachedPolicy but kept narrow so the iam package
// doesn't import the SDK types directly.
type AttachedPolicy struct {
	PolicyArn  string
	PolicyName string
}

// ── Engine seams ─────────────────────────────────────────────────

// PolicyFetcher is the SDK seam. Satisfied by *identity.Client
// after the policy-fetch methods land on it (see #178's client.go
// extension scoped in the #187 plan). Stubbable for tests so the
// engine can be exercised without AWS.
type PolicyFetcher interface {
	ListRolePolicies(ctx context.Context, roleArn string) ([]string, error)
	GetRolePolicy(ctx context.Context, roleArn, policyName string) (json.RawMessage, error)
	ListAttachedRolePolicies(ctx context.Context, roleArn string) ([]AttachedPolicy, error)
	GetPolicyDocument(ctx context.Context, policyArn string) (json.RawMessage, error)
}

// SARoleIndexer is the seam to #178's identity.Manager. The reverse
// lookup walks SA→Role bindings and asks the engine for matching
// Permissions per role. SARoleSnapshot returns a flattened view of
// the manager's SARoleIndexEntry list.
type SARoleIndexer interface {
	SARoleSnapshot(ctx context.Context, cluster string) ([]SARoleBinding, error)
}

// SARoleBinding is a flattened (namespace, sa, role) tuple — the
// minimal shape the reverse-lookup matcher needs. Distinct from
// identity.SARoleBinding (which carries source + roleExists +
// association IDs) because the matcher doesn't care about those.
type SARoleBinding struct {
	SAName    string
	Namespace string
	RoleArn   string
}

// ── Config + defaults ────────────────────────────────────────────

// Config bundles per-engine tuning. Zero values fall back to
// DefaultPolicyTTL / DefaultMaxRowsCap / DefaultPodRefsLimit so
// cmd/periscope can supply just the fields it wants to override.
type Config struct {
	PolicyTTL    time.Duration
	MaxRowsCap   int
	PodRefsLimit int
}

const (
	// DefaultPolicyTTL is the per-role policy-cache lifetime. IAM
	// policy versions are immutable (a change creates a new
	// VersionId), so 30 min is a safe freshness window. Manual
	// refresh (via the engine's Invalidate methods) bypasses the
	// cache when operators need fresher data mid-investigation.
	DefaultPolicyTTL = 30 * time.Minute

	// DefaultMaxRowsCap is the soft cap on a single role's expanded
	// Permission row count. Beyond this, RolePermissionsResult is
	// returned with Truncated=true and TotalCount set; the SPA
	// renders a "showing N of M" banner. 10k chosen because typical
	// roles render <500 rows; pathological policies start to freeze
	// the browser around 50k.
	DefaultMaxRowsCap = 10000

	// DefaultPodRefsLimit caps the number of pod names returned in
	// each ReverseLookupMatch. The full count is preserved in
	// PodCount so the SPA can render "5 of 50".
	DefaultPodRefsLimit = 5
)

// WithDefaults returns a copy of cfg with zero fields populated
// from the package defaults.
func (c Config) WithDefaults() Config {
	if c.PolicyTTL == 0 {
		c.PolicyTTL = DefaultPolicyTTL
	}
	if c.MaxRowsCap == 0 {
		c.MaxRowsCap = DefaultMaxRowsCap
	}
	if c.PodRefsLimit == 0 {
		c.PodRefsLimit = DefaultPodRefsLimit
	}
	return c
}
