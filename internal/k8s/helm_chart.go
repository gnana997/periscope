// Package k8s — Helm chart fetch (issue #73).
//
// Reads chart metadata + values + values.schema.json from a chart-repo
// URL or an OCI ref, returning a typed struct the SPA can render in
// the install dialog. Supports two ref shapes:
//
//	HTTP/HTTPS repo:  https://charts.bitnami.com/bitnami  (with chart=wordpress)
//	OCI:              oci://ghcr.io/gnana997/charts/periscope
//
// Two distinct entry points so caches can key independently:
//
//	FetchChartVersions — list available versions for a ref
//	FetchChartValues   — full Chart.yaml projection + values + schema
//	                     for a (ref, version) pair
//
// Why no helm SDK: same rationale as the v1.0 release decoder
// (see helm.go preamble) — own the small decoder, isolate from the
// SDK's transitive dep churn. Chart format is stable: tar.gz with
// Chart.yaml at the root, parseable in ~80 LoC. OCI dial uses
// oras-go/v2 (kubectl-free; the same lib helm itself uses).
//
// v1.1 supports unauthenticated public refs only. Private OCI auth
// (ECR via Pod Identity / IRSA, registry credentials) is tracked
// separately and lands in v1.2 — the per-cluster URL scoping is
// reserved space for that credential resolution.
package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors callers (handlers + tests) match on.
var (
	ErrChartUnreachable      = errors.New("chart fetch: registry unreachable")
	ErrChartNotFound         = errors.New("chart fetch: not found")
	ErrChartUnauthorized     = errors.New("chart fetch: registry requires auth (private refs land in v1.2)")
	ErrChartNotAChart        = errors.New("chart fetch: artifact is not a Helm chart")
	ErrChartTimeout          = errors.New("chart fetch: timed out")
	ErrChartInvalid          = errors.New("chart fetch: chart contents are malformed")
	ErrChartUnsupportedDeps  = errors.New("chart fetch: chart has sub-chart dependencies (flat charts only in v1.1)")
	ErrChartUnsupportedRef   = errors.New("chart fetch: ref scheme not supported (use https:// or oci://)")
	ErrChartVersionNotFound  = errors.New("chart fetch: requested version not found in this ref")
)

// ChartMaintainer mirrors the Helm Chart.yaml maintainers entry.
type ChartMaintainer struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// ChartDep is one entry in Chart.yaml's `dependencies` list. v1.1
// rejects charts with non-empty dependencies, but the SPA renders
// the list so operators understand WHY their chart was rejected.
type ChartDep struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	Repository string `json:"repository,omitempty"`
	Alias      string `json:"alias,omitempty"`
	Condition  string `json:"condition,omitempty"`
}

// ChartMeta is the typed projection of Chart.yaml. Same field set Helm
// itself uses for v2 — operators can paste any standard chart and the
// SPA gets enough metadata to render the install dialog header,
// kubeVersion warning, deps list, and so on.
type ChartMeta struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	APIVersion   string            `json:"apiVersion"` // "v1" | "v2"
	AppVersion   string            `json:"appVersion,omitempty"`
	Description  string            `json:"description,omitempty"`
	KubeVersion  string            `json:"kubeVersion,omitempty"` // semver constraint, e.g. ">=1.24"
	Type         string            `json:"type,omitempty"`        // "application" | "library"
	Keywords     []string          `json:"keywords,omitempty"`
	Home         string            `json:"home,omitempty"`
	Sources      []string          `json:"sources,omitempty"`
	Maintainers  []ChartMaintainer `json:"maintainers,omitempty"`
	Icon         string            `json:"icon,omitempty"`
	Annotations  map[string]string `json:"annotations,omitempty"`
	Dependencies []ChartDep        `json:"dependencies,omitempty"`
}

// ChartFetchResult is what FetchChartValues returns: the typed
// metadata above plus the verbatim values.yaml + values.schema.json
// (when present).
type ChartFetchResult struct {
	Meta   ChartMeta              `json:"meta"`
	Values string                 `json:"values"`           // verbatim values.yaml
	Schema map[string]interface{} `json:"schema,omitempty"` // values.schema.json (decoded)
}

// ChartVersionsResult is what FetchChartVersions returns: ordered
// (newest-first by semver) list of versions for the ref. Capped at
// MaxVersionsReturned so the SPA picker stays usable.
type ChartVersionsResult struct {
	Ref      string   `json:"ref"`
	Versions []string `json:"versions"`
	Latest   string   `json:"latest,omitempty"`
}

// MaxVersionsReturned bounds the version list. Operators don't need
// to scroll through 500 nightly tags. 50 covers any realistic
// release cadence (one per week for ~1y is 52).
const MaxVersionsReturned = 50

// MaxChartBytes caps the chart tarball size we'll unpack. Real-world
// Helm charts are 10-200 KB compressed; 5 MB is a generous ceiling
// that catches both legitimate large charts and obviously-malicious
// payloads.
const MaxChartBytes = 5 * 1024 * 1024

// FetchVersionsArgs identifies the ref to introspect.
type FetchVersionsArgs struct {
	// Ref is the chart repository URL (https://...) or OCI ref
	// (oci://...). For HTTP repos, ChartName must be set.
	Ref string
	// ChartName is the chart's name within the HTTP repo's index.
	// Required for HTTP refs; ignored for OCI (the chart name is
	// implicit in the ref's last path segment).
	ChartName string
}

// FetchValuesArgs identifies a specific (ref, version) to fetch.
type FetchValuesArgs struct {
	Ref       string
	ChartName string // see FetchVersionsArgs.ChartName
	Version   string // exact version to pull
}

// FetchChartVersions returns the ordered list of available versions
// for the given ref. Errors are typed sentinels — handlers map them
// to HTTP statuses via classifyChartFetchErr in the handler.
func FetchChartVersions(ctx context.Context, args FetchVersionsArgs) (ChartVersionsResult, error) {
	scheme, err := schemeFor(args.Ref)
	if err != nil {
		return ChartVersionsResult{}, err
	}
	switch scheme {
	case "oci":
		return fetchOCIVersions(ctx, args.Ref)
	case "http", "https":
		return fetchHTTPVersions(ctx, args.Ref, args.ChartName)
	}
	return ChartVersionsResult{}, ErrChartUnsupportedRef
}

// FetchChartValues returns the chart's full metadata projection plus
// values.yaml + values.schema.json for the given (ref, version).
// Rejects charts with non-empty dependencies — that's a v1.1 scope
// limitation, surfaced as ErrChartUnsupportedDeps.
func FetchChartValues(ctx context.Context, args FetchValuesArgs) (ChartFetchResult, error) {
	if args.Version == "" {
		return ChartFetchResult{}, fmt.Errorf("FetchChartValues: version is required")
	}
	scheme, err := schemeFor(args.Ref)
	if err != nil {
		return ChartFetchResult{}, err
	}
	var tarball []byte
	switch scheme {
	case "oci":
		tarball, err = fetchOCIChartTarball(ctx, args.Ref, args.Version)
	case "http", "https":
		tarball, err = fetchHTTPChartTarball(ctx, args.Ref, args.ChartName, args.Version)
	default:
		return ChartFetchResult{}, ErrChartUnsupportedRef
	}
	if err != nil {
		return ChartFetchResult{}, err
	}
	return unpackChart(tarball)
}

// schemeFor classifies the ref. Helm clients accept both bare URLs
// (treated as https://) and explicit oci:// — we require explicit
// schemes here because the SPA's input field is operator-supplied
// and we'd rather reject a typo than silently pick a default.
func schemeFor(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ErrChartUnsupportedRef
	}
	switch {
	case strings.HasPrefix(ref, "oci://"):
		return "oci", nil
	case strings.HasPrefix(ref, "https://"):
		return "https", nil
	case strings.HasPrefix(ref, "http://"):
		return "http", nil
	}
	return "", ErrChartUnsupportedRef
}
