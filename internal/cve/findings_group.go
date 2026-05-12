// findings_group — pre-grouped/prioritized projection of raw
// Inspector findings into the "what to fix first" surface the
// Security tab + future MCP / AI-agent tool calls consume.
//
// The operator's mental model is "bump these packages", not "review
// these 219 individual CVEs". A v1.0.7-rc1 smoke run on grafana
// 8.0.0 returned 219 raw findings — visually overwhelming, mostly
// repeats of go/stdlib. After grouping the same data collapses to
// ~5 packages with per-group severity rollups + a "fixes all"
// upgrade target.
//
// Computing this server-side means:
//   - SPA and MCP/AI-agent tool calls share one shape (single
//     source of truth for prioritization).
//   - The wire payload is smaller (no per-row repeat of package +
//     fixed-version text).
//   - Filter parameters added to the endpoint (severity-only,
//     exploits-only, fixable-only) become MCP tool arguments
//     naturally.
//
// All exports here are deterministic, pure (no AWS / k8s side
// effects), and unit-tested in findings_group_test.go.

package cve

import (
	"sort"
	"strings"

	"github.com/gnana997/periscope/internal/awsinspector"
)

// PackageGroup is the per-package projection of raw findings. One
// container row carries a slice of these.
type PackageGroup struct {
	// PackageName is the canonical package identifier
	// (normalizePackageName output). Inspector sometimes returns
	// comma-joined repeats ("go/stdlib, go/stdlib") for the same
	// CVE; we collapse to the first token.
	PackageName string `json:"packageName"`

	// CurrentVersion is the version installed in the scanned image,
	// taken from the first non-empty packageVersion in the group.
	// Inspector reports the same package version on every finding
	// in a group, so first-non-empty is a safe pick.
	CurrentVersion string `json:"currentVersion,omitempty"`

	// SuggestedFix is the highest fixedVersion seen across all
	// findings in the group — upgrading to this version closes
	// every CVE in the group. Empty when no fix has been published
	// for any finding in the group.
	SuggestedFix string `json:"suggestedFix,omitempty"`

	// Findings is the sorted CVE list. Ordering: exploits first,
	// then severity desc, then CVSS desc, then EPSS desc, then CVE
	// ID asc. Pre-sorted so MCP tool calls and the SPA both render
	// the most-actionable rows first.
	Findings []awsinspector.Finding `json:"findings"`

	// Counts is the per-severity tally for this group. Drives the
	// per-package header chip.
	Counts WireSeverityCounts `json:"counts"`

	// ExploitCount is the number of findings with exploitAvailable
	// truthy. Surfaced in the header so the operator sees the most
	// urgent groups at a glance.
	ExploitCount int `json:"exploitCount"`

	// FixableCount is the number of findings whose fixedVersion is
	// non-empty. Operators bumping packages typically work on the
	// "fixable" subset; un-fixable findings are tracked for context
	// but can't be remediated without a CVE-side patch.
	FixableCount int `json:"fixableCount"`
}

// GroupByPackage collapses a flat slice of findings into per-package
// groups, sorted by triage priority (worst severity first, then
// exploit count, then severityScore, then top CVSS, then package
// name for stable ordering).
//
// Returns an empty (non-nil) slice when input is empty so JSON
// marshallers emit `[]` rather than `null`.
func GroupByPackage(findings []awsinspector.Finding) []PackageGroup {
	if len(findings) == 0 {
		return []PackageGroup{}
	}
	buckets := make(map[string][]awsinspector.Finding, 8)
	keyOrder := make([]string, 0, 8)
	for _, f := range findings {
		k := normalizePackageName(f.PackageName)
		if _, ok := buckets[k]; !ok {
			keyOrder = append(keyOrder, k)
		}
		buckets[k] = append(buckets[k], f)
	}

	groups := make([]PackageGroup, 0, len(buckets))
	for _, k := range keyOrder {
		items := buckets[k]
		SortFindings(items)
		fixedVersions := make([]string, 0, len(items))
		var currentVersion string
		exploits, fixables := 0, 0
		counts := SeverityCounts{}
		for _, f := range items {
			if currentVersion == "" && f.PackageVersion != "" {
				currentVersion = f.PackageVersion
			}
			if f.FixedVersion != "" {
				fixedVersions = append(fixedVersions, f.FixedVersion)
				fixables++
			}
			if exploitTruthy(f.ExploitAvailable) {
				exploits++
			}
			counts.Add(severityCountsOf(f))
		}
		groups = append(groups, PackageGroup{
			PackageName:    k,
			CurrentVersion: currentVersion,
			SuggestedFix:   maxVersion(fixedVersions),
			Findings:       items,
			Counts:         WireSeverity(counts),
			ExploitCount:   exploits,
			FixableCount:   fixables,
		})
	}

	sort.SliceStable(groups, func(i, j int) bool {
		gi, gj := groups[i], groups[j]
		ri, rj := worstRank(gi), worstRank(gj)
		if ri != rj {
			return ri > rj
		}
		if gi.ExploitCount != gj.ExploitCount {
			return gi.ExploitCount > gj.ExploitCount
		}
		si, sj := groupScore(gi.Counts), groupScore(gj.Counts)
		if si != sj {
			return si > sj
		}
		ci, cj := topCvss(gi.Findings), topCvss(gj.Findings)
		if ci != cj {
			return ci > cj
		}
		return gi.PackageName < gj.PackageName
	})
	return groups
}

// normalizePackageName splits a comma-joined Inspector packageName
// ("go/stdlib, go/stdlib") into the canonical first non-empty token.
// Inspector emits one CPE-match per row; if a CVE matches the same
// package twice the field repeats. Treating both as one group is
// what the operator expects.
func normalizePackageName(raw string) string {
	if raw == "" {
		return "(unknown)"
	}
	for _, tok := range strings.Split(raw, ",") {
		t := strings.TrimSpace(tok)
		if t != "" {
			return t
		}
	}
	return "(unknown)"
}

// SortFindings ranks the most-actionable findings first. Mutates
// the input slice (matches Go's sort.Sort convention). Used by
// GroupByPackage on each per-package bucket; exported so call sites
// that want a flat sorted view can reuse the same ordering.
func SortFindings(findings []awsinspector.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		fi, fj := findings[i], findings[j]
		ei, ej := exploitTruthy(fi.ExploitAvailable), exploitTruthy(fj.ExploitAvailable)
		if ei != ej {
			return ei
		}
		ri, rj := severityRank(fi.Severity), severityRank(fj.Severity)
		if ri != rj {
			return ri > rj
		}
		if fi.CVSSv3Score != fj.CVSSv3Score {
			return fi.CVSSv3Score > fj.CVSSv3Score
		}
		if fi.EPSSScore != fj.EPSSScore {
			return fi.EPSSScore > fj.EPSSScore
		}
		return fi.CVE < fj.CVE
	})
}

// ── Internal scoring + helpers ─────────────────────────────────────

func severityRank(s string) int {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "CRITICAL":
		return 4
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	case "LOW":
		return 1
	case "INFORMATIONAL", "INFO":
		return 0
	}
	return 0
}

// exploitTruthy treats a non-empty, non-"NO" ExploitAvailable string
// as evidence of an exploit. Inspector emits "YES" / "NO" today; we
// fail-open on unknown values so a renamed sentinel doesn't drop
// rows from the priority bucket.
func exploitTruthy(s string) bool {
	u := strings.ToUpper(strings.TrimSpace(s))
	if u == "" || u == "NO" || u == "FALSE" {
		return false
	}
	return true
}

// maxVersion returns the highest version string in versions, using
// the same best-effort semver-ish split-on-`.`-and-`-` comparison
// used by version libraries that don't want a full semver dep. Empty
// string when input is empty.
func maxVersion(versions []string) string {
	best := ""
	for _, v := range versions {
		if v == "" {
			continue
		}
		if best == "" || compareVersions(v, best) > 0 {
			best = v
		}
	}
	return best
}

// compareVersions returns -1/0/1 ordering two version strings using
// segment-wise comparison (split on `.` and `-`, compare numeric
// segments numerically and string segments lexicographically).
// Handles go/stdlib "1.16.1", "1.20.1", "1.24.2", and
// golang.org/x/crypto "0.0.0-20210513..." style without a full
// semver parser.
func compareVersions(a, b string) int {
	if a == b {
		return 0
	}
	pa := splitVersion(a)
	pb := splitVersion(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y string
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		nx, okx := parseUint(x)
		ny, oky := parseUint(y)
		if okx && oky {
			if nx != ny {
				if nx < ny {
					return -1
				}
				return 1
			}
		} else if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func splitVersion(v string) []string {
	// Split on both `.` and `-` so 0.0.0-20210513164829-c07d793c2f9a
	// breaks into [0, 0, 0, 20210513164829, c07d793c2f9a] for
	// segment-wise comparison.
	return strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' })
}

func parseUint(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	var v uint64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		v = v*10 + uint64(r-'0')
	}
	return v, true
}

func worstRank(g PackageGroup) int {
	switch {
	case g.Counts.Critical > 0:
		return 4
	case g.Counts.High > 0:
		return 3
	case g.Counts.Medium > 0:
		return 2
	case g.Counts.Low > 0:
		return 1
	}
	return 0
}

func groupScore(c WireSeverityCounts) int {
	return c.Critical*1000 + c.High*100 + c.Medium*10 + c.Low
}

func topCvss(findings []awsinspector.Finding) float64 {
	var best float64
	for _, f := range findings {
		if f.CVSSv3Score > best {
			best = f.CVSSv3Score
		}
	}
	return best
}

// severityCountsOf returns a 1-bucket SeverityCounts for a single
// finding (the severity it sits in is +1; the rest stay 0). Used by
// the GroupByPackage Counts accumulator.
func severityCountsOf(f awsinspector.Finding) SeverityCounts {
	c := SeverityCounts{}
	switch strings.ToUpper(strings.TrimSpace(f.Severity)) {
	case "CRITICAL":
		c.Critical = 1
	case "HIGH":
		c.High = 1
	case "MEDIUM":
		c.Medium = 1
	case "LOW":
		c.Low = 1
	case "INFORMATIONAL", "INFO":
		c.Informational = 1
	}
	return c
}
