package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/credentials"
)

// Shared test scaffolding for credentials-wrapped HTTP handlers in
// this package. Two pieces:
//
//   1. recordingSink — an audit.Sink that captures every Event the
//      handler emits, so assertions can hit verb / outcome / resource
//      / extra without parsing slog output.
//   2. invokeAuthenticated — boilerplate-eliminator that builds the
//      httptest request, plants chi RouteContext +
//      credentials.Session, runs the handler, and returns the
//      response + the recording sink for assertions.
//
// Both lived inline in apply_handler_test.go and
// bulk_download_audit_handler_test.go before this file. Consolidating
// them here keeps every audit-emitting handler test on a single
// scaffolding so future handlers (helm rollback, workload rollback,
// etc.) land cleanly without copy-pasting another sink declaration
// or re-deriving the request-construction plumbing.

// ── recordingSink ────────────────────────────────────────────────────

// recordingSink captures every audit.Event the handler emits.
// Concurrency-safe so handlers that emit from goroutines (none today,
// but future handlers might) don't trip the race detector.
type recordingSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordingSink) Record(_ context.Context, evt audit.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
}

// snapshot returns a copy of the captured events so tests can iterate
// without holding the lock — and so subsequent emissions don't mutate
// the slice the test is asserting against.
func (r *recordingSink) snapshot() []audit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.Event, len(r.events))
	copy(out, r.events)
	return out
}

// ── invokeAuthenticated ──────────────────────────────────────────────

// defaultTestSession is the session every handler test gets unless it
// builds its own request. Tests that need a different actor should
// build the request directly rather than extending this helper.
var defaultTestSession = credentials.Session{
	Subject: "alice@example.com",
	Email:   "alice@example.com",
}

// defaultTestProvider is the credentials.Provider passed to the
// handler. Reuses the package-level fakeProvider (cani_handler_test.go).
func defaultTestProvider() credentials.Provider {
	return fakeProvider{actor: "alice@example.com"}
}

// invokeAuthenticated drives a credentials-wrapped handler against a
// fully-formed HTTP request and returns the response + the recording
// sink the handler emitted into.
//
// The makeHandler callback is given a pre-built audit.Emitter wired to
// the sink — the helper owns both ends of the audit pipe so callers
// don't have to re-derive that wiring per test.
//
// Parameters:
//   - makeHandler — receives the test's emitter and returns the
//                   handler under test (typical shape:
//                   `func(e *audit.Emitter) credentials.Handler {
//                     return myHandler(reg, e) }`).
//   - method, url — HTTP request method + path.
//   - routeParams — chi.URLParam lookups; pass nil for handlers that
//                   don't read any.
//   - body        — request body. Pass nil for GET-style calls.
//
// Session + provider default to defaultTestSession /
// defaultTestProvider; tests that need a different identity should
// build their own request rather than extending this signature.
func invokeAuthenticated(
	t *testing.T,
	makeHandler func(*audit.Emitter) credentials.Handler,
	method, url string,
	routeParams map[string]string,
	body []byte,
) (*httptest.ResponseRecorder, *recordingSink) {
	t.Helper()

	sink := &recordingSink{}
	h := makeHandler(audit.New(sink))

	var reqBody io.Reader = http.NoBody
	if body != nil {
		reqBody = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, url, reqBody)

	rctx := chi.NewRouteContext()
	for k, v := range routeParams {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = credentials.WithSession(ctx, defaultTestSession)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h(rec, req, defaultTestProvider())
	return rec, sink
}
