// Package authz implements Periscope's per-user K8s authorization
// layer: the mode resolver that converts a user's IdP groups into the
// Kubernetes impersonation strings sent on every K8s API call.
//
// Three modes (RFC 0002 4):
//
//	shared  No impersonation. Every user shares the pod role's K8s
//	        permissions. Default and lowest-friction; matches the
//	        pre-PR-B status quo.
//
//	tier    Map IdP groups to one of five built-in tiers
//	        (read/triage/write/maintain/admin). Periscope impersonates
//	        with a single prefixed group like "periscope-tier:admin".
//	        The Helm chart ships per-cluster RBAC bindings for the
//	        tier groups.
//
//	raw     Impersonate with the user's actual IdP groups, prefixed.
//	        Operator owns all per-cluster RBAC. Maximum flexibility;
//	        maximum operator effort.
//
// Group prefixing (RFC 0002 7.5) is non-negotiable: an attacker who
// compromises Periscope must not be able to impersonate into
// system:masters or any other un-prefixed group. The prefix is
// hardcoded for tier mode (periscope-tier:) and configurable for raw
// mode (default periscope:).
package authz

import (
	"fmt"
	"strings"
)

// Mode selects the authorization strategy.
type Mode string

const (
	ModeShared Mode = "shared"
	ModeTier   Mode = "tier"
	ModeRaw    Mode = "raw"
)

// Default mode applied when the operator hasn't set one.
const DefaultMode = ModeShared

// TierGroupPrefix is the hardcoded prefix for tier-mode impersonation.
// Hardcoded (not configurable) so the chart's shipped RBAC bindings
// always match.
const TierGroupPrefix = "periscope-tier:"

// DefaultRawGroupPrefix is the configurable prefix used in raw mode.
const DefaultRawGroupPrefix = "periscope:"

// Config is the authorization block from auth.yaml. Mirrors the YAML
// shape; the auth package owns YAML loading, this package owns the
// resolution logic.
type Config struct {
	Mode          Mode              `yaml:"mode"`
	GroupTiers    map[string]string `yaml:"groupTiers"`
	DefaultTier   string            `yaml:"defaultTier"`
	GroupPrefix   string            `yaml:"groupPrefix"`
	GroupsClaim   string            `yaml:"groupsClaim"`
	AllowedGroups []string          `yaml:"allowedGroups"`
	// AuditAdminGroups grants full /api/audit visibility to users in any
	// of the listed IdP groups. Independent of authz mode — works in
	// shared, tier, and raw. When empty, audit-admin falls back to
	// mode-specific defaults (see Resolver.IsAuditAdmin).
	AuditAdminGroups []string       `yaml:"auditAdminGroups"`
}

// Identity is the slice of session state authz needs. Avoids importing
// the auth or credentials packages here — caller maps Session into
// Identity.
type Identity struct {
	Subject string
	Groups  []string
}

// ImpersonationConfig is what the K8s client puts on rest.Config.
// Empty (UserName == "") means "do not impersonate" — the shared-mode
// signal.
type ImpersonationConfig struct {
	UserName string
	Groups   []string
}

// IsZero reports whether the config is the empty / shared-mode value.
func (c ImpersonationConfig) IsZero() bool {
	return c.UserName == "" && len(c.Groups) == 0
}

// Resolver applies a Config to an Identity to produce ImpersonationConfig.
// Stateless and cheap; safe to share.
type Resolver struct {
	cfg Config
}

// NewResolver normalizes the config (defaults applied, mode validated)
// and returns a ready resolver. Errors only on a bad mode value.
func NewResolver(cfg Config) (*Resolver, error) {
	if cfg.Mode == "" {
		cfg.Mode = DefaultMode
	}
	switch cfg.Mode {
	case ModeShared, ModeTier, ModeRaw:
	default:
		return nil, fmt.Errorf("authz: unknown mode %q (want shared|tier|raw)", cfg.Mode)
	}
	if cfg.GroupPrefix == "" {
		cfg.GroupPrefix = DefaultRawGroupPrefix
	}
	if cfg.Mode == ModeTier {
		if cfg.DefaultTier != "" && !IsValidTier(cfg.DefaultTier) {
			return nil, fmt.Errorf("authz: defaultTier %q is not a valid tier", cfg.DefaultTier)
		}
		for grp, tier := range cfg.GroupTiers {
			if !IsValidTier(tier) {
				return nil, fmt.Errorf("authz: groupTiers[%q] = %q is not a valid tier", grp, tier)
			}
		}
	}
	return &Resolver{cfg: cfg}, nil
}

// Mode returns the configured mode (post-default).
func (r *Resolver) Mode() Mode { return r.cfg.Mode }

// Resolve returns the impersonation config for the user. Returns the
// zero value (== shared mode signal) when:
//   - mode is shared
//   - identity has no Subject (e.g. anonymous request — caller should
//     have rejected this earlier, but we fail safe)
func (r *Resolver) Resolve(id Identity) ImpersonationConfig {
	if r.cfg.Mode == ModeShared || id.Subject == "" {
		return ImpersonationConfig{}
	}

	switch r.cfg.Mode {
	case ModeTier:
		tier := r.tierForGroups(id.Groups)
		if tier == "" {
			// Operator chose `defaultTier: ""` → deny (no impersonation
			// groups → RBAC will reject every action). Caller layer
			// should ideally pre-reject at login; this is fail-safe.
			return ImpersonationConfig{UserName: id.Subject}
		}
		return ImpersonationConfig{
			UserName: id.Subject,
			Groups:   []string{TierGroupPrefix + tier},
		}

	case ModeRaw:
		return ImpersonationConfig{
			UserName: id.Subject,
			Groups:   prefixAll(r.cfg.GroupPrefix, id.Groups),
		}
	}

	// Unreachable; constructor validates mode.
	return ImpersonationConfig{}
}

// ResolvedTier returns the tier name for an identity in tier mode, or
// "" otherwise. Used by /api/auth/whoami so the SPA can show a tier
// badge in <UserMenu>.
func (r *Resolver) ResolvedTier(id Identity) string {
	if r.cfg.Mode != ModeTier {
		return ""
	}
	return r.tierForGroups(id.Groups)
}

// AllowedTier reports whether the user's groups resolve to any tier or
// to the configured defaultTier. Used by the auth gate: in tier mode
// with `defaultTier: ""`, a user in no listed group is rejected.
func (r *Resolver) AllowedTier(id Identity) bool {
	if r.cfg.Mode != ModeTier {
		return true // shared and raw delegate to allowedGroups + RBAC
	}
	return r.tierForGroups(id.Groups) != ""
}

// tierForGroups resolves a user's groups to a tier, falling back to
// defaultTier (empty string if defaultTier is itself empty).
func (r *Resolver) tierForGroups(groups []string) string {
	for _, g := range groups {
		if t, ok := r.cfg.GroupTiers[g]; ok {
			// If multiple groups map to different tiers, the highest-
			// privilege tier wins. Ordering: admin > maintain > write
			// > triage > read. This is the Rancher / GitHub convention.
			best := t
			for _, g2 := range groups {
				if t2, ok := r.cfg.GroupTiers[g2]; ok {
					if tierRank(t2) > tierRank(best) {
						best = t2
					}
				}
			}
			return best
		}
	}
	return r.cfg.DefaultTier
}

func prefixAll(prefix string, in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		// Skip empty strings to avoid emitting bare "periscope:" groups.
		if strings.TrimSpace(s) == "" {
			continue
		}
		out = append(out, prefix+s)
	}
	return out
}

// IsAuditAdmin reports whether the user can read every actor's rows
// from /api/audit. Resolution order (audit-admin is intentionally
// decoupled from K8s admin so security teams can read history without
// holding cluster-mutating tiers):
//
//  1. AuditAdminGroups non-empty → true iff any of id.Groups is listed.
//     This is the explicit operator switch and wins regardless of mode.
//
//  2. Otherwise, mode-specific fallback:
//     - tier  → true iff resolved tier == "admin".
//     - shared → true iff AllowedGroups is non-empty AND id is in it
//                (the dashboard's existing allowlist gate also gates
//                 audit-admin in shared mode).
//     - raw   → false. Raw mode pushes RBAC to K8s; the dashboard has
//                 no notion of "admin" to consult, so audit-admin must
//                 be granted explicitly via AuditAdminGroups.
//
//  3. Otherwise → false (self-only audit access).
//
// "Self-only" here means the audit_handler hard-overrides the actor
// filter to id.Subject; the user can self-audit but never see what
// colleagues did.
func (r *Resolver) IsAuditAdmin(id Identity) bool {
	// 1. Explicit override wins regardless of mode.
	if len(r.cfg.AuditAdminGroups) > 0 {
		return anyGroupIn(id.Groups, r.cfg.AuditAdminGroups)
	}

	// 2. Mode-specific fallback.
	switch r.cfg.Mode {
	case ModeTier:
		return r.tierForGroups(id.Groups) == TierAdmin
	case ModeShared:
		// Default empty AllowedGroups means "any authenticated user can
		// use the dashboard" — too broad to silently grant audit-admin.
		// Operators who want shared-mode admin access must either
		// populate AllowedGroups (treating it as the admin set) or set
		// AuditAdminGroups explicitly.
		if len(r.cfg.AllowedGroups) == 0 {
			return false
		}
		return anyGroupIn(id.Groups, r.cfg.AllowedGroups)
	case ModeRaw:
		// Raw mode delegates RBAC entirely to K8s; without an explicit
		// AuditAdminGroups list the dashboard has no opinion on who is
		// an audit-admin.
		return false
	}
	return false
}

func anyGroupIn(have, allowed []string) bool {
	if len(have) == 0 || len(allowed) == 0 {
		return false
	}
	allowSet := make(map[string]struct{}, len(allowed))
	for _, g := range allowed {
		allowSet[g] = struct{}{}
	}
	for _, g := range have {
		if _, ok := allowSet[g]; ok {
			return true
		}
	}
	return false
}
