package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/go-chi/chi/v5"
	authv1 "k8s.io/api/authorization/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// fakeProvider mimics credentials.Provider with configurable actor +
// impersonation, so tests can flex shared / tier / raw shapes without
// spinning up the real Provider machinery.
type fakeProvider struct {
	actor string
	imp   credentials.ImpersonationConfig
}

func (f fakeProvider) Retrieve(_ context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: "x", SecretAccessKey: "y"}, nil
}
func (f fakeProvider) Actor() string                                { return f.actor }
func (f fakeProvider) Impersonation() credentials.ImpersonationConfig { return f.imp }

// testRegistry writes a minimal kubeconfig-backend registry to a temp
// dir and loads it. The kubeconfigPath need not exist because tests
// stub the SAR/SSRR funcs — k8s.NewClientset is never invoked.
func testRegistry(t *testing.T) *clusters.Registry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "registry.yaml")
	yaml := "clusters:\n  - name: test\n    backend: kubeconfig\n    kubeconfigPath: /nonexistent/kubeconfig\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	reg, err := clusters.LoadFromFile(path)
	if err != nil {
		t.Fatalf("load registry: %v", err)
	}
	return reg
}

// invokeCanI runs the handler with a route param matching the test
// cluster, returning the recorder for assertion.
func invokeCanI(t *testing.T, h func(http.ResponseWriter, *http.Request, credentials.Provider), p credentials.Provider, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test/can-i", bytes.NewBufferString(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", "test")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h(rec, req, p)
	return rec
}

func decodeCanI(t *testing.T, rec *httptest.ResponseRecorder) CanIResponse {
	t.Helper()
	var resp CanIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	return resp
}

// stubSARFn replaces caniCheckSARFn for the duration of the test.
func stubSARFn(t *testing.T, fn func(ctx context.Context, p credentials.Provider, c clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error)) {
	t.Helper()
	orig := caniCheckSARFn
	caniCheckSARFn = fn
	t.Cleanup(func() { caniCheckSARFn = orig })
}

// stubSSRRFn replaces caniListSSRRFn for the duration of the test.
func stubSSRRFn(t *testing.T, fn func(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace string) (*authv1.SubjectRulesReviewStatus, error)) {
	t.Helper()
	orig := caniListSSRRFn
	caniListSSRRFn = fn
	t.Cleanup(func() { caniListSSRRFn = orig })
}

func TestCanI_ClusterNotFound(t *testing.T) {
	reg := testRegistry(t)
	cache := newCanICache(30 * time.Second)
	h := caniHandler(reg, cache)

	req := httptest.NewRequest(http.MethodPost, "/api/clusters/missing/can-i", bytes.NewBufferString(`{"checks":[{"verb":"get","resource":"pods"}]}`))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", "missing")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()
	h(rec, req, fakeProvider{actor: "alice"})

	if rec.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", rec.Code)
	}
}

func TestCanI_RejectsTooManyChecks(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	checks := make([]CanICheck, caniMaxChecks+1)
	for i := range checks {
		checks[i] = CanICheck{Verb: "get", Resource: "pods", Namespace: "default"}
	}
	body, _ := json.Marshal(CanIRequest{Checks: checks})
	rec := invokeCanI(t, h, fakeProvider{actor: "alice"}, string(body))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestCanI_AnonymousFailClosed(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ authv1.ResourceAttributes) (bool, string, error) {
		t.Fatal("SAR should not be called for anonymous request")
		return false, "", nil
	})

	rec := invokeCanI(t, h, fakeProvider{actor: "anonymous"},
		`{"checks":[{"verb":"get","resource":"pods","namespace":"default"}]}`)
	resp := decodeCanI(t, rec)
	if len(resp.Results) != 1 {
		t.Fatalf("got %d results, want 1", len(resp.Results))
	}
	if resp.Results[0].Allowed {
		t.Error("anonymous request should be denied")
	}
	if resp.Results[0].Reason != "unauthenticated" {
		t.Errorf("reason = %q, want unauthenticated", resp.Results[0].Reason)
	}
}

func TestCanI_SAR_SingleCheck(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	var sarCalls atomic.Int32
	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error) {
		sarCalls.Add(1)
		// Simulate "delete pods" denied, "get pods" allowed.
		if attr.Verb == "delete" {
			return false, "tier 'triage' cannot delete pods", nil
		}
		return true, "", nil
	})
	stubSSRRFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*authv1.SubjectRulesReviewStatus, error) {
		t.Fatal("SSRR should not be called for single namespaced check")
		return nil, nil
	})

	rec := invokeCanI(t, h, fakeProvider{actor: "alice"},
		`{"checks":[{"verb":"delete","resource":"pods","namespace":"default"}]}`)
	resp := decodeCanI(t, rec)
	if len(resp.Results) != 1 || resp.Results[0].Allowed {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Results[0].Reason == "" {
		t.Error("reason not propagated from SAR")
	}
	if sarCalls.Load() != 1 {
		t.Errorf("SAR called %d times, want 1", sarCalls.Load())
	}
}

func TestCanI_BatchedSSRR_PreferredOverSAR(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	var sarCalls, ssrrCalls atomic.Int32
	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ authv1.ResourceAttributes) (bool, string, error) {
		sarCalls.Add(1)
		return false, "", nil
	})
	stubSSRRFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, ns string) (*authv1.SubjectRulesReviewStatus, error) {
		ssrrCalls.Add(1)
		if ns != "default" {
			t.Errorf("SSRR called for ns=%q, want default", ns)
		}
		return &authv1.SubjectRulesReviewStatus{
			ResourceRules: []authv1.ResourceRule{
				{Verbs: []string{"get", "list", "watch"}, APIGroups: []string{""}, Resources: []string{"pods"}},
				{Verbs: []string{"patch"}, APIGroups: []string{"apps"}, Resources: []string{"deployments"}},
			},
		}, nil
	})

	body := `{"checks":[
		{"verb":"get","resource":"pods","namespace":"default"},
		{"verb":"list","resource":"pods","namespace":"default"},
		{"verb":"delete","resource":"pods","namespace":"default"},
		{"verb":"patch","group":"apps","resource":"deployments","namespace":"default"}
	]}`
	rec := invokeCanI(t, h, fakeProvider{actor: "alice"}, body)
	resp := decodeCanI(t, rec)

	if ssrrCalls.Load() != 1 {
		t.Errorf("SSRR called %d times, want 1", ssrrCalls.Load())
	}
	if sarCalls.Load() != 0 {
		t.Errorf("SAR called %d times, want 0 (all checks SSRR-eligible)", sarCalls.Load())
	}
	want := []bool{true, true, false, true}
	for i, w := range want {
		if resp.Results[i].Allowed != w {
			t.Errorf("Results[%d].Allowed = %v, want %v", i, resp.Results[i].Allowed, w)
		}
	}
}

func TestCanI_ClusterScopedFallsBackToSAR(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	var sarSeen []authv1.ResourceAttributes
	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error) {
		sarSeen = append(sarSeen, attr)
		return true, "", nil
	})
	stubSSRRFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*authv1.SubjectRulesReviewStatus, error) {
		t.Fatal("SSRR called for cluster-scoped check")
		return nil, nil
	})

	rec := invokeCanI(t, h, fakeProvider{actor: "alice"},
		`{"checks":[{"verb":"list","resource":"nodes"}]}`)
	resp := decodeCanI(t, rec)
	if !resp.Results[0].Allowed {
		t.Errorf("expected allowed=true, got %+v", resp.Results[0])
	}
	if len(sarSeen) != 1 || sarSeen[0].Resource != "nodes" || sarSeen[0].Namespace != "" {
		t.Errorf("SAR not invoked correctly; seen=%+v", sarSeen)
	}
}

func TestCanI_SubresourceFallsBackToSAR(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	var sarCalls atomic.Int32
	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error) {
		sarCalls.Add(1)
		if attr.Subresource != "exec" {
			t.Errorf("subresource = %q, want exec", attr.Subresource)
		}
		return true, "", nil
	})
	stubSSRRFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*authv1.SubjectRulesReviewStatus, error) {
		t.Fatal("SSRR called for subresource check (must use SAR)")
		return nil, nil
	})

	// Three checks in default namespace, but one has a subresource.
	body := `{"checks":[
		{"verb":"create","resource":"pods","subresource":"exec","namespace":"default"},
		{"verb":"create","resource":"pods","subresource":"exec","namespace":"default"},
		{"verb":"create","resource":"pods","subresource":"exec","namespace":"default"}
	]}`
	rec := invokeCanI(t, h, fakeProvider{actor: "alice"}, body)
	resp := decodeCanI(t, rec)
	if sarCalls.Load() != 3 {
		t.Errorf("SAR called %d times, want 3", sarCalls.Load())
	}
	if !resp.Results[0].Allowed {
		t.Errorf("expected allowed=true, got %+v", resp.Results[0])
	}
}

func TestCanI_CacheHit_ReusesPreviousResult(t *testing.T) {
	reg := testRegistry(t)
	cache := newCanICache(30 * time.Second)
	h := caniHandler(reg, cache)

	var sarCalls atomic.Int32
	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ authv1.ResourceAttributes) (bool, string, error) {
		sarCalls.Add(1)
		return true, "", nil
	})

	body := `{"checks":[{"verb":"delete","resource":"pods","namespace":"default"}]}`
	_ = invokeCanI(t, h, fakeProvider{actor: "alice"}, body)
	_ = invokeCanI(t, h, fakeProvider{actor: "alice"}, body)
	_ = invokeCanI(t, h, fakeProvider{actor: "alice"}, body)

	if sarCalls.Load() != 1 {
		t.Errorf("SAR called %d times across 3 identical requests, want 1", sarCalls.Load())
	}
}

func TestCanI_CacheKeyIsolatesByImpersonation(t *testing.T) {
	reg := testRegistry(t)
	cache := newCanICache(30 * time.Second)
	h := caniHandler(reg, cache)

	var sarCalls atomic.Int32
	stubSARFn(t, func(_ context.Context, p credentials.Provider, _ clusters.Cluster, _ authv1.ResourceAttributes) (bool, string, error) {
		sarCalls.Add(1)
		// Different impersonation → different result. Tier admin allowed,
		// tier triage denied.
		for _, g := range p.Impersonation().Groups {
			if g == "periscope-tier:admin" {
				return true, "", nil
			}
		}
		return false, "tier 'triage' cannot delete pods", nil
	})

	body := `{"checks":[{"verb":"delete","resource":"pods","namespace":"default"}]}`

	// Same actor (alice) but different impersonation groups: separate
	// cache entries. This is the safety property — never let an admin
	// session bleed an "allowed" into a triage session of the same user.
	adminP := fakeProvider{actor: "alice", imp: credentials.ImpersonationConfig{UserName: "alice", Groups: []string{"periscope-tier:admin"}}}
	triageP := fakeProvider{actor: "alice", imp: credentials.ImpersonationConfig{UserName: "alice", Groups: []string{"periscope-tier:triage"}}}

	rec1 := invokeCanI(t, h, adminP, body)
	rec2 := invokeCanI(t, h, triageP, body)

	if !decodeCanI(t, rec1).Results[0].Allowed {
		t.Error("admin should be allowed")
	}
	if decodeCanI(t, rec2).Results[0].Allowed {
		t.Error("triage should be denied — cache leaked from admin session")
	}
	if sarCalls.Load() != 2 {
		t.Errorf("SAR called %d times, want 2 (no cross-tier cache hit)", sarCalls.Load())
	}
}

func TestCanI_SharedMode_CacheCollapses(t *testing.T) {
	reg := testRegistry(t)
	cache := newCanICache(30 * time.Second)
	h := caniHandler(reg, cache)

	var sarCalls atomic.Int32
	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ authv1.ResourceAttributes) (bool, string, error) {
		sarCalls.Add(1)
		return true, "", nil
	})

	// Shared mode = empty Impersonation. Two different actors collapse
	// onto one cache entry — they all see the dashboard's pod role's
	// permissions, which is a single answer. Tier/raw users do NOT
	// collapse (covered by TestCanI_CacheKeyIsolatesByImpersonation).
	alice := fakeProvider{actor: "alice"}
	bob := fakeProvider{actor: "bob"}
	body := `{"checks":[{"verb":"get","resource":"pods","namespace":"default"}]}`
	_ = invokeCanI(t, h, alice, body)
	_ = invokeCanI(t, h, bob, body)
	if sarCalls.Load() != 1 {
		t.Errorf("SAR called %d times, want 1 (shared-mode cache collapse across actors)", sarCalls.Load())
	}
}

func TestCanI_FailClosed_OnSARError(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ authv1.ResourceAttributes) (bool, string, error) {
		return false, "", errors.New("connection refused")
	})

	rec := invokeCanI(t, h, fakeProvider{actor: "alice"},
		`{"checks":[{"verb":"get","resource":"pods","namespace":"default"}]}`)
	if rec.Code != http.StatusOK {
		t.Errorf("status=%d, want 200 (fail-closed should not 5xx)", rec.Code)
	}
	resp := decodeCanI(t, rec)
	if resp.Results[0].Allowed {
		t.Error("expected fail-closed allowed=false on SAR error")
	}
	if resp.Results[0].Reason == "" {
		t.Error("expected classified reason on SAR error")
	}
}

func TestCanI_FailClosed_OnSSRRError(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ authv1.ResourceAttributes) (bool, string, error) {
		t.Fatal("SAR should not be called when SSRR is the chosen route")
		return false, "", nil
	})
	stubSSRRFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*authv1.SubjectRulesReviewStatus, error) {
		return nil, errors.New("apiserver timeout")
	})

	body := `{"checks":[
		{"verb":"get","resource":"pods","namespace":"default"},
		{"verb":"list","resource":"pods","namespace":"default"},
		{"verb":"watch","resource":"pods","namespace":"default"}
	]}`
	rec := invokeCanI(t, h, fakeProvider{actor: "alice"}, body)
	resp := decodeCanI(t, rec)
	if len(resp.Results) != 3 {
		t.Fatalf("got %d results, want 3", len(resp.Results))
	}
	for i, r := range resp.Results {
		if r.Allowed {
			t.Errorf("Results[%d] allowed under SSRR error; want fail-closed", i)
		}
	}
}

func TestCanI_PreservesOrder(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	// Mix of: cluster-scoped (SAR), subresource (SAR), and a same-namespace
	// SSRR-eligible bucket. Verify ordering is preserved despite the
	// internal bucketing.
	stubSARFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error) {
		// "tag" the result via reason so we can assert which path each came from.
		return true, "sar:" + attr.Verb + "/" + attr.Resource, nil
	})
	stubSSRRFn(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*authv1.SubjectRulesReviewStatus, error) {
		return &authv1.SubjectRulesReviewStatus{
			ResourceRules: []authv1.ResourceRule{
				{Verbs: []string{"*"}, APIGroups: []string{"*"}, Resources: []string{"*"}},
			},
		}, nil
	})

	body := `{"checks":[
		{"verb":"list","resource":"nodes"},
		{"verb":"get","resource":"pods","namespace":"default"},
		{"verb":"create","resource":"pods","subresource":"exec","namespace":"default"},
		{"verb":"list","resource":"pods","namespace":"default"},
		{"verb":"watch","resource":"pods","namespace":"default"}
	]}`
	rec := invokeCanI(t, h, fakeProvider{actor: "alice"}, body)
	resp := decodeCanI(t, rec)
	if len(resp.Results) != 5 {
		t.Fatalf("got %d results, want 5", len(resp.Results))
	}
	// Index 0: nodes (cluster-scoped → SAR), reason has "sar:" prefix
	if resp.Results[0].Reason == "" || resp.Results[0].Reason[:4] != "sar:" {
		t.Errorf("Results[0] not from SAR path: %+v", resp.Results[0])
	}
	// Index 2: subresource → SAR
	if resp.Results[2].Reason == "" || resp.Results[2].Reason[:4] != "sar:" {
		t.Errorf("Results[2] not from SAR path: %+v", resp.Results[2])
	}
	// Indices 1, 3, 4: namespaced bucket of 3 → SSRR (no reason)
	for _, i := range []int{1, 3, 4} {
		if resp.Results[i].Reason != "" {
			t.Errorf("Results[%d] should be from SSRR path (empty reason), got %+v", i, resp.Results[i])
		}
		if !resp.Results[i].Allowed {
			t.Errorf("Results[%d] should be allowed under wildcard rule", i)
		}
	}
}

func TestCanI_EmptyChecksReturnsEmptyResults(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	rec := invokeCanI(t, h, fakeProvider{actor: "alice"}, `{"checks":[]}`)
	resp := decodeCanI(t, rec)
	if len(resp.Results) != 0 {
		t.Errorf("got %d results, want 0", len(resp.Results))
	}
}

func TestCanI_BadJSONReturns400(t *testing.T) {
	reg := testRegistry(t)
	h := caniHandler(reg, newCanICache(30*time.Second))

	rec := invokeCanI(t, h, fakeProvider{actor: "alice"}, "not json")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}
