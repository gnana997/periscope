package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// fakeEKSNodegroupsClient implements eksNodegroupsAPI. The list +
// describe fns are pluggable per-test.
type fakeEKSNodegroupsClient struct {
	mu        sync.Mutex
	listCalls int
	descCalls int
	listFn    func(ctx context.Context, in *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error)
	descFn    func(ctx context.Context, in *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error)
}

func (f *fakeEKSNodegroupsClient) ListNodegroups(ctx context.Context, in *eks.ListNodegroupsInput, _ ...func(*eks.Options)) (*eks.ListNodegroupsOutput, error) {
	f.mu.Lock()
	f.listCalls++
	f.mu.Unlock()
	if f.listFn == nil {
		return nil, errors.New("listFn not set")
	}
	return f.listFn(ctx, in)
}

func (f *fakeEKSNodegroupsClient) DescribeNodegroup(ctx context.Context, in *eks.DescribeNodegroupInput, _ ...func(*eks.Options)) (*eks.DescribeNodegroupOutput, error) {
	f.mu.Lock()
	f.descCalls++
	f.mu.Unlock()
	if f.descFn == nil {
		return nil, errors.New("descFn not set")
	}
	return f.descFn(ctx, in)
}

func withFakeNodegroupsClient(t *testing.T, fake *fakeEKSNodegroupsClient) {
	t.Helper()
	orig := newEKSNodegroupsClient
	newEKSNodegroupsClient = func(_ credentials.Provider, _ clusters.Cluster) eksNodegroupsAPI {
		return fake
	}
	t.Cleanup(func() { newEKSNodegroupsClient = orig })
}

// invokeNodegroups drives the list or detail handler with a planted
// session. Reuses the recordingSink + fakeProvider from the package
// scaffolding (audit_test_helpers_test.go / cani_handler_test.go).
//
// `amiCache` is optional — nil exercises the no-drift path (the PR-2
// shape, where DriftComputed stays false on every row); a non-nil
// cache exercises the drift fold-in path that production runs.
func invokeNodegroups(t *testing.T, reg *clusters.Registry, cache *eksNodegroupsCache, sink *recordingSink, url string, params map[string]string, isDetail bool) *httptest.ResponseRecorder {
	return invokeNodegroupsWithDrift(t, reg, cache, nil, sink, url, params, isDetail)
}

func invokeNodegroupsWithDrift(t *testing.T, reg *clusters.Registry, cache *eksNodegroupsCache, amiCache *amiCatalogCache, sink *recordingSink, url string, params map[string]string, isDetail bool) *httptest.ResponseRecorder {
	t.Helper()
	emitter := audit.New(sink)
	var h credentials.Handler
	if isDetail {
		h = eksNodegroupsGetHandler(reg, cache, amiCache, emitter)
	} else {
		h = eksNodegroupsListHandler(reg, cache, amiCache, emitter)
	}
	req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
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

// ── List ─────────────────────────────────────────────────────────────

func TestEKSNodegroupsList_HappyPath(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()
	fake := &fakeEKSNodegroupsClient{
		listFn: func(_ context.Context, in *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error) {
			if in.ClusterName == nil || *in.ClusterName != "prod-eu-west-1" {
				t.Errorf("expected cluster from ARN, got %v", in.ClusterName)
			}
			return &eks.ListNodegroupsOutput{
				Nodegroups: []string{"ng-system", "ng-spot", "ng-custom"},
			}, nil
		},
		descFn: func(_ context.Context, in *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error) {
			return &eks.DescribeNodegroupOutput{
				Nodegroup: mkNodegroup(*in.NodegroupName, ekstypes.NodegroupStatusActive, &now),
			}, nil
		},
	}
	withFakeNodegroupsClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	rec := invokeNodegroups(t, reg, cache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got NodegroupsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Nodegroups) != 3 {
		t.Fatalf("Nodegroups = %d, want 3", len(got.Nodegroups))
	}
	// Custom-AMI nodegroup must sort first.
	if got.Nodegroups[0].Name != "ng-custom" {
		t.Errorf("expected ng-custom first (CUSTOM AMI sort), got %q", got.Nodegroups[0].Name)
	}
	if !got.Nodegroups[0].CustomAMI {
		t.Errorf("ng-custom should have CustomAMI=true")
	}
	if got.Counts.Total != 3 || got.Counts.Custom != 1 {
		t.Errorf("Counts = %+v", got.Counts)
	}
	// PR-2: no drift computed yet.
	for _, ng := range got.Nodegroups {
		if ng.DriftComputed {
			t.Errorf("PR-2 should not set DriftComputed; got true for %q", ng.Name)
		}
	}

	events := sink.snapshot()
	if len(events) != 1 || events[0].Verb != audit.VerbEKSNodegroupsRead {
		t.Errorf("expected one VerbEKSNodegroupsRead event, got %+v", events)
	}
}

func TestEKSNodegroupsList_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "kind-local", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)

	rec := invokeNodegroups(t, reg, cache, sink,
		"/api/clusters/kind-local/eks/nodegroups",
		map[string]string{"cluster": "kind-local"}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var body apiError
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != errBackendNotEKSCode {
		t.Errorf("code = %q", body.Code)
	}
}

// TestEKSNodegroupsList_CapableNonEKSBackends — same regression cover
// as the insights handler's CapableNonEKSBackends test. Operators
// running periscope inside an EKS cluster (in-cluster K8s auth) or
// reaching clusters via the agent tunnel can still query EKS-side
// node groups as long as ARN + Region are configured.
func TestEKSNodegroupsList_CapableNonEKSBackends(t *testing.T) {
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
			fake := &fakeEKSNodegroupsClient{
				listFn: func(_ context.Context, _ *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error) {
					return &eks.ListNodegroupsOutput{}, nil
				},
			}
			withFakeNodegroupsClient(t, fake)
			sink := &recordingSink{}
			cache := newEKSNodegroupsCache(time.Hour)
			rec := invokeNodegroups(t, reg, cache, sink,
				"/api/clusters/"+tc.cluster+"/eks/nodegroups",
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

func TestEKSNodegroupsList_AWSListErrorEmitsFailureAudit(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSNodegroupsClient{
		listFn: func(_ context.Context, _ *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error) {
			return nil, errors.New("AccessDenied: ListNodegroups")
		},
	}
	withFakeNodegroupsClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	rec := invokeNodegroups(t, reg, cache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Outcome != audit.OutcomeFailure {
		t.Errorf("expected one failure event")
	}
}

// TestEKSNodegroupsList_ClassifiesAWSErrors mirrors the insights test:
// each smithy-typed exception flips the response to a structured
// status + code so the SPA renders the right diagnostic.
func TestEKSNodegroupsList_ClassifiesAWSErrors(t *testing.T) {
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
			fake := &fakeEKSNodegroupsClient{
				listFn: func(_ context.Context, _ *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error) {
					return nil, tc.err
				},
			}
			withFakeNodegroupsClient(t, fake)

			rec := invokeNodegroups(t, reg, newEKSNodegroupsCache(time.Hour),
				&recordingSink{},
				"/api/clusters/prod-eu-west-1/eks/nodegroups",
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

// One nodegroup that fails Describe should not sink the whole list —
// the row is replaced with a degraded placeholder so the operator
// knows it exists but its details are unavailable.
func TestEKSNodegroupsList_PartialDescribeFailure(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()
	fake := &fakeEKSNodegroupsClient{
		listFn: func(_ context.Context, _ *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error) {
			return &eks.ListNodegroupsOutput{
				Nodegroups: []string{"ng-ok", "ng-bad"},
			}, nil
		},
		descFn: func(_ context.Context, in *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error) {
			if *in.NodegroupName == "ng-bad" {
				return nil, errors.New("ResourceNotFoundException")
			}
			return &eks.DescribeNodegroupOutput{
				Nodegroup: mkNodegroup(*in.NodegroupName, ekstypes.NodegroupStatusActive, &now),
			}, nil
		},
	}
	withFakeNodegroupsClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	rec := invokeNodegroups(t, reg, cache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (partial-failure tolerance)", rec.Code)
	}
	var got NodegroupsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Nodegroups) != 2 {
		t.Fatalf("Nodegroups = %d, want 2", len(got.Nodegroups))
	}
	// Find ng-bad and confirm the degraded placeholder.
	var bad *NodegroupSummary
	for i := range got.Nodegroups {
		if got.Nodegroups[i].Name == "ng-bad" {
			bad = &got.Nodegroups[i]
		}
	}
	if bad == nil || bad.Status != "DEGRADED_DESCRIBE" {
		t.Errorf("ng-bad placeholder missing or wrong: %+v", bad)
	}
}

func TestEKSNodegroupsList_CacheHitSkipsAWS(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()
	fake := &fakeEKSNodegroupsClient{
		listFn: func(_ context.Context, _ *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error) {
			return &eks.ListNodegroupsOutput{Nodegroups: []string{"ng-1"}}, nil
		},
		descFn: func(_ context.Context, in *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error) {
			return &eks.DescribeNodegroupOutput{
				Nodegroup: mkNodegroup(*in.NodegroupName, ekstypes.NodegroupStatusActive, &now),
			}, nil
		},
	}
	withFakeNodegroupsClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	for i := 0; i < 2; i++ {
		rec := invokeNodegroups(t, reg, cache, sink,
			"/api/clusters/prod-eu-west-1/eks/nodegroups",
			map[string]string{"cluster": "prod-eu-west-1"}, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("call %d status = %d", i, rec.Code)
		}
	}
	if fake.listCalls != 1 {
		t.Errorf("ListNodegroups calls = %d, want 1 (second served from cache)", fake.listCalls)
	}
	if fake.descCalls != 1 {
		t.Errorf("DescribeNodegroup calls = %d, want 1", fake.descCalls)
	}
	events := sink.snapshot()
	if len(events) != 2 {
		t.Errorf("audit events = %d, want 2", len(events))
	}
	if op, _ := events[1].Extra["op"].(string); op != "list:cache_hit" {
		t.Errorf("second event op = %q, want list:cache_hit", op)
	}
}

// ── Detail ───────────────────────────────────────────────────────────

func TestEKSNodegroupsGet_HappyPath(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()
	fake := &fakeEKSNodegroupsClient{
		descFn: func(_ context.Context, in *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error) {
			if in.NodegroupName == nil || *in.NodegroupName != "ng-1" {
				t.Errorf("name = %v", in.NodegroupName)
			}
			ng := mkNodegroup("ng-1", ekstypes.NodegroupStatusActive, &now)
			arn := "arn:aws:eks:eu-west-1:111:nodegroup/prod-eu-west-1/ng-1/x"
			role := "arn:aws:iam::111:role/eks-node"
			ng.NodegroupArn = &arn
			ng.NodeRole = &role
			ng.Subnets = []string{"subnet-a", "subnet-b"}
			return &eks.DescribeNodegroupOutput{Nodegroup: ng}, nil
		},
	}
	withFakeNodegroupsClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	rec := invokeNodegroups(t, reg, cache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups/ng-1",
		map[string]string{"cluster": "prod-eu-west-1", "name": "ng-1"}, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got NodegroupDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "ng-1" || got.NodeRole == "" || len(got.Subnets) != 2 {
		t.Errorf("got = %+v", got)
	}
}

func TestEKSNodegroupsGet_CustomAMIFlag(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()
	fake := &fakeEKSNodegroupsClient{
		descFn: func(_ context.Context, in *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error) {
			ng := mkNodegroup(*in.NodegroupName, ekstypes.NodegroupStatusActive, &now)
			ng.AmiType = ekstypes.AMITypesCustom
			ltID := "lt-0abc"
			ltVer := "3"
			ng.LaunchTemplate = &ekstypes.LaunchTemplateSpecification{
				Id:      &ltID,
				Version: &ltVer,
			}
			return &eks.DescribeNodegroupOutput{Nodegroup: ng}, nil
		},
	}
	withFakeNodegroupsClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	rec := invokeNodegroups(t, reg, cache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups/ng-x",
		map[string]string{"cluster": "prod-eu-west-1", "name": "ng-x"}, true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got NodegroupDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.CustomAMI {
		t.Errorf("CustomAMI = false, want true")
	}
	if got.AmiType != "CUSTOM" {
		t.Errorf("AmiType = %q", got.AmiType)
	}
	if got.LaunchTemplate == nil || got.LaunchTemplate.ID != "lt-0abc" {
		t.Errorf("LaunchTemplate = %+v", got.LaunchTemplate)
	}
}

func TestEKSNodegroupsGet_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "kind-local", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	rec := invokeNodegroups(t, reg, cache, sink,
		"/api/clusters/kind-local/eks/nodegroups/ng-1",
		map[string]string{"cluster": "kind-local", "name": "ng-1"}, true)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
}

func TestEKSNodegroupsGet_MissingName(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	rec := invokeNodegroups(t, reg, cache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups/",
		map[string]string{"cluster": "prod-eu-west-1"}, true)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// withFakeAMICatalog swaps the catalog client constructor for the
// duration of the test. Mirrors the eks-insights/eks-nodegroups
// pattern so each handler-integration test can supply its own
// fake without globals.
func withFakeAMICatalog(t *testing.T, fake amiCatalogAPI) {
	t.Helper()
	orig := newAMICatalogClient
	newAMICatalogClient = func(_ credentials.Provider, _ clusters.Cluster) amiCatalogAPI {
		return fake
	}
	t.Cleanup(func() { newAMICatalogClient = orig })
}

// Drift fold-in on the list response: SSM returns a newer release
// than the nodegroup is on, IsBehind=true and DaysBehind > 0.
func TestEKSNodegroupsList_DriftFoldedIntoSummary(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()

	nodegroupsFake := &fakeEKSNodegroupsClient{
		listFn: func(_ context.Context, _ *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error) {
			return &eks.ListNodegroupsOutput{Nodegroups: []string{"ng-1"}}, nil
		},
		descFn: func(_ context.Context, in *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error) {
			ng := mkNodegroup(*in.NodegroupName, ekstypes.NodegroupStatusActive, &now)
			oldRelease := "1.30.0-20240819"
			ng.ReleaseVersion = &oldRelease
			return &eks.DescribeNodegroupOutput{Nodegroup: ng}, nil
		},
	}
	withFakeNodegroupsClient(t, nodegroupsFake)

	latestImage := "ami-latest"
	latestVersion := "1.30.0-20240901"
	catalogFake := &fakeAMICatalogClient{
		ssmFn: func(_ context.Context, in *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			val := latestImage
			if in.Name != nil && lastTokenIs(*in.Name, "release_version") {
				val = latestVersion
			}
			return &ssm.GetParameterOutput{Parameter: &ssmtypes.Parameter{Value: &val}}, nil
		},
	}
	withFakeAMICatalog(t, catalogFake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	amiCache := newAMICatalogCache(time.Hour)

	rec := invokeNodegroupsWithDrift(t, reg, cache, amiCache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var got NodegroupsListResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Nodegroups) != 1 {
		t.Fatalf("Nodegroups = %d", len(got.Nodegroups))
	}
	row := got.Nodegroups[0]
	if !row.DriftComputed {
		t.Errorf("DriftComputed = false, want true")
	}
	if !row.IsBehind {
		t.Errorf("IsBehind = false, want true")
	}
	if row.LatestReleaseVersion != latestVersion {
		t.Errorf("LatestReleaseVersion = %q", row.LatestReleaseVersion)
	}
	// 2024-09-01 minus 2024-08-19 = 13 days.
	if row.DaysBehind != 13 {
		t.Errorf("DaysBehind = %d, want 13", row.DaysBehind)
	}
	if got.Counts.Behind != 1 {
		t.Errorf("Counts.Behind = %d, want 1", got.Counts.Behind)
	}
}

// Custom AMI rows must NEVER have drift folded onto them, even when
// the catalog is wired and would otherwise return a "latest".
func TestEKSNodegroupsList_CustomAMISkipsDrift(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()

	nodegroupsFake := &fakeEKSNodegroupsClient{
		listFn: func(_ context.Context, _ *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error) {
			return &eks.ListNodegroupsOutput{Nodegroups: []string{"ng-custom"}}, nil
		},
		descFn: func(_ context.Context, in *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error) {
			return &eks.DescribeNodegroupOutput{
				Nodegroup: mkNodegroup(*in.NodegroupName, ekstypes.NodegroupStatusActive, &now),
			}, nil
		},
	}
	withFakeNodegroupsClient(t, nodegroupsFake)

	// Catalog SSM/EC2 must never be called for CUSTOM rows. If
	// either is invoked, the test fails the assertion.
	catalogCalled := false
	catalogFake := &fakeAMICatalogClient{
		ssmFn: func(_ context.Context, _ *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			catalogCalled = true
			return nil, errors.New("should not be called for CUSTOM AMI")
		},
		ec2Fn: func(_ context.Context, _ *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
			catalogCalled = true
			return nil, errors.New("should not be called for CUSTOM AMI")
		},
	}
	withFakeAMICatalog(t, catalogFake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	amiCache := newAMICatalogCache(time.Hour)

	rec := invokeNodegroupsWithDrift(t, reg, cache, amiCache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if catalogCalled {
		t.Errorf("catalog was invoked for CUSTOM AMI nodegroup")
	}
	var got NodegroupsListResponse
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if len(got.Nodegroups) != 1 || got.Nodegroups[0].DriftComputed {
		t.Errorf("CUSTOM row should leave DriftComputed=false; got = %+v", got.Nodegroups)
	}
}

// Catalog failure should NOT fail the whole list — the drift fields
// stay zero (DriftComputed=false) and the row still renders with
// the rest of the nodegroup metadata.
func TestEKSNodegroupsList_CatalogFailureDoesNotBlockList(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	now := time.Now().UTC()

	nodegroupsFake := &fakeEKSNodegroupsClient{
		listFn: func(_ context.Context, _ *eks.ListNodegroupsInput) (*eks.ListNodegroupsOutput, error) {
			return &eks.ListNodegroupsOutput{Nodegroups: []string{"ng-1"}}, nil
		},
		descFn: func(_ context.Context, in *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error) {
			return &eks.DescribeNodegroupOutput{
				Nodegroup: mkNodegroup(*in.NodegroupName, ekstypes.NodegroupStatusActive, &now),
			}, nil
		},
	}
	withFakeNodegroupsClient(t, nodegroupsFake)

	catalogFake := &fakeAMICatalogClient{
		ssmFn: func(_ context.Context, _ *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			return nil, errors.New("AccessDenied: GetParameter")
		},
		ec2Fn: func(_ context.Context, _ *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
			return nil, errors.New("AccessDenied: DescribeImages")
		},
	}
	withFakeAMICatalog(t, catalogFake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	amiCache := newAMICatalogCache(time.Hour)
	rec := invokeNodegroupsWithDrift(t, reg, cache, amiCache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups",
		map[string]string{"cluster": "prod-eu-west-1"}, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got NodegroupsListResponse
	_ = json.NewDecoder(rec.Body).Decode(&got)
	if len(got.Nodegroups) != 1 || got.Nodegroups[0].DriftComputed {
		t.Errorf("expected drift not computed on catalog failure; got %+v", got.Nodegroups)
	}
}

func TestEKSNodegroupsGet_AWSError(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSNodegroupsClient{
		descFn: func(_ context.Context, _ *eks.DescribeNodegroupInput) (*eks.DescribeNodegroupOutput, error) {
			return nil, errors.New("ResourceNotFoundException")
		},
	}
	withFakeNodegroupsClient(t, fake)

	sink := &recordingSink{}
	cache := newEKSNodegroupsCache(time.Hour)
	rec := invokeNodegroups(t, reg, cache, sink,
		"/api/clusters/prod-eu-west-1/eks/nodegroups/missing",
		map[string]string{"cluster": "prod-eu-west-1", "name": "missing"}, true)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d", rec.Code)
	}
	if events := sink.snapshot(); len(events) != 1 || events[0].Outcome != audit.OutcomeFailure {
		t.Errorf("expected one failure event")
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func mkNodegroup(name string, status ekstypes.NodegroupStatus, ts *time.Time) *ekstypes.Nodegroup {
	nameCopy := name
	version := "1.30"
	releaseVersion := "1.30.0-20240819"
	desired := int32(3)
	minSize := int32(1)
	maxSize := int32(5)

	// CUSTOM is signaled via name suffix so the test fixtures stay simple.
	amiType := ekstypes.AMITypesAl2023X8664Standard
	if name == "ng-custom" {
		amiType = ekstypes.AMITypesCustom
	}

	return &ekstypes.Nodegroup{
		NodegroupName:     &nameCopy,
		Status:            status,
		AmiType:           amiType,
		CapacityType:      ekstypes.CapacityTypesOnDemand,
		Version:           &version,
		ReleaseVersion:    &releaseVersion,
		CreatedAt:         ts,
		InstanceTypes:     []string{"m5.large"},
		ScalingConfig: &ekstypes.NodegroupScalingConfig{
			DesiredSize: &desired,
			MinSize:     &minSize,
			MaxSize:     &maxSize,
		},
	}
}
