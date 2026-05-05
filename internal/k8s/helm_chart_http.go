package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// chartFetchHostRe gates the Host field of a parsed chart-fetch URL.
// Matches RFC-1123 DNS names, IPv4 dotted literals, and bracketed
// IPv6, each with an optional :port. Rejecting on no-match gives
// CodeQL go/request-forgery a recognizable regex barrier guard
// between the operator-supplied ref and net/http's request
// constructor — the dial-time SSRF guard remains the runtime
// authority on which destinations actually connect.
var chartFetchHostRe = regexp.MustCompile(
	`^(` +
		`([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(\.([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?))*` +
		`|([0-9]{1,3}(\.[0-9]{1,3}){3})` +
		`|(\[[0-9a-fA-F:.]+\])` +
		`)(:[0-9]{1,5})?$`,
)

// HTTP chart-repo client. A chart repo is "any HTTP server that
// serves an index.yaml describing the charts hosted there." See
// https://helm.sh/docs/topics/chart_repository/ for the format.
//
// We fetch index.yaml ourselves rather than pulling in
// helm.sh/helm/v3/pkg/repo for the same kubectl-pull-in concern as
// the v1.0 release decoder. The schema we decode against is a
// minimal subset of helm's IndexFile — apiVersion, entries map,
// and per-entry version/url/digest. Everything else we ignore.

// chartFetchTimeout is the overall ceiling on a registry round-trip.
// Operators paste a typo, the registry hangs — we'd rather return a
// clean 504 than tie up the goroutine indefinitely.
const chartFetchTimeout = 10 * time.Second

// chartFetchClient is shared across calls; the underlying transport
// pools connections so back-to-back fetches reuse TCP. Timeout caps
// the entire round-trip including TLS handshake + body read.
//
// SSRF defense: the dialer's Control callback runs after DNS
// resolution and before connect, so dialControlSSRFGuard can refuse
// connections to private / link-local / IMDS addresses. Operators
// can opt-in to private IPs via the env flag; see helm_chart_ssrf.go.
var chartFetchClient = &http.Client{
	Timeout:   chartFetchTimeout,
	Transport: newChartFetchTransport(),
}

// newChartFetchTransport builds the SSRF-guarded transport used by
// both the HTTP repo path and the OCI client. Extracted so both can
// share the same connection pool sizing + safety properties.
func newChartFetchTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   dialControlSSRFGuard,
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   2,
	}
}

// indexFile is our minimal projection of helm's repo IndexFile. We
// only read the fields the SPA needs.
type indexFile struct {
	APIVersion string                  `json:"apiVersion"`
	Entries    map[string][]indexEntry `json:"entries"`
}

type indexEntry struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	URLs    []string `json:"urls"`
	Digest  string   `json:"digest,omitempty"`
}

// fetchHTTPVersions GETs the repo's index.yaml, finds the entries
// for chartName, and returns the version list ordered newest-first
// by semver.
func fetchHTTPVersions(ctx context.Context, repoURL, chartName string) (ChartVersionsResult, error) {
	if chartName == "" {
		return ChartVersionsResult{}, fmt.Errorf("%w: chart name is required for HTTP repos", ErrChartUnsupportedRef)
	}
	idx, err := fetchHTTPIndex(ctx, repoURL)
	if err != nil {
		return ChartVersionsResult{}, err
	}
	entries, ok := idx.Entries[chartName]
	if !ok || len(entries) == 0 {
		return ChartVersionsResult{}, fmt.Errorf("%w: chart %q not in repo index", ErrChartNotFound, chartName)
	}
	versions := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Version != "" {
			versions = append(versions, e.Version)
		}
	}
	versions = sortAndCapVersions(versions)
	out := ChartVersionsResult{
		Ref:      repoURL,
		Versions: versions,
	}
	if len(versions) > 0 {
		out.Latest = versions[0]
	}
	return out, nil
}

// fetchHTTPChartTarball pulls the tarball for (chartName, version)
// from the repo. Resolution: read index.yaml → find matching entry
// → resolve URL (relative URLs are joined against the repo URL) →
// GET tarball.
func fetchHTTPChartTarball(ctx context.Context, repoURL, chartName, version string) ([]byte, error) {
	if chartName == "" {
		return nil, fmt.Errorf("%w: chart name is required for HTTP repos", ErrChartUnsupportedRef)
	}
	idx, err := fetchHTTPIndex(ctx, repoURL)
	if err != nil {
		return nil, err
	}
	entries, ok := idx.Entries[chartName]
	if !ok {
		return nil, fmt.Errorf("%w: chart %q not in repo index", ErrChartNotFound, chartName)
	}
	var tarballURL string
	for _, e := range entries {
		if e.Version == version && len(e.URLs) > 0 {
			tarballURL = e.URLs[0]
			break
		}
	}
	if tarballURL == "" {
		return nil, fmt.Errorf("%w: %s@%s", ErrChartVersionNotFound, chartName, version)
	}
	// Resolve relative URLs against the repo base.
	if !strings.Contains(tarballURL, "://") {
		base, err := url.Parse(repoURL)
		if err != nil {
			return nil, fmt.Errorf("%w: bad repo URL: %v", ErrChartUnsupportedRef, err)
		}
		rel, err := url.Parse(tarballURL)
		if err != nil {
			return nil, fmt.Errorf("%w: bad tarball URL %q: %v", ErrChartInvalid, tarballURL, err)
		}
		tarballURL = base.ResolveReference(rel).String()
	}
	return httpGet(ctx, tarballURL, MaxChartBytes)
}

func fetchHTTPIndex(ctx context.Context, repoURL string) (*indexFile, error) {
	idxURL := strings.TrimRight(repoURL, "/") + "/index.yaml"
	body, err := httpGet(ctx, idxURL, 16*1024*1024) // index can be large for big repos
	if err != nil {
		return nil, err
	}
	var idx indexFile
	if err := yaml.Unmarshal(body, &idx); err != nil {
		return nil, fmt.Errorf("%w: index.yaml: %v", ErrChartInvalid, err)
	}
	if idx.Entries == nil {
		return nil, fmt.Errorf("%w: index.yaml has no entries", ErrChartInvalid)
	}
	return &idx, nil
}

func httpGet(ctx context.Context, target string, maxBytes int) ([]byte, error) {
	// Sanitize the URL upfront. Beyond what the dial-time SSRF guard
	// catches at the network layer, this rejects malformed URLs and
	// non-http(s) schemes before any I/O fires — and gives static
	// analysis (CodeQL go/request-forgery) a clear sanitizer barrier
	// between the operator-supplied ref string and net/http's request
	// constructor. The reconstructed URL is functionally identical to
	// `target` but flows from a typed *url.URL whose scheme has been
	// explicitly validated against a finite set.
	safeURL, err := sanitizeChartFetchURL(target)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, safeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: bad URL: %v", ErrChartUnsupportedRef, err)
	}
	req.Header.Set("Accept", "application/x-yaml, application/yaml, application/octet-stream, */*")
	req.Header.Set("User-Agent", "periscope-chart-fetcher")
	resp, err := chartFetchClient.Do(req)
	if err != nil {
		return nil, classifyHTTPErr(err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrChartNotFound, target)
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrChartUnauthorized
	default:
		return nil, fmt.Errorf("%w: %s returned %d", ErrChartUnreachable, target, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrChartUnreachable, err)
	}
	if len(body) > maxBytes {
		return nil, fmt.Errorf("%w: response exceeds %d bytes", ErrChartInvalid, maxBytes)
	}
	return body, nil
}

func classifyHTTPErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrChartTimeout
	}
	// net.Error or url.Error wrapping a connection refusal — treat
	// all remaining transport-level errors as "unreachable" so the
	// SPA shows a single, clear message rather than an opaque
	// dial:tcp:127.0.0.1:443: connection refused.
	return fmt.Errorf("%w: %v", ErrChartUnreachable, err)
}

// sanitizeChartFetchURL parses target, validates the scheme and host,
// and returns a reconstructed URL string built from the typed
// *url.URL components. Two checks compose to give CodeQL's
// go/request-forgery query a sanitizer it recognizes: a scheme
// allow-list (http / https only) and a regex match on the parsed
// Host. The reconstructed URL is then assembled from those validated
// parts.
//
// Functionally equivalent to using `target` directly when the input
// is well-formed, but rejects upfront:
//   - malformed URLs (url.Parse errors)
//   - non-http(s) schemes (defends against file://, gopher://, etc.)
//   - missing host (rejects relative URLs that would otherwise
//     resolve against nothing)
//   - hosts that don't look like a DNS name / IPv4 / bracketed IPv6
//     with optional port
//
// The dial-time SSRF guard (helm_chart_ssrf.go) is still the runtime
// authority on which destinations are allowed; this function adds an
// orthogonal static-analysis-friendly sanitizer.
func sanitizeChartFetchURL(target string) (string, error) {
	parsed, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("%w: bad URL: %v", ErrChartUnsupportedRef, err)
	}
	switch parsed.Scheme {
	case "http", "https":
		// allowed
	default:
		return "", fmt.Errorf("%w: scheme %q not allowed", ErrChartUnsupportedRef, parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%w: missing host", ErrChartUnsupportedRef)
	}
	// Regex-check the host shape. This is the load-bearing barrier for
	// CodeQL go/request-forgery: branching on a regex match of the
	// untrusted Host gives the query a recognizable guard before the
	// reassembled URL flows into http.NewRequestWithContext. Functionally
	// this rejects malformed hosts (embedded paths, control chars, IDN
	// surrogates) that url.Parse would otherwise accept; the dial-time
	// SSRF guard still owns the IP-range policy.
	if !chartFetchHostRe.MatchString(parsed.Host) {
		return "", fmt.Errorf("%w: invalid host %q", ErrChartUnsupportedRef, parsed.Host)
	}
	// Reassemble from validated parts. Note: we don't preserve
	// userinfo (parsed.User) — chart fetches in v1.1 are anonymous,
	// and embedded credentials in URLs are an anti-pattern operators
	// shouldn't be using. Stripping them defends against
	// credentials-in-logs leaks if a malformed URL hits an error path
	// that includes target in the message.
	safe := &url.URL{
		Scheme:   parsed.Scheme,
		Host:     parsed.Host,
		Path:     parsed.Path,
		RawQuery: parsed.RawQuery,
	}
	return safe.String(), nil
}
