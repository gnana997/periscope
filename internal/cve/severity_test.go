package cve

import "testing"

func TestCountSeverities_Mix(t *testing.T) {
	got := CountSeverities([]Finding{
		{Severity: "CRITICAL"},
		{Severity: "HIGH"},
		{Severity: "HIGH"},
		{Severity: "medium"}, // case-insensitive
		{Severity: "LOW"},
		{Severity: "INFORMATIONAL"},
		{Severity: "INFO"}, // alias
		{Severity: "unknown"},
	})
	want := SeverityCounts{Critical: 1, High: 2, Medium: 1, Low: 1, Informational: 2}
	if got != want {
		t.Errorf("CountSeverities: got %+v want %+v", got, want)
	}
}

func TestCountSeverities_Empty(t *testing.T) {
	if got := CountSeverities(nil); got != (SeverityCounts{}) {
		t.Errorf("nil findings should yield zero counts, got %+v", got)
	}
}

func TestSeverityCounts_Add(t *testing.T) {
	a := SeverityCounts{Critical: 1, High: 2}
	a.Add(SeverityCounts{Critical: 3, Low: 4})
	want := SeverityCounts{Critical: 4, High: 2, Low: 4}
	if a != want {
		t.Errorf("Add: got %+v want %+v", a, want)
	}
}
