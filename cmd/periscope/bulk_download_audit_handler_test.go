package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// Tests for bulkDownloadAuditHandler — the SPA-driven audit emission
// that records "alice bulk-downloaded N {kind} from cluster X" as a
// single structured row per download. See RFC 0003 §4 (`bulk_download`
// verb) and the issue tracker (#82) for design rationale.
//
// fakeProvider is shared with cani_handler_test.go (same package).

// recordSink captures audit events for assertion.
type recordSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordSink) Record(_ context.Context, evt audit.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
}

func (r *recordSink) snapshot() []audit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]audit.Event, len(r.events))
	copy(cp, r.events)
	return cp
}

// bulkDownloadRegistry writes a one-cluster YAML to a temp dir and
// loads it. Mirrors how production boots; saves us from exposing a
// programmatic Registry constructor for tests.
func bulkDownloadRegistry(t *testing.T, clusterName string) *clusters.Registry {
	t.Helper()
	dir := t.TempDir()
	yaml := "clusters:\n" +
		"  - name: " + clusterName + "\n" +
		"    backend: in-cluster\n"
	path := filepath.Join(dir, "registry.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	reg, err := clusters.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	return reg
}

// invokeBulkDownload posts the body to the handler with a planted
// session in the context and the {cluster} chi URL param wired in.
func invokeBulkDownload(t *testing.T, reg *clusters.Registry, sink *recordSink, cluster string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	emitter := audit.New(sink)
	h := bulkDownloadAuditHandler(reg, emitter)

	req := httptest.NewRequest(http.MethodPost,
		"/api/clusters/"+cluster+"/audit/bulk-download",
		bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", cluster)
	req = req.WithContext(credentials.WithSession(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		credentials.Session{Subject: "alice@corp", Email: "alice@corp", Groups: []string{"eng"}},
	))

	rec := httptest.NewRecorder()
	h(rec, req, fakeProvider{actor: "alice@corp"})
	return rec
}

func TestBulkDownloadAudit_Success(t *testing.T) {
	reg := bulkDownloadRegistry(t, "prod-eu")
	sink := &recordSink{}

	body := mustJSONBulk(t, map[string]any{
		"kind":          "configmaps",
		"count":         42,
		"ids":           []string{"default/cm-a", "default/cm-b", "default/cm-c"},
		"outcome":       "success",
		"failure_count": 0,
	})
	rec := invokeBulkDownload(t, reg, sink, "prod-eu", body)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	e := events[0]
	if e.Verb != audit.VerbBulkDownload {
		t.Errorf("verb = %q, want %q", e.Verb, audit.VerbBulkDownload)
	}
	if e.Outcome != audit.OutcomeSuccess {
		t.Errorf("outcome = %q, want success", e.Outcome)
	}
	if e.Cluster != "prod-eu" {
		t.Errorf("cluster = %q, want prod-eu", e.Cluster)
	}
	if e.Actor.Sub != "alice@corp" {
		t.Errorf("actor.sub = %q, want alice@corp", e.Actor.Sub)
	}
	if e.Resource.Resource != "configmaps" || e.Resource.Namespace != "*" {
		t.Errorf("resource = %+v, want {Resource:configmaps Namespace:*}", e.Resource)
	}
	if got := e.Extra["kind"]; got != "configmaps" {
		t.Errorf("extra.kind = %v, want configmaps", got)
	}
	if got := e.Extra["count"]; got != 42 {
		t.Errorf("extra.count = %v, want 42", got)
	}
	if got := e.Extra["failure_count"]; got != 0 {
		t.Errorf("extra.failure_count = %v, want 0", got)
	}
}

func TestBulkDownloadAudit_PartialIsSuccess(t *testing.T) {
	reg := bulkDownloadRegistry(t, "c1")
	sink := &recordSink{}
	body := mustJSONBulk(t, map[string]any{
		"kind": "pods", "count": 10,
		"ids":     []string{"ns/p1"},
		"outcome": "success", "failure_count": 3,
	})
	if rec := invokeBulkDownload(t, reg, sink, "c1", body); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	e := sink.snapshot()[0]
	if e.Outcome != audit.OutcomeSuccess {
		t.Errorf("partial should record as success, got %q", e.Outcome)
	}
	if got := e.Extra["failure_count"]; got != 3 {
		t.Errorf("extra.failure_count = %v, want 3", got)
	}
}

func TestBulkDownloadAudit_Failure(t *testing.T) {
	reg := bulkDownloadRegistry(t, "c1")
	sink := &recordSink{}
	body := mustJSONBulk(t, map[string]any{
		"kind": "pods", "count": 5, "outcome": "failure", "failure_count": 5,
	})
	if rec := invokeBulkDownload(t, reg, sink, "c1", body); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	e := sink.snapshot()[0]
	if e.Outcome != audit.OutcomeFailure {
		t.Errorf("outcome = %q, want failure", e.Outcome)
	}
}

func TestBulkDownloadAudit_ClusterNotFound(t *testing.T) {
	reg := bulkDownloadRegistry(t, "c1")
	sink := &recordSink{}
	body := mustJSONBulk(t, map[string]any{"kind": "pods", "count": 1, "outcome": "success"})
	rec := invokeBulkDownload(t, reg, sink, "nope", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := len(sink.snapshot()); got != 0 {
		t.Errorf("emitted %d events on cluster-not-found, want 0", got)
	}
}

func TestBulkDownloadAudit_MalformedBody(t *testing.T) {
	reg := bulkDownloadRegistry(t, "c1")
	sink := &recordSink{}
	rec := invokeBulkDownload(t, reg, sink, "c1", []byte("not json"))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := len(sink.snapshot()); got != 0 {
		t.Errorf("emitted %d events on malformed body, want 0", got)
	}
}

func TestBulkDownloadAudit_UnknownKind(t *testing.T) {
	reg := bulkDownloadRegistry(t, "c1")
	sink := &recordSink{}
	body := mustJSONBulk(t, map[string]any{"kind": "bogusresource", "count": 1, "outcome": "success"})
	rec := invokeBulkDownload(t, reg, sink, "c1", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%q", rec.Code, rec.Body.String())
	}
	if got := len(sink.snapshot()); got != 0 {
		t.Errorf("emitted %d events on unknown kind, want 0", got)
	}
}

func TestBulkDownloadAudit_KnownKinds(t *testing.T) {
	reg := bulkDownloadRegistry(t, "c1")
	for _, kind := range []string{"pods", "configmaps", "secrets", "customresources/certificates"} {
		t.Run(kind, func(t *testing.T) {
			sink := &recordSink{}
			body := mustJSONBulk(t, map[string]any{
				"kind": kind, "count": 1, "outcome": "success",
			})
			rec := invokeBulkDownload(t, reg, sink, "c1", body)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("kind=%q status = %d, want 204; body=%q", kind, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestBulkDownloadAudit_CountOverCap(t *testing.T) {
	reg := bulkDownloadRegistry(t, "c1")
	sink := &recordSink{}
	body := mustJSONBulk(t, map[string]any{
		"kind": "pods", "count": bulkDownloadCap + 1, "outcome": "success",
	})
	rec := invokeBulkDownload(t, reg, sink, "c1", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestBulkDownloadAudit_CountZero(t *testing.T) {
	reg := bulkDownloadRegistry(t, "c1")
	sink := &recordSink{}
	body := mustJSONBulk(t, map[string]any{"kind": "pods", "count": 0, "outcome": "success"})
	rec := invokeBulkDownload(t, reg, sink, "c1", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for count=0", rec.Code)
	}
}

func TestBulkDownloadAudit_IDsTruncatedServerSide(t *testing.T) {
	reg := bulkDownloadRegistry(t, "c1")
	sink := &recordSink{}
	ids := make([]string, bulkDownloadIDCap+10)
	for i := range ids {
		ids[i] = "ns/r-" + strings.Repeat("x", 3)
	}
	body := mustJSONBulk(t, map[string]any{
		"kind": "pods", "count": bulkDownloadIDCap + 10,
		"ids": ids, "outcome": "success",
	})
	rec := invokeBulkDownload(t, reg, sink, "c1", body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rec.Code, rec.Body.String())
	}
	e := sink.snapshot()[0]
	gotIDs, ok := e.Extra["ids"].([]string)
	if !ok {
		t.Fatalf("extra.ids type = %T, want []string", e.Extra["ids"])
	}
	if len(gotIDs) != bulkDownloadIDCap {
		t.Errorf("extra.ids len = %d, want %d (server-side truncate)", len(gotIDs), bulkDownloadIDCap)
	}
	if got := e.Extra["count"]; got != bulkDownloadIDCap+10 {
		t.Errorf("extra.count = %v, want %d (count is the truth, ids are a sample)", got, bulkDownloadIDCap+10)
	}
}

func mustJSONBulk(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}
