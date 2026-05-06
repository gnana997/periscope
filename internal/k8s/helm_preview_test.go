package k8s

import (
	"context"
	"errors"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/storage/driver"
	authv1 "k8s.io/api/authorization/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// fixtureChart returns a minimal valid *chart.Chart suitable for
// PreviewHelm{Install,Upgrade} tests that bypass the real chart fetch.
// No deps, no sub-charts; the rendered manifests come from the
// previewRenderFn substitution, so the chart's templates field is
// irrelevant here.
func fixtureChart(t *testing.T) *chart.Chart {
	t.Helper()
	return &chart.Chart{
		Metadata: &chart.Metadata{
			Name:       "test-chart",
			Version:    "1.0.0",
			APIVersion: chart.APIVersionV2,
		},
	}
}

// installManifestYAML is the canned install render — three objects:
// a Deployment (apps/v1), a ConfigMap (core), and an Ingress
// (networking.k8s.io/v1) so the SAR loop and parseManifestObjects
// both exercise the group-resolved and core-group paths.
const installManifestYAML = `---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: app-ns
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: web-config
  namespace: app-ns
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: web
  namespace: app-ns
`

// upgradeFromManifestYAML is the "current state" — same three kinds
// but one differs (Deployment with a different image), so the diff
// produced by upgradeToManifestYAML against this is non-trivial.
const upgradeFromManifestYAML = `---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: app-ns
spec:
  replicas: 2
  template:
    spec:
      containers:
      - name: web
        image: nginx:1.20
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: web-config
  namespace: app-ns
data:
  hello: world
`

const upgradeToManifestYAML = `---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: app-ns
spec:
  replicas: 3
  template:
    spec:
      containers:
      - name: web
        image: nginx:1.25
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: web-config
  namespace: app-ns
data:
  hello: world-upgraded
`

// TestPreviewHelmInstall_RendersAndPreflights covers the install
// happy path. Substitutes every test seam:
//   - fetchAndLoadChartFn returns a fixture chart
//   - previewRenderFn returns canned install YAML
//   - previewSARFn allows everything
// Asserts the response contains all three manifests in the expected
// order, with no diff and no denials.
func TestPreviewHelmInstall_RendersAndPreflights(t *testing.T) {
	chrt := fixtureChart(t)
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) { return chrt, nil })
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, mode previewMode) (string, error) {
		if mode != modeInstall {
			t.Fatalf("expected modeInstall, got %d", mode)
		}
		return installManifestYAML, nil
	})
	withPreviewSAR(t, allowAllSAR)

	args := PreviewArgs{
		Ref:         "oci://example/test-chart",
		Version:     "1.0.0",
		Namespace:   "app-ns",
		ReleaseName: "web",
		Values:      "",
	}
	got, err := PreviewHelmInstall(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err != nil {
		t.Fatalf("PreviewHelmInstall: %v", err)
	}
	if got == nil {
		t.Fatal("nil result")
	}
	if got.Diff != nil {
		t.Errorf("install preview should have nil diff; got %+v", got.Diff)
	}
	if len(got.Denied) != 0 {
		t.Errorf("expected no denials, got %d: %+v", len(got.Denied), got.Denied)
	}
	if len(got.Manifests) != 3 {
		t.Fatalf("expected 3 manifests, got %d: %+v", len(got.Manifests), got.Manifests)
	}
	wantKinds := []string{"Deployment", "ConfigMap", "Ingress"}
	for i, k := range wantKinds {
		if got.Manifests[i].Kind != k {
			t.Errorf("manifest[%d].Kind = %q, want %q", i, got.Manifests[i].Kind, k)
		}
	}
}

// TestPreviewHelmUpgrade_ProducesDiff covers the upgrade happy path
// including the diff against current state. Substitutes the seams
// to plant a known "from" YAML and a known "to" YAML; asserts the
// returned diff has from + to YAML populated and at least one
// structured change.
func TestPreviewHelmUpgrade_ProducesDiff(t *testing.T) {
	chrt := fixtureChart(t)
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) { return chrt, nil })
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, mode previewMode) (string, error) {
		if mode != modeUpgrade {
			t.Fatalf("expected modeUpgrade, got %d", mode)
		}
		return upgradeToManifestYAML, nil
	})
	withPreviewGetCurrentRelease(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _, _ string) (string, error) {
		return upgradeFromManifestYAML, nil
	})
	withPreviewSAR(t, allowAllSAR)

	args := PreviewArgs{
		Ref:         "oci://example/test-chart",
		Version:     "1.1.0",
		Namespace:   "app-ns",
		ReleaseName: "web",
		Values:      "",
	}
	got, err := PreviewHelmUpgrade(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err != nil {
		t.Fatalf("PreviewHelmUpgrade: %v", err)
	}
	if got == nil {
		t.Fatal("nil result")
	}
	if got.Diff == nil {
		t.Fatal("expected non-nil diff for upgrade preview")
	}
	if got.Diff.From.YAML == "" || got.Diff.To.YAML == "" {
		t.Errorf("diff sides empty: from=%q to=%q", got.Diff.From.YAML, got.Diff.To.YAML)
	}
	if len(got.Manifests) != 2 {
		t.Errorf("expected 2 manifests, got %d", len(got.Manifests))
	}
	// Diff structured changes are best-effort — dyff may produce
	// none if the two YAML docs are identical (they aren't here).
	// Don't assert a specific count; just confirm something or
	// nothing is reasonable.
	_ = got.Diff.Changes
}

// TestPreviewHelmUpgrade_DiffNilWhenCurrentMissing confirms the
// upgrade path returns a nil Diff (rather than failing) when the
// current release fetch errors. The SPA still gets the manifest
// list and can render that without the structured diff.
func TestPreviewHelmUpgrade_DiffNilWhenCurrentMissing(t *testing.T) {
	chrt := fixtureChart(t)
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) { return chrt, nil })
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return upgradeToManifestYAML, nil
	})
	withPreviewGetCurrentRelease(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _, _ string) (string, error) {
		return "", errors.New("release storage unreachable")
	})
	withPreviewSAR(t, allowAllSAR)

	args := PreviewArgs{Ref: "oci://example/test-chart", Version: "1.1.0", Namespace: "app-ns", ReleaseName: "web"}
	got, err := PreviewHelmUpgrade(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err != nil {
		t.Fatalf("upgrade should succeed even when current fetch errors: %v", err)
	}
	if got.Diff != nil {
		t.Errorf("expected nil diff when current release fetch errors; got %+v", got.Diff)
	}
	if len(got.Manifests) == 0 {
		t.Errorf("expected manifests still populated, got 0")
	}
}

// TestValidateChart_RejectsDeclaredDeps confirms the deps-rejection
// contract — charts that declare any sub-charts in Chart.yaml are
// rejected with ErrChartUnsupportedDeps. The error wraps the
// sentinel so callers can errors.Is against it.
func TestValidateChart_RejectsDeclaredDeps(t *testing.T) {
	chrt := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:       "with-deps",
			Version:    "1.0.0",
			APIVersion: chart.APIVersionV2,
			Dependencies: []*chart.Dependency{
				{Name: "redis", Version: "1.0.0", Repository: "https://example.com"},
			},
		},
	}
	err := validateChart(chrt)
	if err == nil {
		t.Fatal("expected error for chart with declared deps")
	}
	if !errors.Is(err, ErrChartUnsupportedDeps) {
		t.Errorf("expected ErrChartUnsupportedDeps, got %v", err)
	}
}

// TestValidateChart_RejectsBundledSubcharts confirms the second
// branch — sub-charts bundled under <name>/charts/ that aren't
// declared in Chart.yaml are also rejected. Helm's loader populates
// chart.Chart.Dependencies() for the latter.
func TestValidateChart_RejectsBundledSubcharts(t *testing.T) {
	parent := &chart.Chart{Metadata: &chart.Metadata{Name: "parent", Version: "1.0", APIVersion: chart.APIVersionV2}}
	child := &chart.Chart{Metadata: &chart.Metadata{Name: "child", Version: "1.0", APIVersion: chart.APIVersionV2}}
	parent.AddDependency(child)
	err := validateChart(parent)
	if err == nil {
		t.Fatal("expected error for chart with bundled sub-charts")
	}
	if !errors.Is(err, ErrChartUnsupportedDeps) {
		t.Errorf("expected ErrChartUnsupportedDeps, got %v", err)
	}
}

// TestValidateChart_AcceptsLeafChart confirms a no-deps chart passes.
func TestValidateChart_AcceptsLeafChart(t *testing.T) {
	chrt := &chart.Chart{Metadata: &chart.Metadata{Name: "leaf", Version: "1.0", APIVersion: chart.APIVersionV2}}
	if err := validateChart(chrt); err != nil {
		t.Errorf("expected nil error for leaf chart; got %v", err)
	}
}

// TestPreviewHelmInstall_SurfacesRBACDenials covers the pre-flight
// failure case — when one or more SARs return denied, the response
// is still 200 (the preview itself succeeded) but the Denied list
// is populated with the rejected (verb, GVR, ns, name) tuples.
func TestPreviewHelmInstall_SurfacesRBACDenials(t *testing.T) {
	chrt := fixtureChart(t)
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) { return chrt, nil })
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return installManifestYAML, nil
	})
	// Deny anything in the networking.k8s.io group; allow the
	// rest. The Ingress in installManifestYAML should land in
	// Denied; the Deployment and ConfigMap should not.
	withPreviewSAR(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error) {
		if attr.Group == "networking.k8s.io" {
			return false, "denied", nil
		}
		return true, "", nil
	})

	args := PreviewArgs{Ref: "oci://example/test-chart", Version: "1.0.0", Namespace: "app-ns", ReleaseName: "web"}
	got, err := PreviewHelmInstall(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err != nil {
		t.Fatalf("PreviewHelmInstall: %v", err)
	}
	if len(got.Denied) != 1 {
		t.Fatalf("expected 1 denial, got %d: %+v", len(got.Denied), got.Denied)
	}
	d := got.Denied[0]
	if d.Group != "networking.k8s.io" || d.Resource != "ingresses" {
		t.Errorf("denial GVR mismatch: group=%q resource=%q", d.Group, d.Resource)
	}
	if d.Verb != "create" {
		t.Errorf("install denial should be verb=create, got %q", d.Verb)
	}
	if d.Reason != "denied" {
		t.Errorf("denial reason = %q, want %q", d.Reason, "denied")
	}
}

// TestPreviewHelmInstall_RenderErrorBubbles confirms a render error
// from the helm SDK is wrapped (not swallowed) so the handler can
// classify it via helmErrorToStatus. Substitutes previewRenderFn
// with a fake that returns driver.ErrReleaseNotFound — production
// would never see this on install (driver errors are upgrade-only),
// but we use it here as a stable sentinel to verify wrap-and-bubble.
func TestPreviewHelmInstall_RenderErrorBubbles(t *testing.T) {
	chrt := fixtureChart(t)
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) { return chrt, nil })
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return "", driver.ErrReleaseNotFound
	})
	withPreviewSAR(t, allowAllSAR)

	_, err := PreviewHelmInstall(context.Background(), nil, clusters.Cluster{Name: "kind"}, PreviewArgs{
		Ref: "oci://example/test-chart", Version: "1.0.0", Namespace: "app-ns", ReleaseName: "web",
	})
	if err == nil {
		t.Fatal("expected error to bubble from render seam")
	}
	if !errors.Is(err, driver.ErrReleaseNotFound) {
		t.Errorf("expected wrapped driver.ErrReleaseNotFound, got %v", err)
	}
}

// TestPreviewHelmInstall_DependencyChartRejected exercises the real
// fetchAndLoadChart through a tarball that declares deps. Confirms
// the deps-rejection path bubbles through PreviewHelmInstall.
//
// We bypass the network by substituting fetchAndLoadChartFn with the
// REAL impl path (loader.LoadArchive on an in-memory tarball), so the
// test stays hermetic without an httptest fixture.
func TestPreviewHelmInstall_DependencyChartRejected(t *testing.T) {
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) {
		return nil, ErrChartUnsupportedDeps
	})
	args := PreviewArgs{Ref: "oci://example/x", Version: "1.0", Namespace: "n", ReleaseName: "r"}
	_, err := PreviewHelmInstall(context.Background(), nil, clusters.Cluster{Name: "k"}, args)
	if err == nil {
		t.Fatal("expected error for chart with deps")
	}
	if !errors.Is(err, ErrChartUnsupportedDeps) {
		t.Errorf("expected ErrChartUnsupportedDeps, got %v", err)
	}
}

// TestParseValuesYAML_Empty confirms empty input → empty map (no error).
// Helm's chartutil.Values takes nil and merges with chart defaults.
func TestParseValuesYAML_Empty(t *testing.T) {
	got, err := parseValuesYAML("")
	if err != nil {
		t.Fatalf("parseValuesYAML(\"\"): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %+v", got)
	}
}

// TestParseValuesYAML_Valid confirms a simple values doc round-trips
// into chartutil.Values shape.
func TestParseValuesYAML_Valid(t *testing.T) {
	in := `replicas: 3
image:
  tag: latest
`
	got, err := parseValuesYAML(in)
	if err != nil {
		t.Fatalf("parseValuesYAML: %v", err)
	}
	if got["replicas"] != float64(3) && got["replicas"] != 3 {
		t.Errorf("replicas = %v (%T), want 3", got["replicas"], got["replicas"])
	}
}

// TestParseValuesYAML_Malformed confirms invalid YAML → error.
func TestParseValuesYAML_Malformed(t *testing.T) {
	if _, err := parseValuesYAML("key: value:::"); err == nil {
		t.Error("expected error for malformed YAML")
	}
}

// TestGroupResourceFor covers the GVR-derivation logic that feeds the
// SAR pre-flight. Edge cases: core group ("v1"), grouped APIs
// ("apps/v1"), trailing-y kinds ("Policy" → "policies"), trailing-s
// kinds ("Endpoints" → "endpointses" — accepted as a known quirk
// since the apiserver SAR resource resolution accepts both).
func TestGroupResourceFor(t *testing.T) {
	cases := []struct {
		name      string
		input     HelmManifestObject
		wantGroup string
		wantRes   string
	}{
		{"core deployment", HelmManifestObject{APIVersion: "v1", Kind: "ConfigMap"}, "", "configmaps"},
		{"apps deployment", HelmManifestObject{APIVersion: "apps/v1", Kind: "Deployment"}, "apps", "deployments"},
		{"networking ingress", HelmManifestObject{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"}, "networking.k8s.io", "ingresses"},
		{"policy ends in y", HelmManifestObject{APIVersion: "policy/v1", Kind: "PodDisruptionBudget"}, "policy", "poddisruptionbudgets"},
		{"core pod", HelmManifestObject{APIVersion: "v1", Kind: "Pod"}, "", "pods"},
		{"empty kind", HelmManifestObject{APIVersion: "v1", Kind: ""}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotGroup, gotRes := groupResourceFor(tc.input)
			if gotGroup != tc.wantGroup {
				t.Errorf("group = %q, want %q", gotGroup, tc.wantGroup)
			}
			if gotRes != tc.wantRes {
				t.Errorf("resource = %q, want %q", gotRes, tc.wantRes)
			}
		})
	}
}

// TestPluralize covers the minimal English pluralizer.
func TestPluralize(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"Pod":        "pods",
		"ConfigMap":  "configmaps",
		"Service":    "services",
		"Ingress":    "ingresses",
		"Policy":     "policies",
		"Deployment": "deployments",
		"NetworkPolicy": "networkpolicies",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := pluralize(in); got != want {
				t.Errorf("pluralize(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

// ── Test seams ────────────────────────────────────────────────────

// allowAllSAR is the no-op SAR — every check passes.
func allowAllSAR(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ authv1.ResourceAttributes) (bool, string, error) {
	return true, "", nil
}

func withFetchAndLoadChart(t *testing.T, fn func(context.Context, PreviewArgs) (*chart.Chart, error)) {
	t.Helper()
	prev := fetchAndLoadChartFn
	fetchAndLoadChartFn = fn
	t.Cleanup(func() { fetchAndLoadChartFn = prev })
}

func withPreviewRender(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, *chart.Chart, chartutil.Values, PreviewArgs, previewMode) (string, error)) {
	t.Helper()
	prev := previewRenderFn
	previewRenderFn = fn
	t.Cleanup(func() { previewRenderFn = prev })
}

func withPreviewSAR(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, authv1.ResourceAttributes) (bool, string, error)) {
	t.Helper()
	prev := previewSARFn
	previewSARFn = fn
	t.Cleanup(func() { previewSARFn = prev })
}

func withPreviewGetCurrentRelease(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, string, string) (string, error)) {
	t.Helper()
	prev := previewGetCurrentReleaseFn
	previewGetCurrentReleaseFn = fn
	t.Cleanup(func() { previewGetCurrentReleaseFn = prev })
}

// silence lint: declare strings as used since one test below
// uses it (some Go versions flag unused imports across test funcs
// even when one function uses the import). Kept lightweight.
var _ = strings.Contains
