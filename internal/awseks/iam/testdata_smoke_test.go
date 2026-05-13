package iam

import (
	"encoding/json"
	"testing"
)

// TestFixtureCorpus_Loads is the self-validating contract for the
// testdata corpus. Every fixture under managed/, sensitive/, and
// wildcard/ MUST be valid JSON (the parser comes later; this just
// guards against committing typos). Pathological fixtures are
// asserted separately because one of them (malformed-json.json)
// is intentionally invalid.
//
// Counts assert that the corpus is the size the plan says it
// should be — guards against accidental fixture deletion.
func TestFixtureCorpus_Loads(t *testing.T) {
	cases := []struct {
		subdir   string
		wantSize int
	}{
		{"managed", 10},
		{"sensitive", 18},
		{"wildcard", 10},
	}
	for _, tc := range cases {
		t.Run(tc.subdir, func(t *testing.T) {
			pairs := loadFixturePairs(t, tc.subdir)
			if len(pairs) != tc.wantSize {
				t.Errorf("%s: got %d fixtures, want %d (check additions/deletions)",
					tc.subdir, len(pairs), tc.wantSize)
			}
			for _, p := range pairs {
				var raw map[string]interface{}
				if err := json.Unmarshal(p.Input, &raw); err != nil {
					t.Errorf("%s: invalid JSON: %v", p.Name, err)
				}
			}
		})
	}
}

// TestFixtureCorpus_Pathological asserts the pathological/ subdir
// matches its expected fixtures, with the explicit understanding
// that malformed-json.json is intentionally NOT valid JSON.
func TestFixtureCorpus_Pathological(t *testing.T) {
	pairs := loadFixturePairs(t, "pathological")
	if len(pairs) != 8 {
		t.Errorf("pathological: got %d fixtures, want 8", len(pairs))
	}

	wantValidJSON := map[string]bool{
		"pathological/not-action-deny":          true,
		"pathological/not-resource-allow":       true,
		"pathological/allow-deny-same-action":   true,
		"pathological/empty-statement-array":    true,
		"pathological/single-statement-object":  true,
		"pathological/fifty-statements":         true,
		"pathological/arn-variable-username":    true,
		"pathological/malformed-json":           false, // explicitly invalid
	}

	for _, p := range pairs {
		want, ok := wantValidJSON[p.Name]
		if !ok {
			t.Errorf("unexpected fixture %s — update wantValidJSON map", p.Name)
			continue
		}
		var raw map[string]interface{}
		err := json.Unmarshal(p.Input, &raw)
		if want && err != nil {
			t.Errorf("%s: expected valid JSON, got parse error: %v", p.Name, err)
		}
		if !want && err == nil {
			t.Errorf("%s: expected INVALID JSON (the malformed case), but parser accepted it", p.Name)
		}
	}
}
