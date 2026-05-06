package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// fakeEKSInsightsClient implements eksInsightsAPI. Both ListInsights
// and DescribeInsight are pluggable per-test; the default returns
// errors so a test that forgot to wire one fails loudly.
type fakeEKSInsightsClient struct {
	mu        sync.Mutex
	listCalls int
	descCalls int
	listFn    func(ctx context.Context, in *eks.ListInsightsInput) (*eks.ListInsightsOutput, error)
	descFn    func(ctx context.Context, in *eks.DescribeInsightInput) (*eks.DescribeInsightOutput, error)
}

func (f *fakeEKSInsightsClient) ListInsights(ctx context.Context, in *eks.ListInsightsInput, _ ...func(*eks.Options)) (*eks.ListInsightsOutput, error) {
	f.mu.Lock()
	f.listCalls++
	f.mu.Unlock()
	if f.listFn == nil {
		return nil, errors.New("listFn not set")
	}
	return f.listFn(ctx, in)
}

func (f *fakeEKSInsightsClient) DescribeInsight(ctx context.Context, in *eks.DescribeInsightInput, _ ...func(*eks.Options)) (*eks.DescribeInsightOutput, error) {
	f.mu.Lock()
	f.descCalls++
	f.mu.Unlock()
	if f.descFn == nil {
		return nil, errors.New("descFn not set")
	}
	return f.descFn(ctx, in)
}

// withFakeEKSClient swaps the package-level client constructor for
// the duration of the test, returning a cleanup that restores the
// original. Mirrors how cani_handler_test.go stubs SAR/SSRR fns.
func withFakeEKSClient(t *testing.T, fake *fakeEKSInsightsClient) {
	t.Helper()
	orig := newEKSInsightsClient
	newEKSInsightsClient = func(_ credentials.Provider, _ clusters.Cluster) eksInsightsAPI {
		return fake
	}
	t.Cleanup(func() { newEKSInsightsClient = orig })
}

// eksRegistry writes a one-cluster YAML with the given backend and
// (for EKS) a matching ARN/region. Mirrors bulkDownloadRegistry.
func eksRegistry(t *testing.T, name, backend string) *clusters.Registry {
	t.Helper()
	dir := t.TempDir()
	var yaml string
	switch backend {
	case clusters.BackendEKS:
		yaml = "clusters:\n" +
			"  - name: " + name + "\n" +
			"    backend: eks\n" +
			"    arn: arn:aws:eks:eu-west-1:111111111111:cluster/" + name + "\n" +
			"    region: eu-west-1\n"
	case clusters.BackendInCluster:
		yaml = "clusters:\n" +
			"  - name: " + name + "\n" +
			"    backend: in-cluster\n"
	case clusters.BackendAgent:
		yaml = "clusters:\n" +
			"  - name: " + name + "\n" +
			"    backend: agent\n"
	case "in-cluster+arn":
		// in-cluster cluster that ALSO supplies the EKS ARN+Region —
		// the supported pattern when the server runs inside an EKS
		// cluster and uses its ServiceAccount for K8s auth, but
		// still wants EKS Insights / Node Groups via Pod Identity.
		yaml = "clusters:\n" +
			"  - name: " + name + "\n" +
			"    backend: in-cluster\n" +
			"    arn: arn:aws:eks:eu-west-1:111111111111:cluster/" + name + "\n" +
			"    region: eu-west-1\n"
	case "agent+arn":
		// agent-backed cluster with EKS metadata — K8s traffic flows
		// over the tunnel, AWS API traffic goes server → AWS.
		yaml = "clusters:\n" +
			"  - name: " + name + "\n" +
			"    backend: agent\n" +
			"    arn: arn:aws:eks:eu-west-1:111111111111:cluster/" + name + "\n" +
			"    region: eu-west-1\n"
	default:
		t.Fatalf("eksRegistry: unhandled backend %q", backend)
	}
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

// invokeInsights drives a list or detail request with a planted
// session and returns the recorder + the recording sink.
func invokeInsights(t *testing.T, reg *clusters.Registry, cache *eksInsightsCache, sink *recordingSink, method, url string, params map[string]string, isDetail bool) *httptest.ResponseRecorder {
	t.Helper()
	emitter := audit.New(sink)
	var h credentials.Handler
	if isDetail {
		h = eksInsightsGetHandler(reg, cache, emitter)
	} else {
		h = eksInsightsListHandler(reg, cache, emitter)
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

// ── List endpoint ────────────────────────────────────────────────────

func TestEKSInsightsList_HappyPath(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	now := time.Now().UTC()
	fake := &fakeEKSInsightsClient{
		listFn: func(_ context.Context, in *eks.ListInsightsInput) (*eks.ListInsightsOutput, error) {
			// Confirm the handler filters the AWS-side query for us.
			if in.Filter == nil || len(in.Filter.Categories) != 1 || in.Filter.Categories[0] != ekstypes.CategoryUpgradeReadiness {
				t.Errorf("expected UPGRADE_READINESS filter, got %+v", in.Filter)
			}
			if in.ClusterName == nil || *in.ClusterName != "prod-eu-west-1" {
				t.Errorf("expected cluster name from ARN; got %v", in.ClusterName)
			}
			return &eks.ListInsightsOutput{
				Insights: []ekstypes.InsightSummary{
					mkSummary("i-1", "Cluster health", "1.32", ekstypes.InsightStatusValuePassing, &now),
					mkSummary("i-2", "Deprecated APIs", "1.32", ekstypes.InsightStatusValueError, &now),
					mkSummary("i-3", "Add-on compat", "1.32", ekstypes.InsightStatusValueWarning, &now),
					mkSummary("i-4", "IAM", "1.32", ekstypes.InsightStatusValuePassing, &now),
				},
			}, nil
		},
	}
	withFakeEKSClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)
	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/upgrade-insights",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got UpgradeInsightsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Insights) != 4 {
		t.Fatalf("Insights = %d, want 4", len(got.Insights))
	}
	wantCounts := UpgradeInsightCounts{Passing: 2, Warning: 1, Error: 1}
	if got.Counts != wantCounts {
		t.Errorf("Counts = %+v, want %+v", got.Counts, wantCounts)
	}
	if got.TargetKubernetesVersion != "1.32" {
		t.Errorf("TargetKubernetesVersion = %q, want 1.32", got.TargetKubernetesVersion)
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Verb != audit.VerbEKSInsightsRead {
		t.Errorf("Verb = %q, want eks_insights_read", ev.Verb)
	}
	if ev.Outcome != audit.OutcomeSuccess {
		t.Errorf("Outcome = %q, want success", ev.Outcome)
	}
	if ev.Cluster != "prod-eu-west-1" {
		t.Errorf("Cluster = %q", ev.Cluster)
	}
	if op, _ := ev.Extra["op"].(string); op != "list" {
		t.Errorf("Extra[op] = %q, want list", op)
	}
}

func TestEKSInsightsList_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "kind-local", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)

	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/kind-local/eks/upgrade-insights",
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
	// No audit row for the non-EKS branch — it's a SPA-side feature
	// gate, not a privileged action.
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("expected zero audit rows, got %d", len(events))
	}
}

func TestEKSInsightsList_AgentBackendReturns422(t *testing.T) {
	reg := eksRegistry(t, "edge-1", clusters.BackendAgent)
	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)
	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/edge-1/eks/upgrade-insights",
		map[string]string{"cluster": "edge-1"}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

// TestEKSInsightsList_CapableNonEKSBackends asserts the EKS gate
// reflects ARN+Region capability rather than the K8s-auth backend.
// in-cluster and agent backends with EKS metadata should bypass the
// 422 envelope and reach the AWS call (here served by a fake).
// Regression cover for v1.0.3-rc1 where isEKSBackend rejected any
// non-eks backend regardless of ARN.
func TestEKSInsightsList_CapableNonEKSBackends(t *testing.T) {
	cases := []struct {
		name    string
		backend string
		cluster string
	}{
		{"in-cluster + arn", "in-cluster+arn", "self"},
		{"agent + arn", "agent+arn", "pre-prod"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := eksRegistry(t, tc.cluster, tc.backend)
			fake := &fakeEKSInsightsClient{
				listFn: func(_ context.Context, _ *eks.ListInsightsInput) (*eks.ListInsightsOutput, error) {
					return &eks.ListInsightsOutput{}, nil
				},
			}
			withFakeEKSClient(t, fake)
			sink := &recordingSink{}
			cache := newEKSInsightsCache(time.Hour)
			rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
				"/api/clusters/"+tc.cluster+"/eks/upgrade-insights",
				map[string]string{"cluster": tc.cluster}, false)
			if rec.Code == http.StatusUnprocessableEntity {
				t.Fatalf("status = 422 (gate rejected capable cluster); want non-422")
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
		})
	}
}

func TestEKSInsightsList_ClusterNotFound(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)
	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/missing/eks/upgrade-insights",
		map[string]string{"cluster": "missing"}, false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEKSInsightsList_AWSErrorEmitsFailureAudit(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSInsightsClient{
		listFn: func(_ context.Context, _ *eks.ListInsightsInput) (*eks.ListInsightsOutput, error) {
			return nil, errors.New("AccessDenied: ListInsights")
		},
	}
	withFakeEKSClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)
	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/upgrade-insights",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body = %s", rec.Code, rec.Body.String())
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	ev := events[0]
	if ev.Outcome != audit.OutcomeFailure {
		t.Errorf("Outcome = %q, want failure", ev.Outcome)
	}
	if ev.Reason == "" {
		t.Errorf("expected non-empty Reason")
	}
}

// TestEKSInsightsList_ClassifiesAWSErrors covers the awsErrorToStatus
// fold-in: each smithy-typed exception lands on the right HTTP status
// + stable error code so the SPA can branch on them. A non-smithy
// error keeps the legacy 502/E_AWS_API behavior.
func TestEKSInsightsList_ClassifiesAWSErrors(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "AccessDenied → 403/E_AWS_FORBIDDEN",
			err:        &ekstypes.AccessDeniedException{Message: strPtr("denied")},
			wantStatus: http.StatusForbidden,
			wantCode:   "E_AWS_FORBIDDEN",
		},
		{
			name:       "ResourceNotFound → 404/E_AWS_NOT_FOUND",
			err:        &ekstypes.ResourceNotFoundException{Message: strPtr("missing")},
			wantStatus: http.StatusNotFound,
			wantCode:   "E_AWS_NOT_FOUND",
		},
		{
			name:       "Throttling → 429/E_AWS_THROTTLED",
			err:        &ekstypes.ThrottlingException{Message: strPtr("slow down")},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "E_AWS_THROTTLED",
		},
		{
			name:       "unknown error → 502/E_AWS_API (legacy default preserved)",
			err:        errors.New("opaque transport blip"),
			wantStatus: http.StatusBadGateway,
			wantCode:   "E_AWS_API",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
			fake := &fakeEKSInsightsClient{
				listFn: func(_ context.Context, _ *eks.ListInsightsInput) (*eks.ListInsightsOutput, error) {
					return nil, tc.err
				},
			}
			withFakeEKSClient(t, fake)

			rec := invokeInsights(t, reg, newEKSInsightsCache(time.Hour),
				&recordingSink{}, http.MethodGet,
				"/api/clusters/prod-eu-west-1/eks/upgrade-insights",
				map[string]string{"cluster": "prod-eu-west-1"}, false)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s",
					rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantCode) {
				t.Errorf("body missing %q: %s", tc.wantCode, rec.Body.String())
			}
		})
	}
}

func strPtr(s string) *string { return &s }

func TestEKSInsightsList_CacheHitSkipsAWS(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSInsightsClient{
		listFn: func(_ context.Context, _ *eks.ListInsightsInput) (*eks.ListInsightsOutput, error) {
			return &eks.ListInsightsOutput{
				Insights: []ekstypes.InsightSummary{
					mkSummary("i-1", "ok", "1.32", ekstypes.InsightStatusValuePassing, nil),
				},
			}, nil
		},
	}
	withFakeEKSClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)

	// First call populates the cache.
	rec1 := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/upgrade-insights",
		map[string]string{"cluster": "prod-eu-west-1"}, false)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call status = %d", rec1.Code)
	}

	// Second call must hit the cache.
	rec2 := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/upgrade-insights",
		map[string]string{"cluster": "prod-eu-west-1"}, false)
	if rec2.Code != http.StatusOK {
		t.Fatalf("second call status = %d", rec2.Code)
	}

	if fake.listCalls != 1 {
		t.Errorf("ListInsights calls = %d, want 1 (second call should be cache hit)", fake.listCalls)
	}

	// Both calls audit. The second should record op=cache_hit so a
	// reviewer can distinguish AWS-side vs cache-served reads.
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2", len(events))
	}
	if op, _ := events[0].Extra["op"].(string); op != "list" {
		t.Errorf("first event op = %q, want list", op)
	}
	if op, _ := events[1].Extra["op"].(string); op != "cache_hit" {
		t.Errorf("second event op = %q, want cache_hit", op)
	}
}

// ── Detail endpoint ──────────────────────────────────────────────────

func TestEKSInsightsGet_HappyPathWithEditorPaths(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()
	fake := &fakeEKSInsightsClient{
		descFn: func(_ context.Context, in *eks.DescribeInsightInput) (*eks.DescribeInsightOutput, error) {
			if in.Id == nil || *in.Id != "i-1" {
				t.Errorf("expected insight id i-1, got %v", in.Id)
			}
			id := "i-1"
			name := "Deprecated APIs"
			version := "1.32"
			desc := "Resources using removed APIs"
			rec := "Migrate before 1.32 upgrade"
			uri := "/apis/policy/v1beta1/namespaces/kube-system/poddisruptionbudgets/coredns"
			usage := "/apis/policy/v1beta1/poddisruptionbudgets"
			replaced := "/apis/policy/v1/poddisruptionbudgets"
			stop := "1.25"
			return &eks.DescribeInsightOutput{
				Insight: &ekstypes.Insight{
					Id:                 &id,
					Name:               &name,
					Category:           ekstypes.CategoryUpgradeReadiness,
					KubernetesVersion:  &version,
					Description:        &desc,
					Recommendation:     &rec,
					LastRefreshTime:    &now,
					LastTransitionTime: &now,
					InsightStatus: &ekstypes.InsightStatus{
						Status: ekstypes.InsightStatusValueError,
					},
					Resources: []ekstypes.InsightResourceDetail{
						{KubernetesResourceUri: &uri},
					},
					CategorySpecificSummary: &ekstypes.InsightCategorySpecificSummary{
						DeprecationDetails: []ekstypes.DeprecationDetail{
							{
								Usage:              &usage,
								ReplacedWith:       &replaced,
								StopServingVersion: &stop,
							},
						},
					},
				},
			}, nil
		},
	}
	withFakeEKSClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)
	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/upgrade-insights/i-1",
		map[string]string{"cluster": "prod-eu-west-1", "insightId": "i-1"}, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got UpgradeInsightDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != "i-1" || got.Status != "ERROR" {
		t.Errorf("got = %+v", got.UpgradeInsightSummary)
	}
	if len(got.Resources) != 1 {
		t.Fatalf("Resources = %d, want 1", len(got.Resources))
	}
	r := got.Resources[0]
	if r.EditorPath != "/clusters/prod-eu-west-1/poddisruptionbudgets?sel=coredns&selNs=kube-system&tab=yaml" {
		t.Errorf("EditorPath = %q", r.EditorPath)
	}
	if r.Group != "policy" || r.Version != "v1beta1" || r.Resource != "poddisruptionbudgets" {
		t.Errorf("parsed parts wrong: %+v", r)
	}
	if len(got.DeprecationDetails) != 1 || got.DeprecationDetails[0].StopServingVersion != "1.25" {
		t.Errorf("DeprecationDetails = %+v", got.DeprecationDetails)
	}
}

func TestEKSInsightsGet_NoAffectedResources(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSInsightsClient{
		descFn: func(_ context.Context, _ *eks.DescribeInsightInput) (*eks.DescribeInsightOutput, error) {
			id := "i-x"
			name := "All clear"
			return &eks.DescribeInsightOutput{Insight: &ekstypes.Insight{
				Id:   &id,
				Name: &name,
				InsightStatus: &ekstypes.InsightStatus{
					Status: ekstypes.InsightStatusValuePassing,
				},
			}}, nil
		},
	}
	withFakeEKSClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)
	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/upgrade-insights/i-x",
		map[string]string{"cluster": "prod-eu-west-1", "insightId": "i-x"}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Resources must be `[]` not `null` so the SPA can iterate
	// without a nil guard. Spot-check the JSON shape.
	if !containsExact(rec.Body.String(), `"resources":[]`) {
		t.Errorf("expected resources:[] in body, got: %s", rec.Body.String())
	}
}

func TestEKSInsightsGet_EmptyInsightResponseFails(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSInsightsClient{
		descFn: func(_ context.Context, _ *eks.DescribeInsightInput) (*eks.DescribeInsightOutput, error) {
			return &eks.DescribeInsightOutput{Insight: nil}, nil
		},
	}
	withFakeEKSClient(t, fake)
	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)
	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/upgrade-insights/i-1",
		map[string]string{"cluster": "prod-eu-west-1", "insightId": "i-1"}, true)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Outcome != audit.OutcomeFailure {
		t.Errorf("expected one failure event; got %+v", events)
	}
}

func TestEKSInsightsGet_MissingInsightId(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)
	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/prod-eu-west-1/eks/upgrade-insights/",
		map[string]string{"cluster": "prod-eu-west-1"}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestEKSInsightsGet_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "kind-local", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache := newEKSInsightsCache(time.Hour)
	rec := invokeInsights(t, reg, cache, sink, http.MethodGet,
		"/api/clusters/kind-local/eks/upgrade-insights/i-1",
		map[string]string{"cluster": "kind-local", "insightId": "i-1"}, true)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func mkSummary(id, name, version string, status ekstypes.InsightStatusValue, ts *time.Time) ekstypes.InsightSummary {
	idCopy := id
	nameCopy := name
	verCopy := version
	return ekstypes.InsightSummary{
		Id:                 &idCopy,
		Name:               &nameCopy,
		Category:           ekstypes.CategoryUpgradeReadiness,
		KubernetesVersion:  &verCopy,
		LastRefreshTime:    ts,
		LastTransitionTime: ts,
		InsightStatus:      &ekstypes.InsightStatus{Status: status},
	}
}

func containsExact(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
