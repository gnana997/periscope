package cve

import (
	"testing"

	"github.com/gnana997/periscope/internal/awsinspector"
)

// ── normalizePackageName ───────────────────────────────────────────

func TestNormalizePackageName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"go/stdlib, go/stdlib", "go/stdlib"},
		{"golang.org/x/crypto, golang.org/x/crypto", "golang.org/x/crypto"},
		{"single-pkg", "single-pkg"},
		{"", "(unknown)"},
		{"   ", "(unknown)"},
		{",,a,,,", "a"},
	}
	for _, c := range cases {
		if got := normalizePackageName(c.in); got != c.want {
			t.Errorf("normalizePackageName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ── version comparison ────────────────────────────────────────────

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.16.1", "1.20.1", -1},
		{"1.20.1", "1.16.1", 1},
		{"1.16.1", "1.16.1", 0},
		{"1.20.1", "1.20.10", -1}, // numeric segments compare numerically
		{"2.0.0", "1.99.99", 1},
		{"0.0.0-20210513164829-c07d793c2f9a", "0.17.0", -1}, // pre-release < release
		{"0.17.0", "0.0.0-20210513164829", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestMaxVersion(t *testing.T) {
	got := maxVersion([]string{"1.16.5", "1.20.1", "1.18.1", "1.24.2"})
	if got != "1.24.2" {
		t.Errorf("maxVersion = %q, want 1.24.2", got)
	}
	if got := maxVersion([]string{}); got != "" {
		t.Errorf("maxVersion([]) = %q, want empty", got)
	}
	if got := maxVersion([]string{"", "", ""}); got != "" {
		t.Errorf("maxVersion(all empty) = %q, want empty", got)
	}
}

// ── exploitTruthy ──────────────────────────────────────────────────

func TestExploitTruthy(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"YES", true},
		{"yes", true},
		{"NO", false},
		{"no", false},
		{"", false},
		{"  ", false},
		{"false", false},
		{"PARTIAL", true}, // fail-open on unknown non-empty values
	}
	for _, c := range cases {
		if got := exploitTruthy(c.in); got != c.want {
			t.Errorf("exploitTruthy(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// ── SortFindings ───────────────────────────────────────────────────

func TestSortFindings_ExploitFirst(t *testing.T) {
	f := []awsinspector.Finding{
		{CVE: "CVE-A", Severity: "HIGH", CVSSv3Score: 7.0},
		{CVE: "CVE-B", Severity: "MEDIUM", CVSSv3Score: 5.0, ExploitAvailable: "YES"},
		{CVE: "CVE-C", Severity: "CRITICAL", CVSSv3Score: 9.0},
	}
	SortFindings(f)
	// Exploit-available wins even when its severity is lower.
	if f[0].CVE != "CVE-B" {
		t.Errorf("first should be exploit-available CVE-B, got %s", f[0].CVE)
	}
	// Within non-exploit findings, severity desc.
	if f[1].CVE != "CVE-C" {
		t.Errorf("second should be CRITICAL CVE-C, got %s", f[1].CVE)
	}
}

func TestSortFindings_SeverityThenCvssThenEpss(t *testing.T) {
	f := []awsinspector.Finding{
		{CVE: "low-cvss-high", Severity: "HIGH", CVSSv3Score: 7.0},
		{CVE: "high-cvss-high", Severity: "HIGH", CVSSv3Score: 9.0},
		{CVE: "tie-cvss-epss-hi", Severity: "HIGH", CVSSv3Score: 9.0, EPSSScore: 0.9},
		{CVE: "critical", Severity: "CRITICAL", CVSSv3Score: 5.0},
	}
	SortFindings(f)
	want := []string{"critical", "tie-cvss-epss-hi", "high-cvss-high", "low-cvss-high"}
	for i, w := range want {
		if f[i].CVE != w {
			t.Errorf("idx %d: want %s, got %s", i, w, f[i].CVE)
		}
	}
}

// ── GroupByPackage ─────────────────────────────────────────────────

func TestGroupByPackage_EmptyInputReturnsEmptySlice(t *testing.T) {
	g := GroupByPackage(nil)
	if g == nil {
		t.Fatal("want non-nil empty slice (so JSON encodes as [])")
	}
	if len(g) != 0 {
		t.Errorf("want 0 groups, got %d", len(g))
	}
}

func TestGroupByPackage_CollapsesDuplicatePackages(t *testing.T) {
	// Inspector emits the same package name multiple times when a CVE
	// matches multiple CPEs of the same package. We collapse them.
	f := []awsinspector.Finding{
		{CVE: "CVE-1", Severity: "HIGH", PackageName: "go/stdlib, go/stdlib", PackageVersion: "1.16.1", FixedVersion: "1.20.1"},
		{CVE: "CVE-2", Severity: "HIGH", PackageName: "go/stdlib", PackageVersion: "1.16.1", FixedVersion: "1.18.1"},
		{CVE: "CVE-3", Severity: "CRITICAL", PackageName: "golang.org/x/crypto", PackageVersion: "0.0.0", FixedVersion: "0.17.0"},
	}
	g := GroupByPackage(f)
	if len(g) != 2 {
		t.Fatalf("want 2 groups, got %d", len(g))
	}
	// CRITICAL package sorts first.
	if g[0].PackageName != "golang.org/x/crypto" {
		t.Errorf("first group should be golang.org/x/crypto, got %s", g[0].PackageName)
	}
	// go/stdlib group has 2 findings, suggested fix = max(1.20.1, 1.18.1).
	stdlib := g[1]
	if stdlib.PackageName != "go/stdlib" {
		t.Errorf("second group should be go/stdlib, got %s", stdlib.PackageName)
	}
	if len(stdlib.Findings) != 2 {
		t.Errorf("go/stdlib should have 2 findings, got %d", len(stdlib.Findings))
	}
	if stdlib.SuggestedFix != "1.20.1" {
		t.Errorf("suggested fix should be 1.20.1, got %s", stdlib.SuggestedFix)
	}
	if stdlib.CurrentVersion != "1.16.1" {
		t.Errorf("current version should be 1.16.1, got %s", stdlib.CurrentVersion)
	}
	if stdlib.Counts.High != 2 || stdlib.Counts.Critical != 0 {
		t.Errorf("go/stdlib counts: want 2H 0C, got %+v", stdlib.Counts)
	}
}

func TestGroupByPackage_PriorityOrder(t *testing.T) {
	// HIGH with exploit should outrank HIGH without — even when the
	// non-exploit one has a higher CVSS.
	f := []awsinspector.Finding{
		{CVE: "no-exp", Severity: "HIGH", CVSSv3Score: 8.0, PackageName: "pkg-no-exp"},
		{CVE: "exp", Severity: "HIGH", CVSSv3Score: 7.0, ExploitAvailable: "YES", PackageName: "pkg-with-exp"},
	}
	g := GroupByPackage(f)
	if g[0].PackageName != "pkg-with-exp" {
		t.Errorf("group with exploit should rank first, got order: %s, %s",
			g[0].PackageName, g[1].PackageName)
	}
}

func TestGroupByPackage_CountsExploitsAndFixables(t *testing.T) {
	f := []awsinspector.Finding{
		{CVE: "CVE-1", Severity: "HIGH", PackageName: "p", FixedVersion: "2.0.0"},
		{CVE: "CVE-2", Severity: "HIGH", PackageName: "p", ExploitAvailable: "YES"},
		{CVE: "CVE-3", Severity: "MEDIUM", PackageName: "p"}, // no fix, no exploit
	}
	g := GroupByPackage(f)
	if len(g) != 1 {
		t.Fatalf("want 1 group, got %d", len(g))
	}
	if g[0].ExploitCount != 1 {
		t.Errorf("exploitCount: want 1, got %d", g[0].ExploitCount)
	}
	if g[0].FixableCount != 1 {
		t.Errorf("fixableCount: want 1, got %d", g[0].FixableCount)
	}
}

func TestGroupByPackage_SortingIsStable(t *testing.T) {
	// Two packages with identical severity profiles — should sort by
	// name for stability across reloads.
	f := []awsinspector.Finding{
		{CVE: "CVE-1", Severity: "HIGH", PackageName: "zlib"},
		{CVE: "CVE-2", Severity: "HIGH", PackageName: "aaa"},
	}
	g := GroupByPackage(f)
	if g[0].PackageName != "aaa" {
		t.Errorf("alphabetical tiebreak: want aaa first, got %s", g[0].PackageName)
	}
}
