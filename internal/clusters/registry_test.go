package clusters

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "clusters.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestLoadFromFile_valid(t *testing.T) {
	p := writeTempFile(t, `
clusters:
  - name: prod
    arn: arn:aws:eks:us-east-1:123456789012:cluster/prod
    region: us-east-1
  - name: staging
    arn: arn:aws:eks:us-west-2:123456789012:cluster/staging-cluster
    region: us-west-2
`)
	r, err := LoadFromFile(p)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	got := r.List()
	if len(got) != 2 {
		t.Fatalf("List() len = %d, want 2", len(got))
	}
	if got[0].Name != "prod" || got[1].Name != "staging" {
		t.Errorf("names = %q,%q; want prod,staging", got[0].Name, got[1].Name)
	}
	c, ok := r.ByName("prod")
	if !ok || c.Region != "us-east-1" {
		t.Errorf("ByName(prod) = %+v, ok=%v", c, ok)
	}
	if _, ok := r.ByName("missing"); ok {
		t.Errorf("ByName(missing) returned ok=true")
	}
}

func TestLoadFromFile_errors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"empty cluster list", `clusters: []`},
		{"missing name", "clusters:\n  - arn: arn:aws:eks:us-east-1:1:cluster/a\n    region: us-east-1\n"},
		{"missing arn", "clusters:\n  - name: a\n    region: us-east-1\n"},
		{"missing region", "clusters:\n  - name: a\n    arn: arn:aws:eks:us-east-1:1:cluster/a\n"},
		{"invalid arn", "clusters:\n  - name: a\n    arn: not-an-arn\n    region: us-east-1\n"},
		{"duplicate name", `
clusters:
  - name: prod
    arn: arn:aws:eks:us-east-1:1:cluster/prod
    region: us-east-1
  - name: prod
    arn: arn:aws:eks:us-west-2:1:cluster/prod-west
    region: us-west-2
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTempFile(t, tc.body)
			if _, err := LoadFromFile(p); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestLoadFromFile_missingFile(t *testing.T) {
	if _, err := LoadFromFile("/nonexistent/path/clusters.yaml"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCluster_EKSName(t *testing.T) {
	cases := map[string]struct {
		arn  string
		want string
	}{
		"valid":   {"arn:aws:eks:us-east-1:123456789012:cluster/prod", "prod"},
		"hyphen":  {"arn:aws:eks:us-east-1:123456789012:cluster/prod-east-1", "prod-east-1"},
		"invalid": {"not-an-arn", ""},
		"empty":   {"", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := Cluster{ARN: c.arn}.EKSName()
			if got != c.want {
				t.Errorf("EKSName(%q) = %q, want %q", c.arn, got, c.want)
			}
		})
	}
}

func TestEmpty(t *testing.T) {
	r := Empty()
	if got := r.List(); len(got) != 0 {
		t.Errorf("Empty().List() len = %d, want 0", len(got))
	}
	if _, ok := r.ByName("anything"); ok {
		t.Errorf("Empty().ByName returned ok=true")
	}
}
