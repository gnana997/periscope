package k8s

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"k8s.io/client-go/rest"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// OpenAPIArgs identifies a sub-path under the cluster's /openapi/v3.
// Path is the relative segment after /openapi/v3 — e.g. "" for the
// discovery doc, "api/v1" for core, "apis/apps/v1" for apps,
// "apis/argoproj.io/v1alpha1" for an ArgoCD CRD group.
type OpenAPIArgs struct {
	Cluster clusters.Cluster
	Path    string
}

// OpenAPIResult is the cached JSON body + Content-Type. We always
// request application/json from the apiserver; protobuf is supported
// by the apiserver but adds bundle complexity to the SPA for no win.
type OpenAPIResult struct {
	Body        []byte
	ContentType string
}

// openAPICache is the package-level cache. The schema is identity-
// independent (same response for every authenticated caller), so we
// cache globally per (cluster, path). The first user pays the round
// trip; everyone after gets the cache hit.
//
// Lifetime: the schema only changes when the cluster upgrades. Caching
// for the backend process lifetime is the right tradeoff for v1; a
// future improvement is a TTL or admin-triggered flush. The cache
// bound is the apiserver's own GV count (typically 30–50, ~200 with
// many CRDs) × ~200 KB per group ≈ at most ~50 MB per cluster. Fine.
var openAPICache sync.Map // openAPICacheKey → []byte

type openAPICacheKey struct {
	cluster string
	path    string
}

// fetchOpenAPI is swapped by tests for an httptest-backed implementation.
// Production path: build a rest.Config with the user's impersonation
// (so the apiserver authenticates the first uncached request), build an
// HTTP client from it (TLS + bearer token + impersonation headers come
// for free), and GET /openapi/v3/{path}.
var fetchOpenAPI = func(ctx context.Context, p credentials.Provider, c clusters.Cluster, path string) ([]byte, error) {
	cfg, err := buildRestConfig(ctx, p, c)
	if err != nil {
		return nil, err
	}
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("openapi: build http client: %w", err)
	}
	url := strings.TrimRight(cfg.Host, "/") + "/openapi/v3"
	if path != "" {
		url += "/" + strings.TrimLeft(path, "/")
	}
	return doOpenAPIFetch(ctx, httpClient, url)
}

func doOpenAPIFetch(ctx context.Context, httpClient *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Apiserver error bodies are usually a JSON Status, but cap the
		// read so a misbehaving server can't stream megabytes into our log.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("openapi: apiserver returned %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

// GetOpenAPI fetches /openapi/v3/{path} from the cluster's apiserver
// (cached). Returns JSON bytes ready to forward to the SPA.
//
// Authorisation: the first (uncached) request goes through the user's
// impersonated rest.Config — the apiserver enforces the
// system:public-info-viewer ClusterRole binding (granted to
// system:authenticated by default). If that binding has been removed
// in a hardened cluster, the first request fails with 401/403; the
// SPA degrades gracefully (no schema → no autocomplete → editor still
// works otherwise).
func GetOpenAPI(ctx context.Context, p credentials.Provider, args OpenAPIArgs) (OpenAPIResult, error) {
	if err := validateOpenAPIPath(args.Path); err != nil {
		return OpenAPIResult{}, err
	}
	key := openAPICacheKey{cluster: args.Cluster.Name, path: args.Path}
	if v, ok := openAPICache.Load(key); ok {
		return OpenAPIResult{Body: v.([]byte), ContentType: "application/json"}, nil
	}

	body, err := fetchOpenAPI(ctx, p, args.Cluster, args.Path)
	if err != nil {
		return OpenAPIResult{}, err
	}

	openAPICache.Store(key, body)
	return OpenAPIResult{Body: body, ContentType: "application/json"}, nil
}

// validateOpenAPIPath rejects paths with traversal sequences or query
// fragments. Defence in depth — the apiserver would reject most of
// these too, but we don't want our cache to grow unbounded with
// malformed keys, and we don't want to forward query params the
// caller might be trying to smuggle through.
func validateOpenAPIPath(path string) error {
	if path == "" {
		return nil
	}
	if strings.Contains(path, "..") || strings.ContainsAny(path, "?#") {
		return fmt.Errorf("openapi: invalid path %q", path)
	}
	return nil
}

// resetOpenAPICacheForTest is a test-only seam for cache state hygiene.
func resetOpenAPICacheForTest() {
	openAPICache = sync.Map{}
}
