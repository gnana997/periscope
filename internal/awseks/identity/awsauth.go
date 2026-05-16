package identity

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

// AwsAuthConfigMapNamespace and AwsAuthConfigMapName name the
// ConfigMap that holds the legacy mapRoles / mapUsers blob. Public
// so handlers can use the same constants when fetching it.
const (
	AwsAuthConfigMapNamespace = "kube-system"
	AwsAuthConfigMapName      = "aws-auth"
)

// awsAuthRow is the on-disk YAML shape inside a single entry of
// mapRoles / mapUsers. Both shapes share the username + groups
// fields and differ only in the ARN field name (rolearn vs userarn).
type awsAuthRow struct {
	RoleArn  string   `json:"rolearn,omitempty"`
	UserArn  string   `json:"userarn,omitempty"`
	Username string   `json:"username,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// ParseAwsAuth parses the kube-system/aws-auth ConfigMap's mapRoles
// and mapUsers blobs. A nil ConfigMap, an empty ConfigMap, and a
// ConfigMap whose blobs parse to nothing all return ([], nil) — an
// absent or post-migration cluster is not an error condition; it's
// the migration-complete signal the SPA renders.
//
// Returns a typed error only on malformed YAML.
func ParseAwsAuth(cm *corev1.ConfigMap) ([]AwsAuthEntry, error) {
	if cm == nil || cm.Data == nil {
		return nil, nil
	}
	var out []AwsAuthEntry

	if raw, ok := cm.Data["mapRoles"]; ok && strings.TrimSpace(raw) != "" {
		rows, err := decodeAwsAuthRows(raw)
		if err != nil {
			return nil, fmt.Errorf("parse mapRoles: %w", err)
		}
		for _, r := range rows {
			if r.RoleArn == "" {
				continue
			}
			out = append(out, AwsAuthEntry{
				PrincipalArn:     r.RoleArn,
				Username:         r.Username,
				KubernetesGroups: r.Groups,
			})
		}
	}

	if raw, ok := cm.Data["mapUsers"]; ok && strings.TrimSpace(raw) != "" {
		rows, err := decodeAwsAuthRows(raw)
		if err != nil {
			return nil, fmt.Errorf("parse mapUsers: %w", err)
		}
		for _, r := range rows {
			if r.UserArn == "" {
				continue
			}
			out = append(out, AwsAuthEntry{
				PrincipalArn:     r.UserArn,
				Username:         r.Username,
				KubernetesGroups: r.Groups,
			})
		}
	}

	return out, nil
}

func decodeAwsAuthRows(raw string) ([]awsAuthRow, error) {
	var rows []awsAuthRow
	if err := yaml.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// assumedRoleArnRe matches the STS assumed-role session ARN form so
// it can be collapsed to its underlying IAM role ARN. The form is:
//
//	arn:aws:sts::ACCOUNT:assumed-role/RoleName/SessionId
//
// Both the role name and the account may contain only the documented
// IAM character set (alphanumerics + `+=,.@_-`); we capture them
// non-greedy and validate by the surrounding literals only.
var assumedRoleArnRe = regexp.MustCompile(`^arn:(aws[\w-]*):sts::(\d+):assumed-role/([^/]+)/.+$`)

// NormalizePrincipalArn returns the canonical key used for matching
// the same IAM principal across sources. Two transformations:
//
//  1. STS assumed-role session ARNs are collapsed to their underlying
//     IAM role ARN. Pod Identity associations return the role form,
//     but anything that flows through STS (cross-account assumes,
//     audit logs) carries the session ARN — the diff matches them as
//     one principal.
//  2. The result is lowercased so case-only differences across
//     aws-auth and Access Entries reconcile correctly. Display layers
//     preserve the original casing separately.
//
// Inputs that don't match a known IAM/STS shape are lowercased and
// returned unchanged — still a useful map key.
func NormalizePrincipalArn(arn string) string {
	arn = strings.TrimSpace(arn)
	if arn == "" {
		return ""
	}
	if m := assumedRoleArnRe.FindStringSubmatch(arn); m != nil {
		partition, account, role := m[1], m[2], m[3]
		arn = fmt.Sprintf("arn:%s:iam::%s:role/%s", partition, account, role)
	}
	return strings.ToLower(arn)
}

// DiffAwsAuthVsAccessEntries reconciles the two cluster-access
// sources by normalized principal ARN. Each distinct principal
// becomes exactly one AwsAuthDiffEntry; the In field marks which
// side(s) it came from; KubernetesGroups is the union across sources
// in stable order.
//
// Output is sorted by the display PrincipalArn so test snapshots and
// SPA rendering are deterministic.
func DiffAwsAuthVsAccessEntries(aa []AwsAuthEntry, ae []AccessEntry) ([]AwsAuthDiffEntry, AwsAuthDiffHealth) {
	type slot struct {
		display string
		inAuth  bool
		inEntry bool
		groups  []string
		seen    map[string]struct{}
	}

	ensure := func(m map[string]*slot, key, display string) *slot {
		s, ok := m[key]
		if !ok {
			s = &slot{display: display, seen: map[string]struct{}{}}
			m[key] = s
		}
		// Preserve the first non-empty display form — both sources
		// may carry casing variants; the first observed wins.
		return s
	}

	addGroups := func(s *slot, groups []string) {
		for _, g := range groups {
			if _, ok := s.seen[g]; ok {
				continue
			}
			s.seen[g] = struct{}{}
			s.groups = append(s.groups, g)
		}
	}

	slots := map[string]*slot{}
	for _, e := range aa {
		key := NormalizePrincipalArn(e.PrincipalArn)
		if key == "" {
			continue
		}
		s := ensure(slots, key, e.PrincipalArn)
		s.inAuth = true
		addGroups(s, e.KubernetesGroups)
	}
	for _, e := range ae {
		key := NormalizePrincipalArn(e.PrincipalArn)
		if key == "" {
			continue
		}
		s := ensure(slots, key, e.PrincipalArn)
		s.inEntry = true
		addGroups(s, e.KubernetesGroups)
	}

	entries := make([]AwsAuthDiffEntry, 0, len(slots))
	var health AwsAuthDiffHealth
	for _, s := range slots {
		var side DiffSide
		switch {
		case s.inAuth && s.inEntry:
			side = DiffSideBoth
			health.Dual++
		case s.inAuth:
			side = DiffSideAwsAuthOnly
			health.AwsAuthOnly++
		case s.inEntry:
			side = DiffSideAccessEntriesOnly
			health.AccessEntriesOnly++
		}
		entries = append(entries, AwsAuthDiffEntry{
			In:               side,
			PrincipalArn:     s.display,
			KubernetesGroups: s.groups,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].PrincipalArn < entries[j].PrincipalArn
	})

	return entries, health
}
