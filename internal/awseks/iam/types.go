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

// ReverseLookupMatch is one hit from a reverse lookup: a (Pod, SA,
// Role, Permission) tuple with the matching Permission already
// attributed.
//
// PodRefs is truncated to PodRefsLimit (default 5) so a single SA
// bound to 50 pods doesn't flood the SPA result row. PodCount is
// the untruncated total so the SPA can render "5 of 50".
type ReverseLookupMatch struct {
	SAName     string     `json:"saName"`
	Namespace  string     `json:"namespace"`
	RoleArn    string     `json:"roleArn"`
	Permission Permission `json:"permission"`
	PodRefs    []string   `json:"podRefs"`
	PodCount   int        `json:"podCount"`
}

// ReverseLookupResponse is the wire shape for the reverse-lookup
// endpoint. Echoes the query for SPA convenience (lets the result
// pane show the query without holding it in component state).
type ReverseLookupResponse struct {
	Action   string               `json:"action"`
	Resource string               `json:"resource,omitempty"`
	Scope    ReverseLookupScope   `json:"scope,omitempty"`
	Matches  []ReverseLookupMatch `json:"matches"`
}

// ReverseLookupScope is the optional filter context for a reverse
// lookup.
type ReverseLookupScope struct {
	Cluster   string `json:"cluster,omitempty"`
	Namespace string `json:"namespace,omitempty"`
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
