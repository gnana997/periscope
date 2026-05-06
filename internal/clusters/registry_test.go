package clusters

import (
	"os"
	"path/filepath"
	"strings"
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

func TestLoadFromFile_backendDefaultsToEKS(t *testing.T) {
	p := writeTempFile(t, `
clusters:
  - name: prod
    arn: arn:aws:eks:us-east-1:123456789012:cluster/prod
    region: us-east-1
`)
	r, err := LoadFromFile(p)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	c, _ := r.ByName("prod")
	if c.Backend != BackendEKS {
		t.Errorf("Backend default = %q, want %q", c.Backend, BackendEKS)
	}
}

func TestLoadFromFile_explicitEKSBackend(t *testing.T) {
	p := writeTempFile(t, `
clusters:
  - name: prod
    backend: eks
    arn: arn:aws:eks:us-east-1:123456789012:cluster/prod
    region: us-east-1
`)
	r, err := LoadFromFile(p)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	c, _ := r.ByName("prod")
	if c.Backend != BackendEKS {
		t.Errorf("Backend = %q, want %q", c.Backend, BackendEKS)
	}
}

func TestLoadFromFile_kubeconfigBackend(t *testing.T) {
	p := writeTempFile(t, `
clusters:
  - name: kind-local
    backend: kubeconfig
    kubeconfigPath: /home/dev/.kube/config
    kubeconfigContext: kind-kind
`)
	r, err := LoadFromFile(p)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	c, ok := r.ByName("kind-local")
	if !ok {
		t.Fatal("ByName(kind-local) not found")
	}
	if c.Backend != BackendKubeconfig {
		t.Errorf("Backend = %q, want %q", c.Backend, BackendKubeconfig)
	}
	if c.KubeconfigPath != "/home/dev/.kube/config" {
		t.Errorf("KubeconfigPath = %q", c.KubeconfigPath)
	}
	if c.KubeconfigContext != "kind-kind" {
		t.Errorf("KubeconfigContext = %q", c.KubeconfigContext)
	}
}

func TestLoadFromFile_kubeconfigContextOptional(t *testing.T) {
	p := writeTempFile(t, `
clusters:
  - name: kind-local
    backend: kubeconfig
    kubeconfigPath: /home/dev/.kube/config
`)
	if _, err := LoadFromFile(p); err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
}

func TestLoadFromFile_mixedBackends(t *testing.T) {
	p := writeTempFile(t, `
clusters:
  - name: prod-eks
    arn: arn:aws:eks:us-east-1:123456789012:cluster/prod
    region: us-east-1
  - name: kind-local
    backend: kubeconfig
    kubeconfigPath: /home/dev/.kube/config
`)
	r, err := LoadFromFile(p)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if got := len(r.List()); got != 2 {
		t.Fatalf("List() len = %d, want 2", got)
	}
}

func TestLoadFromFile_errors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"missing name", "clusters:\n  - arn: arn:aws:eks:us-east-1:1:cluster/a\n    region: us-east-1\n"},
		{"eks: missing arn", "clusters:\n  - name: a\n    region: us-east-1\n"},
		{"eks: missing region", "clusters:\n  - name: a\n    arn: arn:aws:eks:us-east-1:1:cluster/a\n"},
		{"eks: invalid arn", "clusters:\n  - name: a\n    arn: not-an-arn\n    region: us-east-1\n"},
		// ARN+Region pairing on non-EKS backends — operator opted into
		// EKS-side metadata but did not supply the matching region or a
		// parseable ARN. Same enforcement as BackendEKS so AWS calls
		// have a real region to dial and a parseable cluster name.
		{"in-cluster: arn without region", "clusters:\n  - name: a\n    backend: in-cluster\n    arn: arn:aws:eks:us-east-1:1:cluster/a\n"},
		{"agent: malformed arn", "clusters:\n  - name: a\n    backend: agent\n    arn: not-an-arn\n    region: us-east-1\n"},
		{"kubeconfig: arn without region", "clusters:\n  - name: a\n    backend: kubeconfig\n    kubeconfigPath: /tmp/kc\n    arn: arn:aws:eks:us-east-1:1:cluster/a\n"},
		{"kubeconfig: missing path", "clusters:\n  - name: a\n    backend: kubeconfig\n"},
		{"unknown backend", "clusters:\n  - name: a\n    backend: weird\n"},
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

func TestLoadFromFile_environmentAndTags(t *testing.T) {
	p := writeTempFile(t, `
clusters:
  - name: prod
    arn: arn:aws:eks:us-east-1:123456789012:cluster/prod
    region: us-east-1
    environment: prod
    tags:
      team: platform
      costcenter: "42"
  - name: dev
    arn: arn:aws:eks:us-east-1:123456789012:cluster/dev
    region: us-east-1
`)
	r, err := LoadFromFile(p)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	prod, ok := r.ByName("prod")
	if !ok {
		t.Fatal("ByName(prod) not found")
	}
	if prod.Environment != "prod" {
		t.Errorf("Environment = %q, want %q", prod.Environment, "prod")
	}
	if got := prod.Tags["team"]; got != "platform" {
		t.Errorf("Tags[team] = %q, want platform", got)
	}
	if got := prod.Tags["costcenter"]; got != "42" {
		t.Errorf("Tags[costcenter] = %q, want 42", got)
	}

	dev, _ := r.ByName("dev")
	if dev.Environment != "" {
		t.Errorf("dev.Environment = %q, want empty", dev.Environment)
	}
	if len(dev.Tags) != 0 {
		t.Errorf("dev.Tags = %v, want nil/empty", dev.Tags)
	}
}

func TestLoadFromFile_inClusterBackend(t *testing.T) {
	p := writeTempFile(t, `
clusters:
  - name: in-cluster
    backend: in-cluster
`)
	r, err := LoadFromFile(p)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	c, ok := r.ByName("in-cluster")
	if !ok {
		t.Fatal("ByName(in-cluster) not found")
	}
	if c.Backend != BackendInCluster {
		t.Errorf("Backend = %q, want %q", c.Backend, BackendInCluster)
	}
	// in-cluster needs no extra fields — kubeconfig path / ARN / region
	// must all be empty.
	if c.KubeconfigPath != "" || c.ARN != "" || c.Region != "" {
		t.Errorf("unexpected non-empty fields on in-cluster: kubeconfigPath=%q arn=%q region=%q",
			c.KubeconfigPath, c.ARN, c.Region)
	}
}

func TestLoadFromFile_emptyRegistryReturnsEmpty(t *testing.T) {
	// Regression: empty cluster lists used to be a fatal error
	// (`registry contains no clusters`), which crashed the pod on
	// first install before clusters were configured. Now it returns
	// an empty registry and the SPA renders the no-clusters state.
	p := writeTempFile(t, `clusters: []`)
	r, err := LoadFromFile(p)
	if err != nil {
		t.Fatalf("LoadFromFile: empty list should NOT error: %v", err)
	}
	if got := len(r.List()); got != 0 {
		t.Errorf("List() len = %d, want 0", got)
	}
}

func TestCluster_EKSCapable(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		want    bool
		cluster string
	}{
		{
			name: "eks backend (canonical)",
			yaml: "clusters:\n" +
				"  - name: prod\n" +
				"    arn: arn:aws:eks:us-east-1:1:cluster/prod\n" +
				"    region: us-east-1\n",
			cluster: "prod",
			want:    true,
		},
		{
			name: "in-cluster + arn + region",
			yaml: "clusters:\n" +
				"  - name: self\n" +
				"    backend: in-cluster\n" +
				"    arn: arn:aws:eks:us-west-2:1:cluster/self\n" +
				"    region: us-west-2\n",
			cluster: "self",
			want:    true,
		},
		{
			name: "agent + arn + region",
			yaml: "clusters:\n" +
				"  - name: pre-prod\n" +
				"    backend: agent\n" +
				"    arn: arn:aws:eks:us-west-2:1:cluster/pre-prod\n" +
				"    region: us-west-2\n",
			cluster: "pre-prod",
			want:    true,
		},
		{
			name: "in-cluster (no arn) — not capable",
			yaml: "clusters:\n" +
				"  - name: kind-local\n" +
				"    backend: in-cluster\n",
			cluster: "kind-local",
			want:    false,
		},
		{
			name: "agent (no arn) — not capable",
			yaml: "clusters:\n" +
				"  - name: edge-1\n" +
				"    backend: agent\n",
			cluster: "edge-1",
			want:    false,
		},
		{
			name: "kubeconfig (no arn) — not capable",
			yaml: "clusters:\n" +
				"  - name: dev\n" +
				"    backend: kubeconfig\n" +
				"    kubeconfigPath: /tmp/kc\n",
			cluster: "dev",
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := writeTempFile(t, tc.yaml)
			r, err := LoadFromFile(p)
			if err != nil {
				t.Fatalf("LoadFromFile: %v", err)
			}
			c, ok := r.ByName(tc.cluster)
			if !ok {
				t.Fatalf("cluster %q not found in registry", tc.cluster)
			}
			if got := c.EKSCapable(); got != tc.want {
				t.Errorf("EKSCapable() = %v, want %v (cluster=%+v)", got, tc.want, c)
			}
		})
	}
}

// FuzzLoadRegistryBytes hammers the registry YAML loader with
// arbitrary byte sequences. Operator-supplied input — a pod that
// panics on a typo'd config is worse than one that errors cleanly,
// so the contract under fuzz is "either a valid Registry or a
// non-nil error, never a panic".
//
// We additionally check that any cluster present in a successfully-
// loaded registry passes the same invariants the loader enforces:
// non-empty name, recognized backend, and (when ARN is set on any
// backend) a parseable EKSName + non-empty Region. This catches
// regressions where new validation paths leak through.
//
// Run locally with:
//
//	go test ./internal/clusters/ -run none -fuzz FuzzLoadRegistryBytes -fuzztime 30s
func FuzzLoadRegistryBytes(f *testing.F) {
	// Seed corpus — happy paths plus the kinds of malformed YAML an
	// operator could realistically paste in (anchors, merge keys,
	// numeric vs string scalar coercion, deeply nested aliases).
	seeds := []string{
		``,
		`clusters: []`,
		"clusters:\n  - name: a\n    arn: arn:aws:eks:us-east-1:1:cluster/a\n    region: us-east-1\n",
		"clusters:\n  - name: a\n    backend: in-cluster\n",
		"clusters:\n  - name: a\n    backend: agent\n    arn: arn:aws:eks:us-east-1:1:cluster/a\n    region: us-east-1\n",
		"clusters:\n  - name: a\n    backend: kubeconfig\n    kubeconfigPath: /tmp/kc\n",
		"clusters:\n  - name: a\n    backend: weird\n",
		"clusters:\n  - name: a\n    arn: not-an-arn\n    region: us-east-1\n",
		"clusters:\n  - {}\n",
		"clusters: 42",
		"clusters:\n  - &x\n    name: a\n    arn: arn:aws:eks:us-east-1:1:cluster/a\n    region: us-east-1\n  - <<: *x\n    name: b\n",
		"clusters:\n  - name: \"\\x00\"\n    backend: in-cluster\n",
		strings.Repeat("a: b\n", 1000),
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		// 64 KiB is well above any realistic cluster registry; cap
		// here so the fuzzer doesn't burn cycles on multi-MB inputs
		// that don't materially expand coverage. Real-world the file
		// is read from disk so size is operator-bounded anyway.
		if len(raw) > 64*1024 {
			t.Skip("oversized input — out of realistic config-file range")
		}

		reg, err := LoadFromBytes(raw)

		// Property 1 — exactly one of (reg, err) is non-nil.
		switch {
		case reg == nil && err == nil:
			t.Fatalf("both reg and err are nil")
		case reg != nil && err != nil:
			t.Fatalf("both reg and err are non-nil: reg=%+v err=%v", reg, err)
		}

		if reg == nil {
			return
		}

		// Property 2 — every loaded cluster satisfies invariants.
		for _, c := range reg.List() {
			if c.Name == "" {
				t.Fatalf("loaded cluster has empty Name: %+v", c)
			}
			switch c.Backend {
			case BackendEKS, BackendKubeconfig, BackendInCluster, BackendAgent:
				// recognized
			default:
				t.Fatalf("loaded cluster has unrecognized Backend %q: %+v", c.Backend, c)
			}
			// When ARN is set on any backend, the loader must have
			// validated Region presence + ARN parseability. The
			// EKSCapable invariant should hold uniformly.
			if c.ARN != "" {
				if c.Region == "" {
					t.Fatalf("loaded cluster %q has ARN but empty Region", c.Name)
				}
				if c.EKSName() == "" {
					t.Fatalf("loaded cluster %q has unparseable ARN %q", c.Name, c.ARN)
				}
				if !c.EKSCapable() {
					t.Fatalf("loaded cluster %q has ARN+Region but EKSCapable=false: %+v", c.Name, c)
				}
			}
		}
	})
}
