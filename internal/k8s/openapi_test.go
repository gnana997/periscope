package k8s

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// withFakeAPIServer spins up an httptest server that serves a couple
// of canned OpenAPI v3 paths and tracks how many times each was hit.
// Returns a teardown that swaps fetchOpenAPI back and resets the cache.
func withFakeAPIServer(t *testing.T, hitCount *int32) func() {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/openapi/v3", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hitCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"paths":{"api/v1":{"serverRelativeURL":"/openapi/v3/api/v1"}}}`))
	})
	mux.HandleFunc("/openapi/v3/apis/apps/v1", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hitCount, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"openapi":"3.0.0","components":{"schemas":{"io.k8s.api.apps.v1.Deployment":{}}}}`))
	})
	mux.HandleFunc("/openapi/v3/apis/missing/v1", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(hitCount, 1)
		http.Error(w, `{"kind":"Status","status":"Failure","code":404,"reason":"NotFound"}`, http.StatusNotFound)
	})

	server := httptest.NewServer(mux)

	origFetch := fetchOpenAPI
	fetchOpenAPI = func(ctx context.Context, _ credentials.Provider, _ clusters.Cluster, path string) ([]byte, error) {
		url := server.URL + "/openapi/v3"
		if path != "" {
			url += "/" + path
		}
		return doOpenAPIFetch(ctx, server.Client(), url)
	}

	return func() {
		fetchOpenAPI = origFetch
		server.Close()
		resetOpenAPICacheForTest()
	}
}

func TestGetOpenAPI_DiscoveryDoc(t *testing.T) {
	var hits int32
	defer withFakeAPIServer(t, &hits)()

	got, err := GetOpenAPI(context.Background(), stubProvider{}, OpenAPIArgs{
		Cluster: clusters.Cluster{Name: "test"},
		Path:    "",
	})
	if err != nil {
		t.Fatalf("GetOpenAPI: %v", err)
	}
	if got.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want application/json", got.ContentType)
	}
	if !contains(got.Body, "paths") {
		t.Errorf("body missing 'paths' key: %s", got.Body)
	}
	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Errorf("apiserver hit %d times on first call, want 1", h)
	}
}

func TestGetOpenAPI_CacheHit(t *testing.T) {
	var hits int32
	defer withFakeAPIServer(t, &hits)()

	args := OpenAPIArgs{Cluster: clusters.Cluster{Name: "test"}, Path: "apis/apps/v1"}

	// First call — cache miss
	if _, err := GetOpenAPI(context.Background(), stubProvider{}, args); err != nil {
		t.Fatalf("GetOpenAPI #1: %v", err)
	}
	// Second call — cache hit, must not touch the apiserver
	if _, err := GetOpenAPI(context.Background(), stubProvider{}, args); err != nil {
		t.Fatalf("GetOpenAPI #2: %v", err)
	}
	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Errorf("apiserver hits = %d after 2 calls, want 1 (cache should serve #2)", h)
	}
}

func TestGetOpenAPI_DifferentPathsCacheSeparately(t *testing.T) {
	var hits int32
	defer withFakeAPIServer(t, &hits)()

	c := clusters.Cluster{Name: "test"}
	if _, err := GetOpenAPI(context.Background(), stubProvider{}, OpenAPIArgs{Cluster: c, Path: ""}); err != nil {
		t.Fatalf("GetOpenAPI discovery: %v", err)
	}
	if _, err := GetOpenAPI(context.Background(), stubProvider{}, OpenAPIArgs{Cluster: c, Path: "apis/apps/v1"}); err != nil {
		t.Fatalf("GetOpenAPI apps/v1: %v", err)
	}
	if h := atomic.LoadInt32(&hits); h != 2 {
		t.Errorf("apiserver hits = %d, want 2 (each path is its own cache key)", h)
	}
}

func TestGetOpenAPI_CrossUserCacheReuse(t *testing.T) {
	// Different impersonated users should still share the cache —
	// schema is identity-independent.
	var hits int32
	defer withFakeAPIServer(t, &hits)()

	c := clusters.Cluster{Name: "test"}
	if _, err := GetOpenAPI(context.Background(), stubProvider{}, OpenAPIArgs{Cluster: c, Path: "apis/apps/v1"}); err != nil {
		t.Fatalf("user A: %v", err)
	}
	if _, err := GetOpenAPI(context.Background(), stubProvider{}, OpenAPIArgs{Cluster: c, Path: "apis/apps/v1"}); err != nil {
		t.Fatalf("user B: %v", err)
	}
	if h := atomic.LoadInt32(&hits); h != 1 {
		t.Errorf("apiserver hits = %d, want 1 across two users (cache is identity-independent)", h)
	}
}

func TestGetOpenAPI_ApiserverError(t *testing.T) {
	var hits int32
	defer withFakeAPIServer(t, &hits)()

	_, err := GetOpenAPI(context.Background(), stubProvider{}, OpenAPIArgs{
		Cluster: clusters.Cluster{Name: "test"},
		Path:    "apis/missing/v1",
	})
	if err == nil {
		t.Fatal("expected error from 404 apiserver response, got nil")
	}
	// Errored responses must NOT be cached — a transient failure should
	// not poison the cache.
	if _, err := GetOpenAPI(context.Background(), stubProvider{}, OpenAPIArgs{
		Cluster: clusters.Cluster{Name: "test"},
		Path:    "apis/missing/v1",
	}); err == nil {
		t.Fatal("expected error on retry, got nil (was the error cached?)")
	}
	if h := atomic.LoadInt32(&hits); h != 2 {
		t.Errorf("apiserver hits = %d after 2 errored calls, want 2 (errors not cached)", h)
	}
}

func TestValidateOpenAPIPath(t *testing.T) {
	cases := []struct {
		path    string
		wantErr bool
	}{
		{"", false},
		{"api/v1", false},
		{"apis/apps/v1", false},
		{"apis/argoproj.io/v1alpha1", false},
		{"../etc/passwd", true},
		{"api/v1?foo=bar", true},
		{"api/v1#fragment", true},
	}
	for _, tc := range cases {
		err := validateOpenAPIPath(tc.path)
		if tc.wantErr && err == nil {
			t.Errorf("path %q: expected error, got nil", tc.path)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("path %q: unexpected error: %v", tc.path, err)
		}
	}
}

func contains(body []byte, substr string) bool {
	if len(body) == 0 || len(substr) == 0 {
		return false
	}
	for i := 0; i+len(substr) <= len(body); i++ {
		if string(body[i:i+len(substr)]) == substr {
			return true
		}
	}
	return false
}
