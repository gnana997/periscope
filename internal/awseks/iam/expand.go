package iam

import (
	"fmt"
	"strings"
)

// StatementMeta is the call-site context passed into Statement.Expand.
// Stamped onto every Permission row produced — drives audit
// attribution and the SPA's "open in IAM console" deep-links.
//
// Catalog is optional; nil means use DefaultCatalog (the
// process-wide catalog loaded from sensitive.yaml). Tests pass an
// alternative catalog to exercise edge cases without rewriting the
// embedded YAML.
type StatementMeta struct {
	PolicyArn    string
	PolicyName   string
	PolicySource PolicySource
	StatementIdx int
	Catalog      *Catalog
}

// Expand projects a parsed Statement into the canonical wire shape.
//
// Two output shapes, mutually exclusive:
//
//   - If the statement uses NotAction, NotResource, or NotPrincipal,
//     it's surfaced as a single *RawStatement — v1.1 doesn't try to
//     project the inverse-match semantics into Permission rows
//     because the resulting set is unbounded and the SPA can't render
//     "every action except these" cleanly. The SPA shows a "complex
//     statement — see in IAM console" chip pointing at the source.
//
//   - Otherwise the cartesian product of Action[] × Resource[] is
//     emitted as []Permission. Each row carries pre-computed render
//     hints (Service, Sensitive, SensitiveReason, Wildcard,
//     HasCondition) so the SPA doesn't re-derive on every render.
//
// Defensive behavior for malformed input:
//
//   - Empty Action and empty NotAction → returns (nil, nil). The
//     statement grants nothing; not worth a RawStatement.
//   - Empty Resource and empty NotResource → treated as Resource:
//     ["*"]. Some legal statements (cross-cutting service-control
//     style) elide Resource; without this fallback the cartesian
//     yields zero rows even when the statement obviously grants
//     something.
//
// The returned (perms, raw) tuple has at most one non-nil side.
func (s Statement) Expand(meta StatementMeta) ([]Permission, *RawStatement) {
	if len(s.NotAction) > 0 || len(s.NotResource) > 0 || len(s.NotPrincipal) > 0 {
		return nil, buildRawStatement(s, meta)
	}

	actions := s.Action
	if len(actions) == 0 {
		// Empty Action + no NotAction → nothing to project.
		return nil, nil
	}
	resources := s.Resource
	if len(resources) == 0 {
		// Defensive: a statement with Action but no Resource is
		// rendered as if Resource: ["*"]. Service-only AWS actions
		// (e.g. sts:GetCallerIdentity) sometimes omit Resource;
		// treating it as "*" preserves the matcher's behavior
		// without surprising the operator.
		resources = []string{"*"}
	}

	catalog := meta.Catalog
	if catalog == nil {
		catalog = DefaultCatalog()
	}

	hasCondition := len(s.Condition) > 0

	perms := make([]Permission, 0, len(actions)*len(resources))
	for _, action := range actions {
		service := serviceFromAction(action)
		actionWildcard := hasWildcardChars(action)
		category, sensitive := catalog.Classify(action)
		for _, resource := range resources {
			resourceWildcard := hasWildcardChars(resource)
			perms = append(perms, Permission{
				Action:          action,
				Service:         service,
				Resource:        resource,
				Effect:          s.Effect,
				PolicyArn:       meta.PolicyArn,
				PolicyName:      meta.PolicyName,
				PolicySource:    meta.PolicySource,
				StatementSid:    s.Sid,
				StatementIdx:    meta.StatementIdx,
				Sensitive:       sensitive,
				SensitiveReason: category,
				HasCondition:    hasCondition,
				Wildcard:        actionWildcard || resourceWildcard,
			})
		}
	}
	return perms, nil
}

// buildRawStatement constructs the RawStatement surface for
// NotAction / NotResource / NotPrincipal statements. ConsoleURL is
// deliberately left empty — the engine fills it in later (it knows
// the partition + role context that this layer doesn't).
func buildRawStatement(s Statement, meta StatementMeta) *RawStatement {
	rs := &RawStatement{
		PolicyArn:    meta.PolicyArn,
		PolicyName:   meta.PolicyName,
		PolicySource: meta.PolicySource,
		StatementIdx: meta.StatementIdx,
		StatementSid: s.Sid,
	}
	effect := strings.ToLower(string(s.Effect))
	switch {
	case len(s.NotAction) > 0:
		rs.Reason = "NotAction"
		rs.Summary = fmt.Sprintf("%s every action except %s",
			effect, summarizeList(s.NotAction))
	case len(s.NotResource) > 0:
		rs.Reason = "NotResource"
		rs.Summary = fmt.Sprintf("%s %s on every resource except %s",
			effect, summarizeList(s.Action), summarizeList(s.NotResource))
	case len(s.NotPrincipal) > 0:
		rs.Reason = "NotPrincipal"
		rs.Summary = fmt.Sprintf("%s for principals not matching the NotPrincipal block", effect)
	}
	return rs
}

// summarizeList renders a list for a RawStatement.Summary chip.
// Truncates at 3 items with a "(and N more)" suffix so the summary
// stays one-line on the SPA chip.
func summarizeList(items []string) string {
	switch {
	case len(items) == 0:
		return "(none)"
	case len(items) <= 3:
		return strings.Join(items, ", ")
	default:
		return fmt.Sprintf("%s (and %d more)", strings.Join(items[:3], ", "), len(items)-3)
	}
}

// serviceFromAction extracts the lower-cased service prefix from
// an IAM action. Returns "*" for the literal wildcard, "" for an
// action without a colon prefix (rare; defensive).
//
// Examples:
//
//	"s3:GetObject"          → "s3"
//	"IAM:PassRole"          → "iam"   (lower-cased)
//	"s3-object-lambda:Get*" → "s3-object-lambda"
//	"*"                     → "*"
//	"NoColonAction"         → ""
func serviceFromAction(action string) string {
	if action == "*" {
		return "*"
	}
	if i := strings.Index(action, ":"); i > 0 {
		return strings.ToLower(action[:i])
	}
	return ""
}

// hasWildcardChars reports whether s contains any IAM glob
// metacharacter ("*" or "?").
func hasWildcardChars(s string) bool {
	return strings.ContainsAny(s, "*?")
}
