package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	iamengine "github.com/gnana997/periscope/internal/awseks/iam"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// ── /api/identity/sensitive-catalog ──────────────────────────────

func TestSensitiveCatalogHandler_HasVersionAndEntries(t *testing.T) {
	h := identitySensitiveCatalogHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/identity/sensitive-catalog", http.NoBody)
	rec := httptest.NewRecorder()
	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got iamengine.SensitiveCatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Version == "" {
		t.Error("Version is empty; sensitive.yaml must set version")
	}
	if len(got.Entries) == 0 {
		t.Fatal("Entries is empty; sensitive.yaml has rows but the endpoint returned none")
	}
	for _, e := range got.Entries {
		if e.Action == "" || e.Category == "" {
			t.Errorf("malformed entry: %+v", e)
		}
		if e.ReverseQuery.Action != e.Action {
			t.Errorf("reverseQuery.action = %q, want = action %q", e.ReverseQuery.Action, e.Action)
		}
	}
}

func TestSensitiveCatalogHandler_AlphabeticalOrder(t *testing.T) {
	h := identitySensitiveCatalogHandler()
	req := httptest.NewRequest(http.MethodGet, "/api/identity/sensitive-catalog", http.NoBody)
	rec := httptest.NewRecorder()
	h(rec, req)

	var got iamengine.SensitiveCatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for i := 1; i < len(got.Entries); i++ {
		if got.Entries[i-1].Action > got.Entries[i].Action {
			t.Errorf("entries not sorted: %q before %q", got.Entries[i-1].Action, got.Entries[i].Action)
		}
	}
}

// ── /api/clusters/{cluster}/identity/capabilities ───────────────

func TestCapabilitiesHandler_NotEKS_ReturnsNotEKSPerFeature(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()
	probe := newCapabilitiesCache()
	awsCfg := awsAccessConfig{IAMProbe: true}

	h := identityCapabilitiesHandler(reg, cache, probe, awsCfg, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/capabilities")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got iamengine.CapabilitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	tab := got.Features[iamengine.FeatureAwsAccessTab]
	if tab.Available {
		t.Errorf("awsAccessTab.Available = true, want false for non-EKS")
	}
	if tab.Reason != iamengine.ReasonNotEKS {
		t.Errorf("awsAccessTab.Reason = %q, want %q", tab.Reason, iamengine.ReasonNotEKS)
	}
	rev := got.Features[iamengine.FeatureReverseLookup]
	if rev.Available || rev.Reason != iamengine.ReasonNotEKS {
		t.Errorf("reverseLookup non-EKS lock not applied: %+v", rev)
	}
	if cat := got.Features[iamengine.FeatureSensitiveCatalog]; !cat.Available {
		t.Errorf("sensitiveCatalog should always be available")
	}
}

func TestCapabilitiesHandler_CachesPerActor(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()
	probe := newCapabilitiesCache()
	awsCfg := awsAccessConfig{IAMProbe: true}

	h := identityCapabilitiesHandler(reg, cache, probe, awsCfg, audit.New(sink))

	// First call — miss, fills cache.
	rec1 := invokeIdentity(t, h, "test", "/api/clusters/test/identity/capabilities")
	if got := rec1.Header().Get("X-Capabilities-Cache"); got != "miss" {
		t.Errorf("first call cache header = %q, want miss", got)
	}

	// Second call — hit.
	rec2 := invokeIdentity(t, h, "test", "/api/clusters/test/identity/capabilities")
	if got := rec2.Header().Get("X-Capabilities-Cache"); got != "hit" {
		t.Errorf("second call cache header = %q, want hit", got)
	}

	// Audit should now have both a miss row and a hit row.
	var sawHit, sawMiss bool
	for _, ev := range sink.snapshot() {
		if ev.Verb != audit.VerbAwsIAMRead {
			continue
		}
		op := ev.Extra["op"]
		if op == "capabilities" {
			sawMiss = true
		}
		if op == "capabilities:cache_hit" {
			sawHit = true
		}
	}
	if !sawMiss || !sawHit {
		t.Errorf("audit rows: miss=%v hit=%v, want both true", sawMiss, sawHit)
	}
}

func TestCapabilitiesHandler_NoCacheBypassesCache(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()
	probe := newCapabilitiesCache()
	awsCfg := awsAccessConfig{IAMProbe: true}

	h := identityCapabilitiesHandler(reg, cache, probe, awsCfg, audit.New(sink))

	// Prime the cache.
	invokeIdentity(t, h, "test", "/api/clusters/test/identity/capabilities")

	// Second call with Cache-Control: no-cache → bypass.
	req := httptest.NewRequest(http.MethodGet, "/api/clusters/test/identity/capabilities", http.NoBody)
	req.Header.Set("Cache-Control", "no-cache")
	rec := httptest.NewRecorder()
	// Stamp the chi route ctx like invokeIdentity does, but reuse fakeProvider.
	stampChiAndProvider(req, "test")
	h(rec, req, fakeProvider{actor: "alice@corp"})

	if got := rec.Header().Get("X-Capabilities-Cache"); got != "bypass" {
		t.Errorf("no-cache header = %q, want bypass", got)
	}
}

// ── /identity/workload-permissions ──────────────────────────────

func TestWorkloadPermissionsHandler_MissingKind_400(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()

	h := iamWorkloadPermissionsHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/workload-permissions?namespace=ns&name=p")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing kind)", rec.Code)
	}
}

func TestWorkloadPermissionsHandler_NonEKS_422(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()

	h := iamWorkloadPermissionsHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/workload-permissions?kind=Pod&namespace=ns&name=p")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestWorkloadPermissionsHandler_UnknownKind_400(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()

	h := iamWorkloadPermissionsHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/workload-permissions?kind=Job&namespace=ns&name=j1")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (unknown kind)", rec.Code)
	}
}

// stampChiAndProvider mirrors invokeIdentity's chi.Context plumbing
// for tests that need to set their own header / method.
func stampChiAndProvider(req *http.Request, cluster string) {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", cluster)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = credentials.WithSession(ctx, credentials.Session{Subject: "alice@corp", Email: "alice@corp"})
	*req = *req.WithContext(ctx)
}
