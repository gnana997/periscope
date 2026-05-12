package cve

import "strings"

// SeverityCounts is the per-severity histogram surfaced on chip
// rows. Pure data shape — no JSON tags here; the wire DTOs in
// types.go add the tags so the in-memory aggregation can stay
// representation-agnostic.
type SeverityCounts struct {
	Critical      int
	High          int
	Medium        int
	Low           int
	Informational int
}

// CountSeverities folds a finding slice into a severity histogram.
// Inspector reports the severity string in upper case
// (CRITICAL/HIGH/MEDIUM/LOW/INFORMATIONAL); we accept any case so
// future projections that lower-case the field still work.
// Unknown severities are silently ignored — defensive against
// future Inspector additions we haven't mapped yet.
func CountSeverities(findings []Finding) SeverityCounts {
	var c SeverityCounts
	for _, f := range findings {
		switch strings.ToUpper(f.Severity) {
		case "CRITICAL":
			c.Critical++
		case "HIGH":
			c.High++
		case "MEDIUM":
			c.Medium++
		case "LOW":
			c.Low++
		case "INFORMATIONAL", "INFO":
			c.Informational++
		}
	}
	return c
}

// Add folds another set of counts into c. Used by the per-pod
// roll-up that sums across container digests.
func (c *SeverityCounts) Add(o SeverityCounts) {
	c.Critical += o.Critical
	c.High += o.High
	c.Medium += o.Medium
	c.Low += o.Low
	c.Informational += o.Informational
}
