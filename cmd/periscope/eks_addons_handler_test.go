package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// fakeEKSAddonsClient implements eksAddonsAPI. Each method is
// pluggable per-test; default behavior returns errors so a test that
// forgot to wire one fails loudly.
type fakeEKSAddonsClient struct {
	listAddonsCalls  int32
	descAddonCalls   int32
	descVersionsCals int32
	descConfigCalls  int32
	descClusterCalls int32
	createAddonCalls int32
	updateAddonCalls int32
	deleteAddonCalls int32

	listFn     func(ctx context.Context, in *eks.ListAddonsInput) (*eks.ListAddonsOutput, error)
	descFn     func(ctx context.Context, in *eks.DescribeAddonInput) (*eks.DescribeAddonOutput, error)
	versionsFn func(ctx context.Context, in *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error)
	configFn   func(ctx context.Context, in *eks.DescribeAddonConfigurationInput) (*eks.DescribeAddonConfigurationOutput, error)
	clusterFn  func(ctx context.Context, in *eks.DescribeClusterInput) (*eks.DescribeClusterOutput, error)
	createFn   func(ctx context.Context, in *eks.CreateAddonInput) (*eks.CreateAddonOutput, error)
	updateFn   func(ctx context.Context, in *eks.UpdateAddonInput) (*eks.UpdateAddonOutput, error)
	deleteFn   func(ctx context.Context, in *eks.DeleteAddonInput) (*eks.DeleteAddonOutput, error)
}

func (f *fakeEKSAddonsClient) ListAddons(ctx context.Context, in *eks.ListAddonsInput, _ ...func(*eks.Options)) (*eks.ListAddonsOutput, error) {
	atomic.AddInt32(&f.listAddonsCalls, 1)
	if f.listFn == nil {
		return nil, errors.New("listFn not set")
	}
	return f.listFn(ctx, in)
}

func (f *fakeEKSAddonsClient) DescribeAddon(ctx context.Context, in *eks.DescribeAddonInput, _ ...func(*eks.Options)) (*eks.DescribeAddonOutput, error) {
	atomic.AddInt32(&f.descAddonCalls, 1)
	if f.descFn == nil {
		return nil, errors.New("descFn not set")
	}
	return f.descFn(ctx, in)
}

func (f *fakeEKSAddonsClient) DescribeAddonVersions(ctx context.Context, in *eks.DescribeAddonVersionsInput, _ ...func(*eks.Options)) (*eks.DescribeAddonVersionsOutput, error) {
	atomic.AddInt32(&f.descVersionsCals, 1)
	if f.versionsFn == nil {
		return &eks.DescribeAddonVersionsOutput{}, nil
	}
	return f.versionsFn(ctx, in)
}

func (f *fakeEKSAddonsClient) DescribeAddonConfiguration(ctx context.Context, in *eks.DescribeAddonConfigurationInput, _ ...func(*eks.Options)) (*eks.DescribeAddonConfigurationOutput, error) {
	atomic.AddInt32(&f.descConfigCalls, 1)
	if f.configFn == nil {
		return &eks.DescribeAddonConfigurationOutput{}, nil
	}
	return f.configFn(ctx, in)
}

func (f *fakeEKSAddonsClient) DescribeCluster(ctx context.Context, in *eks.DescribeClusterInput, _ ...func(*eks.Options)) (*eks.DescribeClusterOutput, error) {
	atomic.AddInt32(&f.descClusterCalls, 1)
	if f.clusterFn == nil {
		return &eks.DescribeClusterOutput{
			Cluster: &ekstypes.Cluster{Version: strPtrAddons("1.29")},
		}, nil
	}
	return f.clusterFn(ctx, in)
}

func (f *fakeEKSAddonsClient) CreateAddon(ctx context.Context, in *eks.CreateAddonInput, _ ...func(*eks.Options)) (*eks.CreateAddonOutput, error) {
	atomic.AddInt32(&f.createAddonCalls, 1)
	if f.createFn == nil {
		return nil, errors.New("createFn not set")
	}
	return f.createFn(ctx, in)
}

func (f *fakeEKSAddonsClient) UpdateAddon(ctx context.Context, in *eks.UpdateAddonInput, _ ...func(*eks.Options)) (*eks.UpdateAddonOutput, error) {
	atomic.AddInt32(&f.updateAddonCalls, 1)
	if f.updateFn == nil {
		return nil, errors.New("updateFn not set")
	}
	return f.updateFn(ctx, in)
}

func (f *fakeEKSAddonsClient) DeleteAddon(ctx context.Context, in *eks.DeleteAddonInput, _ ...func(*eks.Options)) (*eks.DeleteAddonOutput, error) {
	atomic.AddInt32(&f.deleteAddonCalls, 1)
	if f.deleteFn == nil {
		return nil, errors.New("deleteFn not set")
	}
	return f.deleteFn(ctx, in)
}

func withFakeEKSAddonsClient(t *testing.T, fake *fakeEKSAddonsClient) {
	t.Helper()
	orig := newEKSAddonsClient
	newEKSAddonsClient = func(_ credentials.Provider, _ clusters.Cluster) eksAddonsAPI {
		return fake
	}
	t.Cleanup(func() { newEKSAddonsClient = orig })
}

func invokeAddons(t *testing.T, reg *clusters.Registry, cache *eksAddonsCache, versions *addonVersionsCache, sink *recordingSink, method, url string, params map[string]string, isDetail bool) *httptest.ResponseRecorder {
	t.Helper()
	emitter := audit.New(sink)
	var h credentials.Handler
	if isDetail {
		h = eksAddonsGetHandler(reg, cache, versions, emitter)
	} else {
		h = eksAddonsListHandler(reg, cache, versions, emitter)
	}
	req := httptest.NewRequest(method, url, http.NoBody)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	req = req.WithContext(credentials.WithSession(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		credentials.Session{Subject: "alice@corp", Email: "alice@corp", Groups: []string{"eng"}},
	))
	rec := httptest.NewRecorder()
	h(rec, req, fakeProvider{actor: "alice@corp"})
	return rec
}

func strPtrAddons(s string) *string { return &s }

// mkAddon builds a minimal ekstypes.Addon for fakeFn returns.
func mkAddon(name, version, status string, issues int) *ekstypes.Addon {
	a := &ekstypes.Addon{
		AddonName:    strPtrAddons(name),
		AddonVersion: strPtrAddons(version),
		Status:       ekstypes.AddonStatus(status),
	}
	if issues > 0 {
		h := &ekstypes.AddonHealth{}
		for i := 0; i < issues; i++ {
			h.Issues = append(h.Issues, ekstypes.AddonIssue{
				Code:    ekstypes.AddonIssueCodeAccessDenied,
				Message: strPtrAddons("issue"),
			})
		}
		a.Health = h
	}
	return a
}

// versionsResponse wraps a DescribeAddonVersionsOutput for one addon
// with a static set of (version, compatK8s, default) tuples. The
// outermost AddonInfo's AddonName matches the input filter.
func versionsResponse(addon string, vers []struct {
	Ver       string
	K8s       []string
	IsDefault bool
}) *eks.DescribeAddonVersionsOutput {
	info := ekstypes.AddonInfo{AddonName: strPtrAddons(addon)}
	for _, v := range vers {
		entry := ekstypes.AddonVersionInfo{AddonVersion: strPtrAddons(v.Ver)}
		for _, k := range v.K8s {
			entry.Compatibilities = append(entry.Compatibilities, ekstypes.Compatibility{
				ClusterVersion: strPtrAddons(k),
				DefaultVersion: v.IsDefault,
			})
		}
		info.AddonVersions = append(info.AddonVersions, entry)
	}
	return &eks.DescribeAddonVersionsOutput{Addons: []ekstypes.AddonInfo{info}}
}

// ── List endpoint ────────────────────────────────────────────────────

func TestEKSAddonsList_HappyPath(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	fake := &fakeEKSAddonsClient{
		listFn: func(_ context.Context, in *eks.ListAddonsInput) (*eks.ListAddonsOutput, error) {
			if in.ClusterName == nil || *in.ClusterName != "prod-eu-west-1" {
				t.Errorf("cluster name = %v", in.ClusterName)
			}
			return &eks.ListAddonsOutput{
				Addons: []string{"vpc-cni", "coredns", "kube-proxy"},
			}, nil
		},
		descFn: func(_ context.Context, in *eks.DescribeAddonInput) (*eks.DescribeAddonOutput, error) {
			switch *in.AddonName {
			case "vpc-cni":
				// Stale: installed v1.16.4 but catalog has v1.18.0 as
				// latest, and v1.16.4 doesn't list 1.30 in compat →
				// updateAvailable + blocksNextMinor.
				return &eks.DescribeAddonOutput{Addon: mkAddon("vpc-cni", "v1.16.4", "ACTIVE", 0)}, nil
			case "coredns":
				return &eks.DescribeAddonOutput{Addon: mkAddon("coredns", "v1.10.1", "ACTIVE", 0)}, nil
			case "kube-proxy":
				// Health issue → fail glyph.
				return &eks.DescribeAddonOutput{Addon: mkAddon("kube-proxy", "v1.29.3", "DEGRADED", 1)}, nil
			}
			return nil, errors.New("unexpected addon")
		},
		versionsFn: func(_ context.Context, in *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
			switch *in.AddonName {
			case "vpc-cni":
				return versionsResponse("vpc-cni", []struct {
					Ver       string
					K8s       []string
					IsDefault bool
				}{
					{Ver: "v1.18.0", K8s: []string{"1.27", "1.28", "1.29", "1.30"}, IsDefault: true},
					{Ver: "v1.16.4", K8s: []string{"1.27", "1.28", "1.29"}}, // no 1.30
				}), nil
			case "coredns":
				return versionsResponse("coredns", []struct {
					Ver       string
					K8s       []string
					IsDefault bool
				}{
					{Ver: "v1.10.1", K8s: []string{"1.27", "1.28", "1.29", "1.30"}, IsDefault: true},
				}), nil
			case "kube-proxy":
				return versionsResponse("kube-proxy", []struct {
					Ver       string
					K8s       []string
					IsDefault bool
				}{
					{Ver: "v1.29.3", K8s: []string{"1.29", "1.30"}, IsDefault: true},
				}), nil
			}
			return &eks.DescribeAddonVersionsOutput{}, nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSAddonsCache(time.Hour)
	versions := newAddonVersionsCache(time.Hour)
	rec := invokeAddons(t, reg, cache, versions, sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/addons",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got AddonsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ClusterKubernetesVersion != "1.29" {
		t.Errorf("ClusterKubernetesVersion = %q, want 1.29", got.ClusterKubernetesVersion)
	}
	if len(got.Addons) != 3 {
		t.Fatalf("Addons = %d, want 3", len(got.Addons))
	}
	// Sort: kube-proxy first (fail), then vpc-cni (blocks), then coredns (ok).
	if got.Addons[0].Name != "kube-proxy" {
		t.Errorf("first row = %q, want kube-proxy (unhealthy first)", got.Addons[0].Name)
	}
	if got.Addons[1].Name != "vpc-cni" {
		t.Errorf("second row = %q, want vpc-cni (blocks-next-minor)", got.Addons[1].Name)
	}

	// Per-addon assertions.
	byName := map[string]AddonSummary{}
	for _, a := range got.Addons {
		byName[a.Name] = a
	}
	if v := byName["vpc-cni"]; !v.UpdateAvailable || !v.BlocksNextMinor || v.HealthGlyph != "update" || v.LatestVersion != "v1.18.0" {
		t.Errorf("vpc-cni summary wrong: %+v", v)
	}
	if v := byName["coredns"]; v.UpdateAvailable || v.BlocksNextMinor || v.HealthGlyph != "ok" {
		t.Errorf("coredns summary wrong: %+v", v)
	}
	if v := byName["kube-proxy"]; v.HealthGlyph != "fail" || v.HealthIssueCount != 1 {
		t.Errorf("kube-proxy summary wrong: %+v", v)
	}

	wantCounts := AddonsCounts{Total: 3, Healthy: 1, UpdateAvailable: 1, Unhealthy: 1, BlocksNextMinor: 1}
	if got.Counts != wantCounts {
		t.Errorf("Counts = %+v, want %+v", got.Counts, wantCounts)
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Verb != audit.VerbEKSAddonsRead {
		t.Errorf("Verb = %q, want eks_addons_read", ev.Verb)
	}
	if ev.Outcome != audit.OutcomeSuccess {
		t.Errorf("Outcome = %q, want success", ev.Outcome)
	}
	if op, _ := ev.Extra["op"].(string); op != "list" {
		t.Errorf("Extra[op] = %q, want list", op)
	}
}

func TestEKSAddonsList_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "kind-local", clusters.BackendInCluster)
	sink := &recordingSink{}
	rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
		sink, http.MethodGet,
		"/api/clusters/kind-local/eks/addons",
		map[string]string{"cluster": "kind-local"}, false)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
	var body apiError
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != errBackendNotEKSCode {
		t.Errorf("code = %q, want %q", body.Code, errBackendNotEKSCode)
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("expected zero audit rows for non-EKS branch, got %d", len(events))
	}
}

// CapableNonEKSBackends asserts the EKS gate reflects ARN+Region
// capability, not the K8s-auth backend. Mirror the same regression
// cover as TestEKSInsightsList_CapableNonEKSBackends.
func TestEKSAddonsList_CapableNonEKSBackends(t *testing.T) {
	cases := []struct {
		name, backend, cluster string
	}{
		{"in-cluster + arn", "in-cluster+arn", "self"},
		{"agent + arn", "agent+arn", "pre-prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := eksRegistry(t, tc.cluster, tc.backend)
			fake := &fakeEKSAddonsClient{
				listFn: func(_ context.Context, _ *eks.ListAddonsInput) (*eks.ListAddonsOutput, error) {
					return &eks.ListAddonsOutput{}, nil
				},
			}
			withFakeEKSAddonsClient(t, fake)
			rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
				&recordingSink{}, http.MethodGet,
				"/api/clusters/"+tc.cluster+"/eks/addons",
				map[string]string{"cluster": tc.cluster}, false)
			if rec.Code == http.StatusUnprocessableEntity {
				t.Fatalf("status = 422 (gate rejected capable cluster); want non-422")
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestEKSAddonsList_ClusterNotFound(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
		&recordingSink{}, http.MethodGet,
		"/api/clusters/missing/eks/addons",
		map[string]string{"cluster": "missing"}, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEKSAddonsList_AWSErrorEmitsFailureAudit(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSAddonsClient{
		listFn: func(_ context.Context, _ *eks.ListAddonsInput) (*eks.ListAddonsOutput, error) {
			return nil, errors.New("AccessDenied: ListAddons")
		},
	}
	withFakeEKSAddonsClient(t, fake)

	sink := &recordingSink{}
	rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
		sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/addons",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	if events[0].Outcome != audit.OutcomeFailure {
		t.Errorf("Outcome = %q, want failure", events[0].Outcome)
	}
}

func TestEKSAddonsList_ClassifiesAWSErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "AccessDenied → 403/E_AWS_FORBIDDEN",
			err:        &ekstypes.AccessDeniedException{Message: strPtrAddons("denied")},
			wantStatus: http.StatusForbidden,
			wantCode:   "E_AWS_FORBIDDEN",
		},
		{
			name:       "ResourceNotFound → 404/E_AWS_NOT_FOUND",
			err:        &ekstypes.ResourceNotFoundException{Message: strPtrAddons("missing")},
			wantStatus: http.StatusNotFound,
			wantCode:   "E_AWS_NOT_FOUND",
		},
		{
			name:       "Throttling → 429/E_AWS_THROTTLED",
			err:        &ekstypes.ThrottlingException{Message: strPtrAddons("slow down")},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "E_AWS_THROTTLED",
		},
		{
			name:       "unknown error → 502/E_AWS_API",
			err:        errors.New("opaque transport blip"),
			wantStatus: http.StatusBadGateway,
			wantCode:   "E_AWS_API",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
			fake := &fakeEKSAddonsClient{
				clusterFn: func(_ context.Context, _ *eks.DescribeClusterInput) (*eks.DescribeClusterOutput, error) {
					return nil, tc.err
				},
			}
			withFakeEKSAddonsClient(t, fake)

			rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
				&recordingSink{}, http.MethodGet,
				"/api/clusters/prod-eu-west-1/eks/addons",
				map[string]string{"cluster": "prod-eu-west-1"}, false)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantCode) {
				t.Errorf("body missing %q: %s", tc.wantCode, rec.Body.String())
			}
		})
	}
}

func TestEKSAddonsList_CacheHit(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	cache := newEKSAddonsCache(time.Hour)
	cache.PutList("prod-eu-west-1", AddonsListResponse{
		Addons: []AddonSummary{{Name: "cached", HealthGlyph: "ok"}},
		Counts: AddonsCounts{Total: 1, Healthy: 1},
	})

	fake := &fakeEKSAddonsClient{}
	withFakeEKSAddonsClient(t, fake)
	sink := &recordingSink{}

	rec := invokeAddons(t, reg, cache, newAddonVersionsCache(time.Hour), sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/addons",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if atomic.LoadInt32(&fake.listAddonsCalls) != 0 {
		t.Errorf("expected no AWS calls on cache hit; got %d", fake.listAddonsCalls)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Extra["op"] != "list:cache_hit" {
		t.Errorf("expected cache_hit audit row; got %+v", events)
	}
}

// TestEKSAddonsList_ParallelFanoutRaceClean asserts the parallel
// fan-out is race-clean. The handler dispatches DescribeAddon per
// addon name in goroutines; with -race this catches a missed mutex
// or shared-slot bug.
func TestEKSAddonsList_ParallelFanoutRaceClean(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	const N = 16
	names := make([]string, N)
	for i := 0; i < N; i++ {
		names[i] = "addon-" + strconv.Itoa(i)
	}
	fake := &fakeEKSAddonsClient{
		listFn: func(_ context.Context, _ *eks.ListAddonsInput) (*eks.ListAddonsOutput, error) {
			return &eks.ListAddonsOutput{Addons: names}, nil
		},
		descFn: func(_ context.Context, in *eks.DescribeAddonInput) (*eks.DescribeAddonOutput, error) {
			// Tiny sleep encourages goroutines to interleave — gives
			// -race a chance to spot any concurrent writer hazard.
			time.Sleep(time.Millisecond)
			return &eks.DescribeAddonOutput{Addon: mkAddon(*in.AddonName, "v1.0.0", "ACTIVE", 0)}, nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
		&recordingSink{}, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/addons",
		map[string]string{"cluster": "prod-eu-west-1"}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var got AddonsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Addons) != N {
		t.Errorf("addons = %d, want %d", len(got.Addons), N)
	}
	// Every name should appear exactly once — proves no slot was
	// double-written or dropped.
	seen := map[string]int{}
	for _, a := range got.Addons {
		seen[a.Name]++
	}
	for _, name := range names {
		if seen[name] != 1 {
			t.Errorf("name %q appeared %d times", name, seen[name])
		}
	}
}

// TestEKSAddonsList_AddonVersionsCacheShared asserts the shared
// catalog cache is consulted before AWS. Two clusters running the
// same addon name should result in one DescribeAddonVersions call.
func TestEKSAddonsList_AddonVersionsCacheShared(t *testing.T) {
	regA := eksRegistry(t, "cluster-a", clusters.BackendEKS)
	regB := eksRegistry(t, "cluster-b", clusters.BackendEKS)

	fake := &fakeEKSAddonsClient{
		listFn: func(_ context.Context, _ *eks.ListAddonsInput) (*eks.ListAddonsOutput, error) {
			return &eks.ListAddonsOutput{Addons: []string{"vpc-cni"}}, nil
		},
		descFn: func(_ context.Context, _ *eks.DescribeAddonInput) (*eks.DescribeAddonOutput, error) {
			return &eks.DescribeAddonOutput{Addon: mkAddon("vpc-cni", "v1.18.0", "ACTIVE", 0)}, nil
		},
		versionsFn: func(_ context.Context, _ *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
			return versionsResponse("vpc-cni", []struct {
				Ver       string
				K8s       []string
				IsDefault bool
			}{
				{Ver: "v1.18.0", K8s: []string{"1.29", "1.30"}, IsDefault: true},
			}), nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	sharedVersions := newAddonVersionsCache(time.Hour)
	for _, reg := range []*clusters.Registry{regA, regB} {
		cache := newEKSAddonsCache(time.Hour)
		// Each cluster gets its own per-cluster cache so the addons
		// list is fetched fresh; the catalog cache is shared.
		var name string
		switch reg {
		case regA:
			name = "cluster-a"
		case regB:
			name = "cluster-b"
		}
		rec := invokeAddons(t, reg, cache, sharedVersions, &recordingSink{}, http.MethodGet,
			"/api/clusters/"+name+"/eks/addons",
			map[string]string{"cluster": name}, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d for %s", rec.Code, name)
		}
	}
	if got := atomic.LoadInt32(&fake.descVersionsCals); got != 1 {
		t.Errorf("DescribeAddonVersions calls = %d, want 1 (shared cache should serve cluster-b)", got)
	}
}

// ── Detail endpoint ──────────────────────────────────────────────────

func TestEKSAddonsDetail_HappyPath(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	now := time.Now().UTC()
	fake := &fakeEKSAddonsClient{
		descFn: func(_ context.Context, in *eks.DescribeAddonInput) (*eks.DescribeAddonOutput, error) {
			if *in.AddonName != "vpc-cni" {
				t.Errorf("addon name = %v", in.AddonName)
			}
			return &eks.DescribeAddonOutput{Addon: &ekstypes.Addon{
				AddonName:             strPtrAddons("vpc-cni"),
				AddonVersion:          strPtrAddons("v1.16.4"),
				AddonArn:              strPtrAddons("arn:aws:eks:eu-west-1:111111111111:addon/prod/vpc-cni/abcd"),
				ServiceAccountRoleArn: strPtrAddons("arn:aws:iam::111111111111:role/vpc-cni"),
				Status:                ekstypes.AddonStatusActive,
				CreatedAt:             &now,
				ModifiedAt:            &now,
				Owner:                 strPtrAddons("aws"),
				Publisher:             strPtrAddons("eks"),
			}}, nil
		},
		versionsFn: func(_ context.Context, _ *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
			return versionsResponse("vpc-cni", []struct {
				Ver       string
				K8s       []string
				IsDefault bool
			}{
				{Ver: "v1.18.0", K8s: []string{"1.27", "1.28", "1.29", "1.30"}, IsDefault: true},
				{Ver: "v1.16.4", K8s: []string{"1.27", "1.28", "1.29"}},
			}), nil
		},
		configFn: func(_ context.Context, _ *eks.DescribeAddonConfigurationInput) (*eks.DescribeAddonConfigurationOutput, error) {
			return &eks.DescribeAddonConfigurationOutput{
				ConfigurationSchema: strPtrAddons(`{"type":"object"}`),
			}, nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	sink := &recordingSink{}
	rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
		sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/addons/vpc-cni",
		map[string]string{"cluster": "prod-eu-west-1", "name": "vpc-cni"}, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var got AddonDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "vpc-cni" || got.Version != "v1.16.4" {
		t.Errorf("got = %+v", got)
	}
	if !got.UpdateAvailable || !got.BlocksNextMinor {
		t.Errorf("expected UpdateAvailable + BlocksNextMinor; got %+v", got.AddonSummary)
	}
	if got.LatestVersion != "v1.18.0" {
		t.Errorf("LatestVersion = %q, want v1.18.0", got.LatestVersion)
	}
	if got.CompatMinK8s != "1.27" || got.CompatMaxK8s != "1.29" {
		t.Errorf("compat range = (%q, %q), want (1.27, 1.29)", got.CompatMinK8s, got.CompatMaxK8s)
	}
	if got.ServiceAccountRoleARN == "" {
		t.Errorf("expected ServiceAccountRoleARN populated")
	}
	if got.ConfigurationSchema == "" {
		t.Errorf("expected ConfigurationSchema populated")
	}
	if len(got.AvailableVersions) != 2 {
		t.Errorf("AvailableVersions = %d, want 2", len(got.AvailableVersions))
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	if op, _ := events[0].Extra["op"].(string); op != "detail" {
		t.Errorf("op = %q, want detail", op)
	}
}

func TestEKSAddonsDetail_PodIdentityAssociationsExposed(t *testing.T) {
	// Regression: AWS DescribeAddon returns the addon's Pod Identity
	// associations in PodIdentityAssociations []string. The detail
	// handler used to drop these on the floor — frontend had no way
	// to surface "this addon uses Pod Identity instead of IRSA."
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()
	piARNs := []string{
		"arn:aws:eks:eu-west-1:111111111111:podidentityassociation/prod/a-abc123",
		"arn:aws:eks:eu-west-1:111111111111:podidentityassociation/prod/a-def456",
	}
	fake := &fakeEKSAddonsClient{
		descFn: func(_ context.Context, _ *eks.DescribeAddonInput) (*eks.DescribeAddonOutput, error) {
			return &eks.DescribeAddonOutput{Addon: &ekstypes.Addon{
				AddonName:               strPtrAddons("aws-ebs-csi-driver"),
				AddonVersion:            strPtrAddons("v1.59.0-eksbuild.1"),
				Status:                  ekstypes.AddonStatusActive,
				CreatedAt:               &now,
				ModifiedAt:              &now,
				PodIdentityAssociations: piARNs,
			}}, nil
		},
	}
	withFakeEKSAddonsClient(t, fake)
	rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
		&recordingSink{}, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/addons/aws-ebs-csi-driver",
		map[string]string{"cluster": "prod-eu-west-1", "name": "aws-ebs-csi-driver"}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var got AddonDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.PodIdentityAssociations) != 2 {
		t.Fatalf("PodIdentityAssociations len = %d, want 2", len(got.PodIdentityAssociations))
	}
	if got.PodIdentityAssociations[0] != piARNs[0] || got.PodIdentityAssociations[1] != piARNs[1] {
		t.Errorf("PodIdentityAssociations = %+v, want %+v", got.PodIdentityAssociations, piARNs)
	}
}

func TestEKSAddonsDetail_MissingNameReturns400(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
		&recordingSink{}, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/addons/",
		map[string]string{"cluster": "prod-eu-west-1", "name": ""}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEKSAddonsDetail_ConfigurationSoftFail(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSAddonsClient{
		descFn: func(_ context.Context, _ *eks.DescribeAddonInput) (*eks.DescribeAddonOutput, error) {
			return &eks.DescribeAddonOutput{Addon: mkAddon("coredns", "v1.10.1", "ACTIVE", 0)}, nil
		},
		configFn: func(_ context.Context, _ *eks.DescribeAddonConfigurationInput) (*eks.DescribeAddonConfigurationOutput, error) {
			return nil, &ekstypes.AccessDeniedException{Message: strPtrAddons("denied")}
		},
	}
	withFakeEKSAddonsClient(t, fake)

	rec := invokeAddons(t, reg, newEKSAddonsCache(time.Hour), newAddonVersionsCache(time.Hour),
		&recordingSink{}, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/addons/coredns",
		map[string]string{"cluster": "prod-eu-west-1", "name": "coredns"}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (config schema is soft-fail); status = %d, body = %s",
			rec.Code, rec.Body.String())
	}
	var got AddonDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ConfigurationSchema != "" {
		t.Errorf("ConfigurationSchema = %q, want empty", got.ConfigurationSchema)
	}
}

// ── Helper unit tests ────────────────────────────────────────────────

func TestParseK8sMinor(t *testing.T) {
	cases := []struct {
		in       string
		wantMaj  int
		wantMin  int
		wantOk   bool
	}{
		{"1.29", 1, 29, true},
		{"1.30", 1, 30, true},
		{"1.10", 1, 10, true},
		{"1.29.1+eksbuild.1", 1, 29, true},
		{"", 0, 0, false},
		{"1", 0, 0, false},
		{"abc", 0, 0, false},
		{"1.x", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			maj, min, ok := parseK8sMinor(tc.in)
			if ok != tc.wantOk || maj != tc.wantMaj || min != tc.wantMin {
				t.Errorf("(%d, %d, %v), want (%d, %d, %v)", maj, min, ok, tc.wantMaj, tc.wantMin, tc.wantOk)
			}
		})
	}
}

func TestNextMinor(t *testing.T) {
	cases := map[string]string{
		"1.29":  "1.30",
		"1.9":   "1.10",
		"1.30":  "1.31",
		"":      "",
		"bogus": "",
	}
	for in, want := range cases {
		if got := nextMinor(in); got != want {
			t.Errorf("nextMinor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompatRange(t *testing.T) {
	cases := []struct {
		in       []string
		wantMin  string
		wantMax  string
	}{
		{[]string{"1.27", "1.28", "1.29"}, "1.27", "1.29"},
		{[]string{"1.30", "1.27"}, "1.27", "1.30"},
		{[]string{}, "", ""},
		{[]string{"bogus"}, "", ""},
	}
	for _, tc := range cases {
		gotMin, gotMax := compatRange(tc.in)
		if gotMin != tc.wantMin || gotMax != tc.wantMax {
			t.Errorf("compatRange(%v) = (%q, %q), want (%q, %q)", tc.in, gotMin, gotMax, tc.wantMin, tc.wantMax)
		}
	}
}
