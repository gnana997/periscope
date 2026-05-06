package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// chartFixtureRegistry brings up an httptest server that serves an
// index.yaml + a chart tarball, returns its URL. Tests stub the
// chart ref to this URL so the handler talks to a real (in-memory)
// chart repo.
func chartFixtureRegistry(t *testing.T) string {
	t.Helper()
	tarball := buildHelmChartTarball(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `apiVersion: v1
entries:
  nginx:
    - name: nginx
      version: 1.0.0
      urls: ["nginx-1.0.0.tgz"]
    - name: nginx
      version: 0.9.0
      urls: ["nginx-0.9.0.tgz"]
`)
	})
	mux.HandleFunc("/nginx-1.0.0.tgz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func buildHelmChartTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name, body string
	}{
		{"nginx/Chart.yaml", "apiVersion: v2\nname: nginx\nversion: 1.0.0\ndescription: web\n"},
		{"nginx/values.yaml", "replicaCount: 2\n"},
	} {
		_ = tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.body))})
		_, _ = tw.Write([]byte(f.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// chartHandlerInvoke wraps invokeAuthenticated for the chart handlers,
// adding the {cluster} param. Both handlers share the same {cluster}
// path-param shape.
func chartHandlerInvoke(
	t *testing.T,
	makeHandler func(*audit.Emitter) credentials.Handler,
	method, url string,
	body []byte,
) (*httptest.ResponseRecorder, *recordingSink) {
	t.Helper()
	return invokeAuthenticated(t, makeHandler, method, url,
		map[string]string{"cluster": "test"}, body)
}

func TestChartVersionsHandler_HappyPath(t *testing.T) {
	repoURL := chartFixtureRegistry(t)
	reg := testRegistry(t)
	cache := newChartVersionsCache()

	url := "/api/clusters/test/helm/chart/versions?ref=" + repoURL + "&chart=nginx"
	rec, sink := chartHandlerInvoke(t,
		func(*audit.Emitter) credentials.Handler { return chartVersionsHandler(reg, cache) },
		http.MethodGet, url, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got k8s.ChartVersionsResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Versions) != 2 || got.Versions[0] != "1.0.0" {
		t.Errorf("Versions = %v, want [1.0.0, 0.9.0]", got.Versions)
	}
	if got.Latest != "1.0.0" {
		t.Errorf("Latest = %q, want 1.0.0", got.Latest)
	}
	// Versions endpoint MUST NOT audit.
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("versions handler audited %d events, want 0", len(events))
	}
}

func TestChartVersionsHandler_MissingRef_Returns400(t *testing.T) {
	reg := testRegistry(t)
	cache := newChartVersionsCache()
	rec, _ := chartHandlerInvoke(t,
		func(*audit.Emitter) credentials.Handler { return chartVersionsHandler(reg, cache) },
		http.MethodGet, "/api/clusters/test/helm/chart/versions", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestChartVersionsHandler_BadScheme_Returns422(t *testing.T) {
	reg := testRegistry(t)
	cache := newChartVersionsCache()
	rec, _ := chartHandlerInvoke(t,
		func(*audit.Emitter) credentials.Handler { return chartVersionsHandler(reg, cache) },
		http.MethodGet, "/api/clusters/test/helm/chart/versions?ref=ftp://wrong&chart=x", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestChartVersionsHandler_CacheHit_DoesNotRefetch(t *testing.T) {
	// Build a fixture that fails on second hit so cache miss is
	// the only way the second call can succeed.
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = fmt.Fprint(w, `apiVersion: v1
entries:
  x:
    - {name: x, version: 1.0.0, urls: ["x-1.0.0.tgz"]}
`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := testRegistry(t)
	cache := newChartVersionsCache()
	url := "/api/clusters/test/helm/chart/versions?ref=" + srv.URL + "&chart=x"

	for i := 0; i < 3; i++ {
		rec, _ := chartHandlerInvoke(t,
			func(*audit.Emitter) credentials.Handler { return chartVersionsHandler(reg, cache) },
			http.MethodGet, url, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: status = %d", i, rec.Code)
		}
	}
	if hits != 1 {
		t.Errorf("registry hits = %d, want 1 (cache should serve calls 2 and 3)", hits)
	}
}

func TestChartVersionsHandler_NoCache_BypassesCache(t *testing.T) {
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = fmt.Fprint(w, `apiVersion: v1
entries:
  x:
    - {name: x, version: 1.0.0, urls: ["x-1.0.0.tgz"]}
`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := testRegistry(t)
	cache := newChartVersionsCache()
	url := "/api/clusters/test/helm/chart/versions?ref=" + srv.URL + "&chart=x&nocache=true"

	for i := 0; i < 3; i++ {
		rec, _ := chartHandlerInvoke(t,
			func(*audit.Emitter) credentials.Handler { return chartVersionsHandler(reg, cache) },
			http.MethodGet, url, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d: %d", i, rec.Code)
		}
	}
	if hits != 3 {
		t.Errorf("registry hits = %d, want 3 (nocache should bypass)", hits)
	}
}

func TestChartValuesHandler_HappyPath_AuditsAndCaches(t *testing.T) {
	repoURL := chartFixtureRegistry(t)
	reg := testRegistry(t)
	cache := newChartValuesCache()

	body := fmt.Sprintf(`{"ref":"%s","chart":"nginx","version":"1.0.0"}`, repoURL)
	rec, sink := chartHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return chartValuesHandler(reg, cache, e) },
		http.MethodPost, "/api/clusters/test/helm/chart/values", []byte(body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got k8s.ChartFetchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Meta.Name != "nginx" {
		t.Errorf("Meta.Name = %q, want nginx", got.Meta.Name)
	}
	if !strings.Contains(got.Values, "replicaCount") {
		t.Errorf("Values not preserved: %q", got.Values)
	}

	// Must emit ONE audit row, verb=helm_chart_fetch, outcome=success.
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit emitted %d events, want 1", len(events))
	}
	if events[0].Verb != audit.VerbHelmChartFetch {
		t.Errorf("Verb = %q, want %q", events[0].Verb, audit.VerbHelmChartFetch)
	}
	if events[0].Outcome != audit.OutcomeSuccess {
		t.Errorf("Outcome = %q, want success", events[0].Outcome)
	}
	if got, want := events[0].Extra["ref"], repoURL; got != want {
		t.Errorf("Extra.ref = %v, want %q", got, want)
	}
	if got := events[0].Extra["chart"]; got != "nginx" {
		t.Errorf("Extra.chart = %v, want nginx", got)
	}
	if got := events[0].Extra["version"]; got != "1.0.0" {
		t.Errorf("Extra.version = %v, want 1.0.0", got)
	}
	if got := events[0].Extra["cached"]; got != false {
		t.Errorf("Extra.cached = %v, want false (first call)", got)
	}

	// Second call should hit cache and emit cached:true row.
	rec2, sink2 := chartHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return chartValuesHandler(reg, cache, e) },
		http.MethodPost, "/api/clusters/test/helm/chart/values", []byte(body))
	if rec2.Code != http.StatusOK {
		t.Fatalf("call 2 status = %d", rec2.Code)
	}
	events2 := sink2.snapshot()
	if len(events2) != 1 || events2[0].Extra["cached"] != true {
		t.Errorf("call 2 cached flag = %v, want true", events2[0].Extra["cached"])
	}
}

func TestChartValuesHandler_RejectsBadBody(t *testing.T) {
	reg := testRegistry(t)
	cache := newChartValuesCache()
	cases := []struct {
		name string
		body string
	}{
		{name: "not json", body: "not json"},
		{name: "missing ref", body: `{"version":"1.0.0"}`},
		{name: "missing version", body: `{"ref":"https://x"}`},
		{name: "empty body", body: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, _ := chartHandlerInvoke(t,
				func(e *audit.Emitter) credentials.Handler { return chartValuesHandler(reg, cache, e) },
				http.MethodPost, "/api/clusters/test/helm/chart/values", []byte(tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestChartValuesHandler_UnsupportedDeps_EmitsFailureRow(t *testing.T) {
	tarball := buildDepsChartTarball(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `apiVersion: v1
entries:
  wp:
    - {name: wp, version: 1.0.0, urls: ["wp-1.0.0.tgz"]}
`)
	})
	mux.HandleFunc("/wp-1.0.0.tgz", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	reg := testRegistry(t)
	cache := newChartValuesCache()
	body := fmt.Sprintf(`{"ref":"%s","chart":"wp","version":"1.0.0"}`, srv.URL)
	rec, sink := chartHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return chartValuesHandler(reg, cache, e) },
		http.MethodPost, "/api/clusters/test/helm/chart/values", []byte(body))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Outcome == audit.OutcomeSuccess {
		t.Errorf("expected failure-outcome audit row; got %+v", events)
	}
}

func TestChartValuesHandler_UnknownCluster_Returns404(t *testing.T) {
	reg := testRegistry(t)
	cache := newChartValuesCache()
	body := `{"ref":"https://x","chart":"y","version":"1.0.0"}`
	// Override cluster to an unknown one.
	rec, _ := invokeAuthenticated(t,
		func(e *audit.Emitter) credentials.Handler { return chartValuesHandler(reg, cache, e) },
		http.MethodPost, "/api/clusters/missing/helm/chart/values",
		map[string]string{"cluster": "missing"}, []byte(body))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func buildDepsChartTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := `apiVersion: v2
name: wp
version: 1.0.0
dependencies:
  - name: mariadb
    version: 11.0.0
`
	_ = tw.WriteHeader(&tar.Header{Name: "wp/Chart.yaml", Mode: 0o644, Size: int64(len(body))})
	_, _ = tw.Write([]byte(body))
	values := "k: v\n"
	_ = tw.WriteHeader(&tar.Header{Name: "wp/values.yaml", Mode: 0o644, Size: int64(len(values))})
	_, _ = tw.Write([]byte(values))
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}
