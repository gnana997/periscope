package k8s

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
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
var chartFetchClient = &http.Client{
	Timeout: chartFetchTimeout,
	Transport: &http.Transport{
		// Connect timeout via DialContext keeps a slow registry
		// from eating the full chartFetchTimeout on dial alone.
		ResponseHeaderTimeout: 5 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   2,
	},
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: bad URL: %v", ErrChartUnsupportedRef, err)
	}
	req.Header.Set("Accept", "application/x-yaml, application/yaml, application/octet-stream, */*")
	req.Header.Set("User-Agent", "periscope-chart-fetcher")
	resp, err := chartFetchClient.Do(req)
	if err != nil {
		return nil, classifyHTTPErr(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
		// fall through
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrChartNotFound, target)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
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
