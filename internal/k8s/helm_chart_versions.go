package k8s

import (
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

// sortAndCapVersions filters a raw tag/version list down to the
// SPA-renderable set:
//
//   - Drop tags that don't parse as semver (`latest`, `dev`, `nightly`,
//     `main`, branch names, sha tags). Helm's repo index typically
//     ships only semver versions, but OCI tag listings often include
//     non-semver convenience tags that aren't useful in a version
//     picker.
//   - Sort newest-first.
//   - Cap at MaxVersionsReturned so the picker stays usable.
//
// We accept both bare semver ("1.2.3") and `v`-prefixed ("v1.2.3")
// — Helm itself does. Internally we normalize via semver.Canonical
// for sorting; the original string (with or without v) is what we
// return to the SPA, since charts are referenced by their original
// tag in the registry.
func sortAndCapVersions(raw []string) []string {
	type pair struct {
		original string // exactly what the registry / index reported
		semver   string // canonical "vX.Y.Z" for sorting
	}
	var parsed []pair
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		// semver.IsValid accepts "v1.2.3"; helm tags are usually
		// bare ("1.2.3"), so prepend the v if missing for the check.
		canonical := v
		if !strings.HasPrefix(canonical, "v") {
			canonical = "v" + canonical
		}
		if !semver.IsValid(canonical) {
			continue
		}
		parsed = append(parsed, pair{original: v, semver: canonical})
	}
	sort.Slice(parsed, func(i, j int) bool {
		// Descending by semver. semver.Compare returns -1, 0, 1.
		return semver.Compare(parsed[i].semver, parsed[j].semver) > 0
	})
	if len(parsed) > MaxVersionsReturned {
		parsed = parsed[:MaxVersionsReturned]
	}
	out := make([]string, len(parsed))
	for i, p := range parsed {
		out[i] = p.original
	}
	return out
}
