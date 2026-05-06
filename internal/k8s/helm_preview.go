// helm_preview.go — dry-run preview for helm install / upgrade.
//
// Wraps helm's pkg/action.NewInstall(...).Run(DryRun=true, ClientOnly=true)
// for install mode and pkg/action.NewUpgrade(...).Run(DryRun=true) for
// upgrade mode. The output is:
//
//   - The list of K8s manifests helm would apply (parsed via the
//     existing parseManifestObjects helper from helm.go).
//   - A semantic diff against the live release manifest (upgrade only;
//     null for install).
//   - A pre-flight RBAC denied list — every manifest's (verb, GVR, ns,
//     name) tuple is run through the impersonated SAR endpoint, and
//     denials surface inline so the SPA can show the operator exactly
//     what the apiserver would reject before they hit Apply.
//
// The SAR loop runs sequentially (not in parallel) because the cani
// path uses impersonation headers and we don't want to fan out a
// per-preview SAR storm against the apiserver. Helm's dry-run already
// adds 1-2s of latency; the SARs add tens of ms even for a 30-manifest
// chart, which is well within the operator's perceived budget.

package k8s

import (
	"bytes"
	"context"
	"fmt"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	authv1 "k8s.io/api/authorization/v1"
	"sigs.k8s.io/yaml"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// PreviewArgs is shared by install + upgrade preview. The caller fills
// every field; ReleaseName is the proposed name (install) or the
// existing release's name (upgrade). For upgrade, Namespace must
// match the existing release's namespace.
type PreviewArgs struct {
	Ref         string
	ChartName   string // see FetchValuesArgs — required for HTTP, ignored for OCI
	Version     string
	Namespace   string
	ReleaseName string
	// Values is the verbatim values.yaml content the operator authored.
	// Empty string = use the chart's default values. Helm's loader
	// merges chart defaults underneath, so this is overrides only.
	Values string
}

// PreviewResult is the response shape returned by both preview modes.
// Diff is nil for install mode (nothing to compare against). Denied
// is nil when every manifest passed the SAR pre-flight; non-nil
// when one or more kinds would be rejected by the apiserver.
type PreviewResult struct {
	Manifests []HelmManifestObject `json:"manifests"`
	Diff      *HelmDiff            `json:"diff,omitempty"`
	Denied    []PreviewDenial      `json:"denied,omitempty"`
}

// PreviewDenial is one entry in PreviewResult.Denied. Fields mirror
// authv1.ResourceAttributes plus the Reason from CheckSAR's response
// (e.g. "denied" / "auth_failed" / a free-form apiserver string).
type PreviewDenial struct {
	Group     string `json:"group,omitempty"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	Verb      string `json:"verb"`
	Reason    string `json:"reason"`
}

// previewMode discriminates install-vs-upgrade in the shared
// previewRenderFn seam. Kept package-private since the public API
// is the two PreviewHelm{Install,Upgrade} entry points.
type previewMode int

const (
	modeInstall previewMode = iota
	modeUpgrade
)

// previewRenderFn is the test seam for the helm SDK render call.
// Production code wires defaultPreviewRender (which does the actual
// pkg/action call); tests substitute a fake that returns canned
// manifest YAML so the test path doesn't need a live action.Configuration
// or a real K8s endpoint.
var previewRenderFn = defaultPreviewRender

// previewSARFn is the test seam for the per-manifest SelfSubjectAccessReview.
// Production wires CheckSAR; tests substitute to drive the denial
// branches without an apiserver.
var previewSARFn = CheckSAR

// previewGetCurrentReleaseFn is the test seam for fetching the
// currently-deployed release manifest in upgrade mode (used as the
// "from" side of the diff). Production wires a small wrapper around
// GetHelmRelease; tests substitute to plant a known starting state.
var previewGetCurrentReleaseFn = defaultPreviewGetCurrentRelease

// fetchAndLoadChartFn is the test seam for chart fetch + parse +
// deps validation. Production wires the real network-aware impl
// below; tests substitute to skip the OCI/HTTP round-trip.
var fetchAndLoadChartFn = fetchAndLoadChart

// defaultPreviewRender runs the actual helm SDK dry-run for install
// or upgrade. Returns the rendered manifest YAML.
func defaultPreviewRender(ctx context.Context, p credentials.Provider, c clusters.Cluster, chrt *chart.Chart, vals chartutil.Values, args PreviewArgs, mode previewMode) (string, error) {
	cfg, err := buildHelmActionConfig(ctx, p, c, args.Namespace)
	if err != nil {
		return "", err
	}
	switch mode {
	case modeInstall:
		inst := action.NewInstall(cfg)
		inst.DryRun = true
		inst.ClientOnly = true // skip apiserver capability resolution; chart's kubeVersion check still runs locally
		inst.ReleaseName = args.ReleaseName
		inst.Namespace = args.Namespace
		inst.IncludeCRDs = true
		rel, err := inst.RunWithContext(ctx, chrt, vals)
		if err != nil {
			return "", fmt.Errorf("helm install dry-run: %w", err)
		}
		return rel.Manifest, nil
	case modeUpgrade:
		upg := action.NewUpgrade(cfg)
		upg.DryRun = true
		upg.Namespace = args.Namespace
		rel, err := upg.RunWithContext(ctx, args.ReleaseName, chrt, vals)
		if err != nil {
			return "", fmt.Errorf("helm upgrade dry-run: %w", err)
		}
		return rel.Manifest, nil
	}
	return "", fmt.Errorf("unknown preview mode %d", mode)
}

// defaultPreviewGetCurrentRelease wraps GetHelmRelease for the test
// seam. Returns the manifest YAML of the latest deployed revision
// (rev=0 means "latest"). Errors bubble up; the caller decides
// whether a missing release is fatal (it isn't — we still surface the
// preview manifest list, just with a nil diff).
func defaultPreviewGetCurrentRelease(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace, name string) (string, error) {
	rel, err := GetHelmRelease(ctx, p, c, namespace, name, 0, defaultDetailMaxBytes)
	if err != nil {
		return "", err
	}
	if rel == nil {
		return "", nil
	}
	return rel.ManifestYAML, nil
}

// PreviewHelmInstall renders the manifests helm would apply for a
// fresh install, runs the RBAC pre-flight (verb=create), and returns
// the result. Diff is always nil for install — there's no live
// release to compare against.
func PreviewHelmInstall(ctx context.Context, p credentials.Provider, c clusters.Cluster, args PreviewArgs) (*PreviewResult, error) {
	chrt, err := fetchAndLoadChartFn(ctx, args)
	if err != nil {
		return nil, err
	}
	vals, err := parseValuesYAML(args.Values)
	if err != nil {
		return nil, fmt.Errorf("parse values: %w", err)
	}
	manifestYAML, err := previewRenderFn(ctx, p, c, chrt, vals, args, modeInstall)
	if err != nil {
		return nil, err
	}
	manifests := parseManifestObjects(manifestYAML, args.Namespace)
	denied, err := preflightSARs(ctx, p, c, manifests, "create")
	if err != nil {
		return nil, fmt.Errorf("preflight: %w", err)
	}
	return &PreviewResult{Manifests: manifests, Denied: denied}, nil
}

// PreviewHelmUpgrade renders the manifests helm would apply for an
// upgrade against the existing release, computes a semantic diff
// against the current release manifest, and runs the RBAC pre-flight
// (verb=patch — apiserver accepts patch on objects that allow patch
// even when the user lacks update; the dry-run preview's job is
// "would the apiserver accept this," not "could the operator do this
// every possible way").
func PreviewHelmUpgrade(ctx context.Context, p credentials.Provider, c clusters.Cluster, args PreviewArgs) (*PreviewResult, error) {
	chrt, err := fetchAndLoadChartFn(ctx, args)
	if err != nil {
		return nil, err
	}
	vals, err := parseValuesYAML(args.Values)
	if err != nil {
		return nil, fmt.Errorf("parse values: %w", err)
	}
	manifestYAML, err := previewRenderFn(ctx, p, c, chrt, vals, args, modeUpgrade)
	if err != nil {
		return nil, err
	}
	manifests := parseManifestObjects(manifestYAML, args.Namespace)

	// Diff against the live release manifest. Diff failures are
	// non-fatal — we still return the manifest list for the SPA's
	// monaco view, just with a nil diff.
	var diff *HelmDiff
	currentYAML, currentErr := previewGetCurrentReleaseFn(ctx, p, c, args.Namespace, args.ReleaseName)
	if currentErr == nil && currentYAML != "" {
		diff, _ = DiffHelmManifests(ctx, currentYAML, manifestYAML)
	}

	denied, err := preflightSARs(ctx, p, c, manifests, "patch")
	if err != nil {
		return nil, fmt.Errorf("preflight: %w", err)
	}
	return &PreviewResult{Manifests: manifests, Diff: diff, Denied: denied}, nil
}

// fetchAndLoadChart pulls the tarball from OCI/HTTP, hands it to
// helm's loader, and rejects sub-chart deps. Sub-charts are out of
// scope for v1.1 (same constraint enforced by unpackChart on the
// chart-fetch path); we re-check here because helm's loader happily
// recurses into <chart>/charts/ if present, which would silently
// expand scope.
func fetchAndLoadChart(ctx context.Context, args PreviewArgs) (*chart.Chart, error) {
	tarball, err := FetchChartArchive(ctx, FetchValuesArgs{
		Ref:       args.Ref,
		ChartName: args.ChartName,
		Version:   args.Version,
	})
	if err != nil {
		return nil, fmt.Errorf("fetch chart archive: %w", err)
	}
	chrt, err := loader.LoadArchive(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("%w: load: %v", ErrChartInvalid, err)
	}
	if err := validateChart(chrt); err != nil {
		return nil, err
	}
	return chrt, nil
}

// validateChart enforces the v1.1 sub-chart-rejection contract on a
// loaded *chart.Chart. Sub-charts are rejected for parity with the
// chart-fetch endpoint (#106) — supporting them without per-cluster
// scoped credential resolution would be a partial implementation,
// and the failure mode of "silent partial install" is worse than
// the failure mode of "we said no upfront."
//
// Two checks: declared dependencies in Chart.yaml, AND bundled
// sub-charts under <name>/charts/ that aren't declared. Helm's
// loader populates chrt.Dependencies() for the latter case; we
// catch both.
func validateChart(chrt *chart.Chart) error {
	if chrt == nil {
		return fmt.Errorf("%w: nil chart", ErrChartInvalid)
	}
	if chrt.Metadata != nil && len(chrt.Metadata.Dependencies) > 0 {
		return fmt.Errorf("%w: chart declares %d dependencies", ErrChartUnsupportedDeps, len(chrt.Metadata.Dependencies))
	}
	if len(chrt.Dependencies()) > 0 {
		return fmt.Errorf("%w: chart bundles sub-charts under charts/", ErrChartUnsupportedDeps)
	}
	return nil
}

// parseValuesYAML decodes operator-supplied values.yaml into the
// map shape helm's action.Run expects. Empty input means "no
// overrides; use chart defaults" — return an empty map so chartutil
// merges normally.
func parseValuesYAML(raw string) (chartutil.Values, error) {
	if raw == "" {
		return chartutil.Values{}, nil
	}
	out := chartutil.Values{}
	if err := yaml.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// preflightSARs runs a SelfSubjectAccessReview against the impersonated
// user for every manifest's (verb, GVR, ns, name) tuple. Returns the
// list of denied tuples (nil when every check passed). Verb is the
// primary verb for the operation — "create" for install, "patch" for
// upgrade. We intentionally do NOT also test "update" on upgrade
// because the apiserver accepts patch on objects that allow patch
// even when the user lacks update; the dry-run preview's job is
// "would the apiserver accept this", not "could the operator do this
// every possible way."
//
// Errors from CheckSAR (network / auth path failures) abort the
// loop and bubble up — those are infrastructure failures, not policy
// denials, and silently dropping them would mislead the SPA into
// rendering "everything's fine" when it isn't.
func preflightSARs(ctx context.Context, p credentials.Provider, c clusters.Cluster, manifests []HelmManifestObject, verb string) ([]PreviewDenial, error) {
	var denied []PreviewDenial
	for _, m := range manifests {
		group, resource := groupResourceFor(m)
		attr := authv1.ResourceAttributes{
			Namespace: m.Namespace,
			Verb:      verb,
			Group:     group,
			Resource:  resource,
			Name:      m.Name,
		}
		allowed, reason, err := previewSARFn(ctx, p, c, attr)
		if err != nil {
			return nil, fmt.Errorf("SAR for %s/%s: %w", resource, m.Name, err)
		}
		if !allowed {
			denied = append(denied, PreviewDenial{
				Group:     group,
				Resource:  resource,
				Namespace: m.Namespace,
				Name:      m.Name,
				Verb:      verb,
				Reason:    reason,
			})
		}
	}
	return denied, nil
}

// groupResourceFor splits a manifest's apiVersion into (group, resource).
// HelmManifestObject only carries APIVersion + Kind, so we derive the
// resource name by lowercasing-and-pluralizing the kind. This matches
// the most common K8s naming convention for built-in kinds; CRDs follow
// the same convention by spec. Edge cases (Endpoints with no plural,
// Status with irregular plural) are handled by the apiserver's SAR
// resource resolution — it accepts the singular form too, so even when
// our derivation is "wrong" the SAR still lands.
//
// Group derivation: APIVersion shapes are "v1" (core, group=""), "apps/v1",
// "rbac.authorization.k8s.io/v1", etc. We split on "/" and take the
// first segment when there are two; "v1" alone → empty group.
func groupResourceFor(m HelmManifestObject) (group, resource string) {
	if i := indexOfSlash(m.APIVersion); i >= 0 {
		group = m.APIVersion[:i]
	}
	resource = pluralize(m.Kind)
	return
}

func indexOfSlash(s string) int {
	for i, r := range s {
		if r == '/' {
			return i
		}
	}
	return -1
}

// pluralize is a minimal English plural — enough for the K8s built-in
// kinds. The apiserver's SAR endpoint accepts both forms; we lean
// toward plural since that's the canonical resource name in the API
// machinery. Lowercases first so "Pod" → "pods", "Ingress" → "ingresses".
func pluralize(kind string) string {
	if kind == "" {
		return ""
	}
	lower := toLower(kind)
	switch {
	case endsWith(lower, "s"), endsWith(lower, "x"), endsWith(lower, "ch"), endsWith(lower, "sh"):
		return lower + "es"
	case endsWith(lower, "y") && !isVowel(lower[len(lower)-2]):
		return lower[:len(lower)-1] + "ies"
	default:
		return lower + "s"
	}
}

func toLower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

// defaultDetailMaxBytes is the cap GetHelmRelease takes for manifest
// payload size. The handler-level config has its own value; when
// PreviewHelmUpgrade calls GetHelmRelease internally we use a
// generous cap (1 MiB) so very large releases don't get their diff
// truncated unexpectedly. The dry-run path doesn't have a way to
// surface "diff was truncated" to the SPA, so we err on the side of
// "include everything."
const defaultDetailMaxBytes = 1 << 20
