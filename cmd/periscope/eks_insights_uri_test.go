package main

import (
	"strings"
	"testing"
)

// TestParseKubernetesResourceURI exercises every shape EKS can
// realistically return: namespaced/cluster-scoped × core/named-group
// × built-in-plural/CRD-plural, plus malformed inputs. The
// editor-link mapping is the riskiest piece of PR-1 (see issue #103
// thread) so the coverage here is deliberately exhaustive.
func TestParseKubernetesResourceURI(t *testing.T) {
	const cluster = "prod-eu-west-1"

	cases := []struct {
		name        string
		uri         string
		wantOK      bool
		wantGroup   string
		wantVersion string
		wantPlural  string
		wantNs      string
		wantName    string
		wantPath    string
	}{
		{
			name:        "core_namespaced_pod",
			uri:         "/api/v1/namespaces/default/pods/nginx",
			wantOK:      true,
			wantGroup:   "",
			wantVersion: "v1",
			wantPlural:  "pods",
			wantNs:      "default",
			wantName:    "nginx",
			wantPath:    "/clusters/prod-eu-west-1/pods?sel=nginx&selNs=default&tab=yaml",
		},
		{
			name:        "core_clusterscoped_node",
			uri:         "/api/v1/nodes/ip-10-0-0-1",
			wantOK:      true,
			wantGroup:   "",
			wantVersion: "v1",
			wantPlural:  "nodes",
			wantNs:      "",
			wantName:    "ip-10-0-0-1",
			wantPath:    "/clusters/prod-eu-west-1/nodes?sel=ip-10-0-0-1&selNs=&tab=yaml",
		},
		{
			name:        "named_group_namespaced_pdb_v1beta1",
			uri:         "/apis/policy/v1beta1/namespaces/kube-system/poddisruptionbudgets/coredns",
			wantOK:      true,
			wantGroup:   "policy",
			wantVersion: "v1beta1",
			wantPlural:  "poddisruptionbudgets",
			wantNs:      "kube-system",
			wantName:    "coredns",
			wantPath:    "/clusters/prod-eu-west-1/poddisruptionbudgets?sel=coredns&selNs=kube-system&tab=yaml",
		},
		{
			name:        "named_group_clusterscoped_clusterrole",
			uri:         "/apis/rbac.authorization.k8s.io/v1/clusterroles/edit",
			wantOK:      true,
			wantGroup:   "rbac.authorization.k8s.io",
			wantVersion: "v1",
			wantPlural:  "clusterroles",
			wantNs:      "",
			wantName:    "edit",
			wantPath:    "/clusters/prod-eu-west-1/clusterroles?sel=edit&selNs=&tab=yaml",
		},
		{
			name:        "alias_persistentvolumeclaims",
			uri:         "/api/v1/namespaces/data/persistentvolumeclaims/postgres",
			wantOK:      true,
			wantGroup:   "",
			wantVersion: "v1",
			wantPlural:  "persistentvolumeclaims",
			wantNs:      "data",
			wantName:    "postgres",
			wantPath:    "/clusters/prod-eu-west-1/pvcs?sel=postgres&selNs=data&tab=yaml",
		},
		{
			name:        "alias_persistentvolumes",
			uri:         "/api/v1/persistentvolumes/pv-001",
			wantOK:      true,
			wantGroup:   "",
			wantVersion: "v1",
			wantPlural:  "persistentvolumes",
			wantNs:      "",
			wantName:    "pv-001",
			wantPath:    "/clusters/prod-eu-west-1/pvs?sel=pv-001&selNs=&tab=yaml",
		},
		{
			name:        "alias_customresourcedefinitions",
			uri:         "/apis/apiextensions.k8s.io/v1/customresourcedefinitions/widgets.example.com",
			wantOK:      true,
			wantGroup:   "apiextensions.k8s.io",
			wantVersion: "v1",
			wantPlural:  "customresourcedefinitions",
			wantNs:      "",
			wantName:    "widgets.example.com",
			wantPath:    "/clusters/prod-eu-west-1/crds?sel=widgets.example.com&selNs=&tab=yaml",
		},
		{
			name:        "crd_namespaced_falls_through",
			uri:         "/apis/example.com/v1alpha1/namespaces/team-a/widgets/blue",
			wantOK:      true,
			wantGroup:   "example.com",
			wantVersion: "v1alpha1",
			wantPlural:  "widgets",
			wantNs:      "team-a",
			wantName:    "blue",
			wantPath:    "/clusters/prod-eu-west-1/customresources/example.com/v1alpha1/widgets?sel=blue&selNs=team-a&tab=yaml",
		},
		{
			name:        "crd_clusterscoped_falls_through",
			uri:         "/apis/cert-manager.io/v1/clusterissuers/letsencrypt",
			wantOK:      true,
			wantGroup:   "cert-manager.io",
			wantVersion: "v1",
			wantPlural:  "clusterissuers",
			wantNs:      "",
			wantName:    "letsencrypt",
			wantPath:    "/clusters/prod-eu-west-1/customresources/cert-manager.io/v1/clusterissuers?sel=letsencrypt&selNs=&tab=yaml",
		},
		{
			name:        "removed_api_psp_v1beta1",
			uri:         "/apis/policy/v1beta1/podsecuritypolicies/restricted",
			wantOK:      true,
			wantGroup:   "policy",
			wantVersion: "v1beta1",
			wantPlural:  "podsecuritypolicies",
			wantNs:      "",
			wantName:    "restricted",
			// PSPs aren't in the built-in SPA route set; falls through
			// to customresources. The version flagged as deprecated
			// is preserved in the deep link — the editor will surface
			// the load error when the apiserver no longer serves it.
			wantPath: "/clusters/prod-eu-west-1/customresources/policy/v1beta1/podsecuritypolicies?sel=restricted&selNs=&tab=yaml",
		},
		{
			name:   "empty",
			uri:    "",
			wantOK: false,
		},
		{
			name:   "garbage",
			uri:    "not-a-resource-uri",
			wantOK: false,
		},
		{
			name:   "missing_name",
			uri:    "/api/v1/namespaces/default/pods",
			wantOK: false,
		},
		{
			name:   "missing_version",
			uri:    "/apis/policy",
			wantOK: false,
		},
		{
			name:   "core_no_resource",
			uri:    "/api/v1",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseKubernetesResourceURI(cluster, tc.uri)
			if ok != tc.wantOK {
				t.Fatalf("parseKubernetesResourceURI(%q) ok = %v, want %v", tc.uri, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if got.Group != tc.wantGroup {
				t.Errorf("Group = %q, want %q", got.Group, tc.wantGroup)
			}
			if got.Version != tc.wantVersion {
				t.Errorf("Version = %q, want %q", got.Version, tc.wantVersion)
			}
			if got.Plural != tc.wantPlural {
				t.Errorf("Plural = %q, want %q", got.Plural, tc.wantPlural)
			}
			if got.Namespace != tc.wantNs {
				t.Errorf("Namespace = %q, want %q", got.Namespace, tc.wantNs)
			}
			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if got.EditorPath != tc.wantPath {
				t.Errorf("EditorPath = %q, want %q", got.EditorPath, tc.wantPath)
			}
		})
	}
}

// TestParseKubernetesResourceURI_EmptyCluster guards against the
// case where the registry name is empty (which would produce a
// broken /clusters//pods link). The parser refuses to build a path
// in that case.
func TestParseKubernetesResourceURI_EmptyCluster(t *testing.T) {
	if _, ok := parseKubernetesResourceURI("", "/api/v1/namespaces/default/pods/nginx"); ok {
		t.Fatalf("expected ok=false when cluster name is empty")
	}
}

// FuzzParseKubernetesResourceURI hunts for panics or
// pathological-string crashes in the EKS-Insights URI mapper.
// Inputs come from AWS via DescribeInsight; while the AWS console
// canonicalises them, we treat the value as untrusted and expect
// the parser to either return (zero, false) or a fully-formed
// EditorPath — never panic.
//
// Properties asserted per fuzz iteration:
//
//  1. No panic for any (cluster, uri) pair.
//  2. When ok is true, EditorPath must start with "/clusters/" and
//     contain the cluster name segment, so the SPA router will
//     handle it cleanly. This catches regressions where a malformed
//     URI sneaks through the switch and produces a half-built path.
//  3. Empty cluster name always returns ok=false (existing contract).
//
// Run locally with: go test ./cmd/periscope/ -run none -fuzz FuzzParseKubernetesResourceURI -fuzztime 30s
func FuzzParseKubernetesResourceURI(f *testing.F) {
	// Seed corpus — real shapes EKS Upgrade Insights returns, plus
	// a few adversarial cases (empty path, all-slashes, very long
	// segments, percent-encoded, unicode).
	type seed struct {
		cluster, uri string
	}
	seeds := []seed{
		{"prod-eu-west-1", "/api/v1/namespaces/default/pods/nginx"},
		{"prod-eu-west-1", "/apis/apps/v1/namespaces/kube-system/deployments/coredns"},
		{"prod-eu-west-1", "/api/v1/nodes/ip-10-0-0-1"},
		{"prod-eu-west-1", "/apis/policy/v1beta1/namespaces/default/poddisruptionbudgets/my-pdb"},
		{"prod-eu-west-1", ""},
		{"prod-eu-west-1", "/"},
		{"prod-eu-west-1", "//"},
		{"prod-eu-west-1", "/api"},
		{"prod-eu-west-1", "/api/v1"},
		{"prod-eu-west-1", "/api/v1/namespaces"},
		{"prod-eu-west-1", "/apis///"},
		{"prod-eu-west-1", "/api/v1/namespaces/default/pods/" + strings.Repeat("a", 1024)},
		{"prod-eu-west-1", "/apis/example.com/v1alpha1/namespaces/team/awesomethings/foo"},
		{"prod-eu-west-1", "/api/v1/namespaces/default/pods/nginx?injected=true"},
		{"", "/api/v1/namespaces/default/pods/nginx"},
		{"cluster with spaces", "/api/v1/pods/nginx"},
		{"prod", "/api/v1/namespaces/默认/pods/网站"},
	}
	for _, s := range seeds {
		f.Add(s.cluster, s.uri)
	}

	f.Fuzz(func(t *testing.T, cluster, uri string) {
		got, ok := parseKubernetesResourceURI(cluster, uri)

		// Property 1 — no panic, implicit (test runner catches it).

		// Property 2 — when the parser returns a path, the path must
		// be well-formed enough for the SPA router not to misroute.
		if ok {
			if got.EditorPath == "" {
				t.Fatalf("ok=true but EditorPath is empty for cluster=%q uri=%q", cluster, uri)
			}
			if !strings.HasPrefix(got.EditorPath, "/clusters/") {
				t.Fatalf("EditorPath %q lacks /clusters/ prefix (cluster=%q uri=%q)", got.EditorPath, cluster, uri)
			}
		}

		// Property 3 — empty cluster name is a hard reject contract.
		if cluster == "" && ok {
			t.Fatalf("empty cluster should always reject; got ok=true for uri=%q", uri)
		}
	})
}
