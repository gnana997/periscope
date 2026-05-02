package authz

// Tier names — the five built-in roles, named after GitHub repository
// roles (RFC 0002 4 + decisions 9). Operators map IdP groups to one
// of these in auth.yaml: groupTiers.
//
// The actual K8s ClusterRoles bound to each tier ship in the Helm chart
// (deploy/helm/periscope/templates/cluster-rbac.yaml). The chart's
// appVersion tracks the shipped role contents so operators can detect
// drift after upgrades.
const (
	TierRead     = "read"
	TierTriage   = "triage"
	TierWrite    = "write"
	TierMaintain = "maintain"
	TierAdmin    = "admin"
)

// AllTiers is the canonical list, ordered low → high privilege. Useful
// for docs generation, validation, and the audit-log consumer.
var AllTiers = []string{
	TierRead,
	TierTriage,
	TierWrite,
	TierMaintain,
	TierAdmin,
}

// IsValidTier returns true if s is one of the five built-in tiers.
func IsValidTier(s string) bool {
	switch s {
	case TierRead, TierTriage, TierWrite, TierMaintain, TierAdmin:
		return true
	}
	return false
}

// tierRank returns a numeric priority for tier comparison. Higher =
// more privilege. Used when a user maps to multiple tiers (via multiple
// matching groups) and we need to pick one.
func tierRank(t string) int {
	switch t {
	case TierRead:
		return 1
	case TierTriage:
		return 2
	case TierWrite:
		return 3
	case TierMaintain:
		return 4
	case TierAdmin:
		return 5
	}
	return 0
}

// TierK8sBindingName returns the conventional ClusterRoleBinding name
// the Helm chart ships for a tier. Documented for operators and used
// by the chart's NOTES.txt to print install-time hints.
func TierK8sBindingName(tier string) string {
	return "periscope-tier-" + tier
}

// TierImpersonateGroup is the prefixed group string that gets sent on
// the wire when a user resolves to this tier. Always TierGroupPrefix +
// tier, but exposed as a function so callers don't accidentally
// concatenate the wrong prefix.
func TierImpersonateGroup(tier string) string {
	return TierGroupPrefix + tier
}
