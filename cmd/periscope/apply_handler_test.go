package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// recordingSink is the test-side implementation of audit.Sink — it
// captures every Event the handler emits so tests can assert on
// verb / outcome / resource shape without parsing slog output. Mirrors
// the same name in internal/audit/emitter_test.go but kept private to
// the handler tests since exporting it would couple two packages.
type recordingSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recordingSink) Record(_ context.Context, evt audit.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
}

func (r *recordingSink) snapshot() []audit.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]audit.Event, len(r.events))
	copy(out, r.events)
	return out
}

// applyHandlerCall is a captured invocation of applyResourceFn — kept
// so tests can assert the handler propagated dryRun/force/group/name
// correctly to the apply layer.
type applyHandlerCall struct {
	args k8s.ApplyResourceArgs
}

// stubApplyFn replaces applyResourceFn for the test's duration with a
// fake that records the args it was called with and returns the
// supplied (result, err). Returns the captured-calls accessor.
func stubApplyFn(t *testing.T, result k8s.ApplyResourceResult, err error) *[]applyHandlerCall {
	t.Helper()
	calls := []applyHandlerCall{}
	mu := sync.Mutex{}
	orig := applyResourceFn
	applyResourceFn = func(_ context.Context, _ credentials.Provider, args k8s.ApplyResourceArgs) (k8s.ApplyResourceResult, error) {
		mu.Lock()
		calls = append(calls, applyHandlerCall{args: args})
		mu.Unlock()
		return result, err
	}
	t.Cleanup(func() { applyResourceFn = orig })
	return &calls
}

// invokeApply drives the handler against a fully-formed PATCH request
// for a Deployment in `default` namespace. Centralising the chi route
// param injection + session attachment keeps each test focused on its
// assertion. `query` is the raw query-string ("" for none).
func invokeApply(t *testing.T, h func(http.ResponseWriter, *http.Request, credentials.Provider), reg *clusters.Registry, query, group, ns, name, body string) (*httptest.ResponseRecorder, *recordingSink) {
	t.Helper()
	url := "/api/clusters/test/resources/" + group + "/v1/deployments/" + ns + "/" + name
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodPatch, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/yaml")

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", "test")
	rctx.URLParams.Add("group", group)
	rctx.URLParams.Add("version", "v1")
	rctx.URLParams.Add("resource", "deployments")
	rctx.URLParams.Add("ns", ns)
	rctx.URLParams.Add("name", name)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = credentials.WithSession(ctx, credentials.Session{Subject: "alice@example.com", Email: "alice@example.com"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	sink := &recordingSink{}
	emitter := audit.New(sink)
	hh := applyResourceHandler(reg, emitter)
	hh(rec, req, fakeProvider{actor: "alice"})
	return rec, sink
}

// makeApplyResult constructs an ApplyResourceResult that matches the
// kind/apiVersion of a Deployment so the handler's JSON encode path
// gets a realistic body.
func makeApplyResult(name, namespace string, dryRun bool) k8s.ApplyResourceResult {
	return k8s.ApplyResourceResult{
		DryRun: dryRun,
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]interface{}{"replicas": int64(2)},
		},
	}
}

// minimalDeploymentYAML returns a body that satisfies the L1/L2 layers
// — but since the handler tests stub k8s.ApplyResource, the body
// content does not need to round-trip through the validation pipeline.
// We still send a non-empty body so readApplyBody returns OK.
func minimalDeploymentYAML(name, namespace string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: " + name +
		"\n  namespace: " + namespace + "\nspec:\n  replicas: 2\n"
}

// TestApplyHandler_Create_AuditSuccess covers the create case the SPA's
// new "Apply YAML" flow (#51) depends on: PATCH against a name that
// doesn't exist yet should land 200 with one audit row marked
// success and the resource ref correctly populated.
func TestApplyHandler_Create_AuditSuccess(t *testing.T) {
	reg := testRegistry(t)
	calls := stubApplyFn(t, makeApplyResult("new-app", "default", false), nil)

	rec, sink := invokeApply(t, nil, reg, "", "apps", "default", "new-app", minimalDeploymentYAML("new-app", "default"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := len(*calls); got != 1 {
		t.Fatalf("applyResourceFn calls=%d, want 1", got)
	}
	args := (*calls)[0].args
	if args.Group != "apps" || args.Version != "v1" || args.Resource != "deployments" {
		t.Errorf("GVR mismatch: got %+v", args)
	}
	if args.Namespace != "default" || args.Name != "new-app" {
		t.Errorf("ns/name mismatch: ns=%q name=%q", args.Namespace, args.Name)
	}
	if args.DryRun || args.Force {
		t.Errorf("dryRun=%v force=%v, want both false", args.DryRun, args.Force)
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events=%d, want 1", len(events))
	}
	e := events[0]
	if e.Verb != audit.VerbApply {
		t.Errorf("verb=%q, want %q", e.Verb, audit.VerbApply)
	}
	if e.Outcome != audit.OutcomeSuccess {
		t.Errorf("outcome=%q, want success", e.Outcome)
	}
	if e.Cluster != "test" {
		t.Errorf("cluster=%q, want test", e.Cluster)
	}
	if e.Resource.Group != "apps" || e.Resource.Version != "v1" || e.Resource.Resource != "deployments" {
		t.Errorf("resource ref GVR=%+v", e.Resource)
	}
	if e.Resource.Namespace != "default" || e.Resource.Name != "new-app" {
		t.Errorf("resource ref ns/name=%+v", e.Resource)
	}
	if e.Extra["dryRun"] != false {
		t.Errorf("Extra[dryRun]=%v, want false", e.Extra["dryRun"])
	}
}

// TestApplyHandler_Update_AuditSuccess is regression coverage for the
// existing inline-editor edit path the apply endpoint already serves
// — confirms the SSA-update audit row stays correct after we re-route
// create through the same endpoint.
func TestApplyHandler_Update_AuditSuccess(t *testing.T) {
	reg := testRegistry(t)
	stubApplyFn(t, makeApplyResult("existing", "prod", false), nil)

	rec, sink := invokeApply(t, nil, reg, "", "apps", "prod", "existing", minimalDeploymentYAML("existing", "prod"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Outcome != audit.OutcomeSuccess {
		t.Fatalf("want one success event, got %+v", events)
	}
	// Decoded body should round-trip the apply result so the SPA can
	// refresh its detail view with the new state.
	var got k8s.ApplyResourceResult
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Object["kind"] != "Deployment" {
		t.Errorf("response kind=%v, want Deployment", got.Object["kind"])
	}
}

// TestApplyHandler_NamespaceNotFound_AuditFailure exercises the third
// case from issue #52: applying into a namespace that doesn't exist
// returns 404 from apiserver, the handler classifies as Failure (not
// Denied — that's reserved for RBAC), and an audit row is still
// emitted so the action is visible in the trail.
func TestApplyHandler_NamespaceNotFound_AuditFailure(t *testing.T) {
	reg := testRegistry(t)
	apiErr := kerrors.NewNotFound(
		schema.GroupResource{Resource: "namespaces"},
		"ghost",
	)
	stubApplyFn(t, k8s.ApplyResourceResult{}, apiErr)

	rec, sink := invokeApply(t, nil, reg, "", "apps", "ghost", "orphan", minimalDeploymentYAML("orphan", "ghost"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", rec.Code, rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events=%d, want 1", len(events))
	}
	e := events[0]
	if e.Outcome != audit.OutcomeFailure {
		t.Errorf("outcome=%q, want failure", e.Outcome)
	}
	if e.Reason == "" {
		t.Errorf("Reason empty, want apiserver error message")
	}
	if e.Resource.Namespace != "ghost" || e.Resource.Name != "orphan" {
		t.Errorf("resource ref ns/name not preserved on failure: %+v", e.Resource)
	}
}

// TestApplyHandler_Forbidden_AuditDenied confirms RBAC denials are
// classified as Denied (not Failure) so operators can query them
// distinctly. Important because the SPA's pre-flight can-i check is
// best-effort — a true RBAC denial can still occur at apply time.
func TestApplyHandler_Forbidden_AuditDenied(t *testing.T) {
	reg := testRegistry(t)
	apiErr := kerrors.NewForbidden(
		schema.GroupResource{Group: "apps", Resource: "deployments"},
		"locked",
		errors.New("RBAC: forbidden"),
	)
	stubApplyFn(t, k8s.ApplyResourceResult{}, apiErr)

	rec, sink := invokeApply(t, nil, reg, "", "apps", "default", "locked", minimalDeploymentYAML("locked", "default"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", rec.Code)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Outcome != audit.OutcomeDenied {
		t.Fatalf("want one denied event, got %+v", events)
	}
}

// TestApplyHandler_DryRun_AuditWithFlag locks in the "dry-run still
// emits an audit row" decision called out in issue #52 — Extra[dryRun]
// flags it for downstream filters so the row remains queryable but
// distinct from real applies.
func TestApplyHandler_DryRun_AuditWithFlag(t *testing.T) {
	reg := testRegistry(t)
	calls := stubApplyFn(t, makeApplyResult("preview", "staging", true), nil)

	rec, sink := invokeApply(t, nil, reg, "dryRun=true", "apps", "staging", "preview", minimalDeploymentYAML("preview", "staging"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := (*calls)[0].args.DryRun; !got {
		t.Errorf("applyResourceFn args.DryRun=%v, want true (handler must propagate ?dryRun=true)", got)
	}

	var resp k8s.ApplyResourceResult
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.DryRun {
		t.Errorf("response DryRun=%v, want true", resp.DryRun)
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events=%d, want 1 (dry-run still emits)", len(events))
	}
	if events[0].Extra["dryRun"] != true {
		t.Errorf("Extra[dryRun]=%v, want true", events[0].Extra["dryRun"])
	}
	if events[0].Outcome != audit.OutcomeSuccess {
		t.Errorf("dry-run outcome=%q, want success", events[0].Outcome)
	}
}

// TestApplyHandler_Force_PassedThrough confirms ?force=true reaches
// k8s.ApplyResource so the SSA "force conflict resolution" path works
// from the SPA's second-attempt flow.
func TestApplyHandler_Force_PassedThrough(t *testing.T) {
	reg := testRegistry(t)
	calls := stubApplyFn(t, makeApplyResult("retry", "prod", false), nil)

	rec, sink := invokeApply(t, nil, reg, "force=true", "apps", "prod", "retry", minimalDeploymentYAML("retry", "prod"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if got := (*calls)[0].args.Force; !got {
		t.Errorf("args.Force=%v, want true", got)
	}
	if events := sink.snapshot(); events[0].Extra["force"] != true {
		t.Errorf("Extra[force]=%v, want true", events[0].Extra["force"])
	}
}

// TestApplyHandler_GroupCoreNormalized checks the URL contract from
// docs/api.md §"apply": URL segment `core` is rewritten to the empty
// API group server-side, both in the apply args and in the audit row.
func TestApplyHandler_GroupCoreNormalized(t *testing.T) {
	reg := testRegistry(t)
	calls := stubApplyFn(t, k8s.ApplyResourceResult{
		Object: map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "ConfigMap",
			"metadata":   map[string]interface{}{"name": "cm", "namespace": "default"},
		},
	}, nil)

	rec, sink := invokeApply(t, nil, reg, "", "core", "default", "cm", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n  namespace: default\n")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := (*calls)[0].args.Group; got != "" {
		t.Errorf("args.Group=%q, want empty (core normalized)", got)
	}
	if got := sink.snapshot()[0].Resource.Group; got != "" {
		t.Errorf("audit Resource.Group=%q, want empty", got)
	}
}

// TestApplyHandler_BadYAML_400 confirms the handler maps the stable
// "apply: " error prefix from the validation pipeline (L1/L2) to a 400
// rather than the default 500 — the SPA needs this distinction to
// show "your YAML was malformed" instead of a generic backend error.
func TestApplyHandler_BadYAML_400(t *testing.T) {
	reg := testRegistry(t)
	stubApplyFn(t, k8s.ApplyResourceResult{}, errors.New("apply: parse yaml: line 3: bad indent"))

	rec, sink := invokeApply(t, nil, reg, "", "apps", "default", "x", "::not yaml::")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
	if events := sink.snapshot(); len(events) != 1 || events[0].Outcome != audit.OutcomeFailure {
		t.Fatalf("want one failure event, got %+v", events)
	}
}

// TestApplyHandler_Conflict_409 covers the SSA conflict path the SPA's
// per-field conflict resolver depends on (docs/api.md §"apply").
// The handler must surface 409 with a structured metav1.Status JSON
// body whose causes[] array drives the resolver; failing to do so
// breaks the second-attempt force flow tested above.
func TestApplyHandler_Conflict_409(t *testing.T) {
	reg := testRegistry(t)
	conflictErr := kerrors.NewConflict(
		schema.GroupResource{Group: "apps", Resource: "deployments"},
		"contended",
		errors.New("apply conflict: kustomize-controller owns spec.replicas"),
	)
	stubApplyFn(t, k8s.ApplyResourceResult{}, conflictErr)

	rec, sink := invokeApply(t, nil, reg, "", "apps", "default", "contended", minimalDeploymentYAML("contended", "default"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status=%d, want 409", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type=%q, want application/json (metav1.Status body)", ct)
	}
	// Decode as a metav1.Status and check the SPA-resolver-relevant
	// shape: status="Failure", reason="Conflict", message present.
	// Note kind/apiVersion are typically empty when client-go status
	// errors are JSON-encoded directly (the apiserver populates them
	// from the scheme; writeAPIError encodes the embedded ErrStatus
	// as-is). We assert on fields the SPA resolver actually reads.
	var status map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode metav1.Status: %v body=%s", err, rec.Body.String())
	}
	if status["status"] != "Failure" {
		t.Errorf("body status=%v, want Failure", status["status"])
	}
	if status["reason"] != "Conflict" {
		t.Errorf("body reason=%v, want Conflict", status["reason"])
	}
	if msg, _ := status["message"].(string); !strings.Contains(msg, "kustomize-controller") {
		t.Errorf("body message=%q, want it to carry the conflict detail", msg)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Outcome != audit.OutcomeFailure {
		t.Fatalf("want one failure event for conflict, got %+v", events)
	}
}

// TestApplyHandler_ClusterNotFound_404 confirms the cluster-routing
// guard at the top of the handler short-circuits before the apply
// path runs — and crucially does NOT emit an audit row, since no
// privileged action was attempted against any real cluster.
func TestApplyHandler_ClusterNotFound_404(t *testing.T) {
	reg := testRegistry(t)
	calls := stubApplyFn(t, k8s.ApplyResourceResult{}, nil)

	req := httptest.NewRequest(http.MethodPatch, "/api/clusters/missing/resources/apps/v1/deployments/default/x", bytes.NewBufferString(minimalDeploymentYAML("x", "default")))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", "missing")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	sink := &recordingSink{}
	emitter := audit.New(sink)
	hh := applyResourceHandler(reg, emitter)
	hh(rec, req, fakeProvider{actor: "alice"})

	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
	if got := len(*calls); got != 0 {
		t.Errorf("applyResourceFn calls=%d, want 0 (handler should short-circuit)", got)
	}
	if got := len(sink.snapshot()); got != 0 {
		t.Errorf("audit events=%d, want 0 (no action against real cluster)", got)
	}
}
