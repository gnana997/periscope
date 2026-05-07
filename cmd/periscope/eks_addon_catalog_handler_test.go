package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

// invokeAddonCatalog runs the catalog handler in-process, mirroring
// invokeAddons / invokeInsights. The fake EKS client is wired via
// withFakeEKSAddonsClient (shared with #117 since the handlers use
// the same eksAddonsAPI seam).
func invokeAddonCatalog(t *testing.T, reg *clusters.Registry, catalog *addonCatalogCache, addons *eksAddonsCache, sink *recordingSink, cluster string) *httptest.ResponseRecorder {
	t.Helper()
	emitter := audit.New(sink)
	h := eksAddonCatalogHandler(reg, catalog, addons, emitter)
	req := httptest.NewRequest(http.MethodGet, "/api/clusters/"+cluster+"/eks/addons/catalog", http.NoBody)
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

// catalogVersionsResponse builds a multi-addon DescribeAddonVersions
// response for the catalog (no AddonName filter). Mirrors
// versionsResponse but shapes the payload as the unfiltered catalog
// shape — multiple AddonInfo entries.
func catalogVersionsResponse(addons []ekstypes.AddonInfo, nextToken string) *eks.DescribeAddonVersionsOutput {
	out := &eks.DescribeAddonVersionsOutput{Addons: addons}
	if nextToken != "" {
		out.NextToken = strPtrAddons(nextToken)
	}
	return out
}

func mkCatalogAddon(name, owner, publisher, addonType string, marketplace bool, vers []struct {
	Ver       string
	K8s       []string
	IsDefault bool
}) ekstypes.AddonInfo {
	info := ekstypes.AddonInfo{
		AddonName: strPtrAddons(name),
		Owner:     strPtrAddons(owner),
		Publisher: strPtrAddons(publisher),
		Type:      strPtrAddons(addonType),
	}
	if marketplace {
		info.MarketplaceInformation = &ekstypes.MarketplaceInformation{
			ProductId:  strPtrAddons("prodview-x"),
			ProductUrl: strPtrAddons("https://aws.amazon.com/marketplace"),
		}
	}
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
	return info
}

// ── Happy path ───────────────────────────────────────────────────────

func TestEKSAddonCatalog_HappyPath(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	fake := &fakeEKSAddonsClient{
		versionsFn: func(_ context.Context, in *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
			// Catalog call — no AddonName filter.
			if in.AddonName != nil && *in.AddonName != "" {
				t.Errorf("AddonName = %q, want empty (catalog call)", *in.AddonName)
			}
			if in.KubernetesVersion == nil || *in.KubernetesVersion != "1.29" {
				t.Errorf("KubernetesVersion = %v, want 1.29", in.KubernetesVersion)
			}
			return catalogVersionsResponse([]ekstypes.AddonInfo{
				mkCatalogAddon("vpc-cni", "aws", "eks", "networking", false, []struct {
					Ver       string
					K8s       []string
					IsDefault bool
				}{
					{Ver: "v1.18.0", K8s: []string{"1.27", "1.28", "1.29", "1.30"}, IsDefault: true},
				}),
				mkCatalogAddon("datadog-agent", "datadog", "datadog", "observability", true, []struct {
					Ver       string
					K8s       []string
					IsDefault bool
				}{
					{Ver: "v7.50.0", K8s: []string{"1.28", "1.29", "1.30"}},
				}),
				mkCatalogAddon("coredns", "aws", "eks", "networking", false, []struct {
					Ver       string
					K8s       []string
					IsDefault bool
				}{
					{Ver: "v1.11.4", K8s: []string{"1.27", "1.28", "1.29", "1.30"}, IsDefault: true},
				}),
			}, ""), nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	sink := &recordingSink{}
	rec := invokeAddonCatalog(t, reg, newAddonCatalogCache(time.Hour), newEKSAddonsCache(time.Hour), sink, "prod-eu-west-1")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got AddonCatalogResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.KubernetesVersion != "1.29" {
		t.Errorf("KubernetesVersion = %q, want 1.29", got.KubernetesVersion)
	}
	if len(got.Available) != 3 {
		t.Fatalf("Available = %d, want 3", len(got.Available))
	}
	// Sort: AWS-owned first (coredns, vpc-cni alphabetical), then third-party (datadog).
	if got.Available[0].Name != "coredns" {
		t.Errorf("first row = %q, want coredns (aws-owned, alphabetical)", got.Available[0].Name)
	}
	if got.Available[1].Name != "vpc-cni" {
		t.Errorf("second row = %q, want vpc-cni", got.Available[1].Name)
	}
	if got.Available[2].Name != "datadog-agent" {
		t.Errorf("third row = %q, want datadog-agent (third-party last)", got.Available[2].Name)
	}
	if !got.Available[2].MarketplaceProduct {
		t.Errorf("datadog-agent.MarketplaceProduct = false, want true")
	}
	if got.Available[0].MarketplaceProduct {
		t.Errorf("coredns.MarketplaceProduct = true, want false")
	}
	// Default flag carried for queried k8sVer.
	if got.Available[0].CompatibleVersions[0].Default == false {
		t.Errorf("coredns default flag missing for queried 1.29")
	}

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	if events[0].Verb != audit.VerbEKSAddonsRead {
		t.Errorf("Verb = %q, want eks_addons_read", events[0].Verb)
	}
	if op, _ := events[0].Extra["op"].(string); op != "catalog" {
		t.Errorf("Extra[op] = %q, want catalog", op)
	}
}

// ── Error envelope ────────────────────────────────────────────────────

func TestEKSAddonCatalog_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "kind-local", clusters.BackendInCluster)
	sink := &recordingSink{}
	rec := invokeAddonCatalog(t, reg, newAddonCatalogCache(time.Hour), newEKSAddonsCache(time.Hour), sink, "kind-local")
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

func TestEKSAddonCatalog_ClusterNotFound(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	rec := invokeAddonCatalog(t, reg, newAddonCatalogCache(time.Hour), newEKSAddonsCache(time.Hour), &recordingSink{}, "missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestEKSAddonCatalog_ClassifiesAWSErrors(t *testing.T) {
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
			name:       "Throttling → 429/E_AWS_THROTTLED",
			err:        &ekstypes.ThrottlingException{Message: strPtrAddons("slow down")},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   "E_AWS_THROTTLED",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
			fake := &fakeEKSAddonsClient{
				versionsFn: func(_ context.Context, _ *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
					return nil, tc.err
				},
			}
			withFakeEKSAddonsClient(t, fake)

			rec := invokeAddonCatalog(t, reg, newAddonCatalogCache(time.Hour), newEKSAddonsCache(time.Hour), &recordingSink{}, "prod-eu-west-1")
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var body apiError
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if body.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", body.Code, tc.wantCode)
			}
		})
	}
}

func TestEKSAddonCatalog_AWSErrorEmitsFailureAudit(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSAddonsClient{
		versionsFn: func(_ context.Context, _ *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
			return nil, errors.New("transient")
		},
	}
	withFakeEKSAddonsClient(t, fake)

	sink := &recordingSink{}
	rec := invokeAddonCatalog(t, reg, newAddonCatalogCache(time.Hour), newEKSAddonsCache(time.Hour), sink, "prod-eu-west-1")
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
	if op, _ := events[0].Extra["op"].(string); op != "catalog" {
		t.Errorf("Extra[op] = %q, want catalog", op)
	}
}

// ── Cache behavior ────────────────────────────────────────────────────

func TestEKSAddonCatalog_CacheHitSkipsAWS(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	var versionsCallCount int32
	fake := &fakeEKSAddonsClient{
		versionsFn: func(_ context.Context, _ *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
			atomic.AddInt32(&versionsCallCount, 1)
			return catalogVersionsResponse([]ekstypes.AddonInfo{
				mkCatalogAddon("vpc-cni", "aws", "eks", "networking", false, nil),
			}, ""), nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	cache := newAddonCatalogCache(time.Hour)
	sink := &recordingSink{}

	rec1 := invokeAddonCatalog(t, reg, cache, newEKSAddonsCache(time.Hour), sink, "prod-eu-west-1")
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call status = %d", rec1.Code)
	}
	rec2 := invokeAddonCatalog(t, reg, cache, newEKSAddonsCache(time.Hour), sink, "prod-eu-west-1")
	if rec2.Code != http.StatusOK {
		t.Fatalf("second call status = %d", rec2.Code)
	}

	if got := atomic.LoadInt32(&versionsCallCount); got != 1 {
		t.Errorf("DescribeAddonVersions calls = %d, want 1 (second should hit cache)", got)
	}

	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2", len(events))
	}
	if op, _ := events[0].Extra["op"].(string); op != "catalog" {
		t.Errorf("first op = %q, want catalog", op)
	}
	if op, _ := events[1].Extra["op"].(string); op != "catalog:cache_hit" {
		t.Errorf("second op = %q, want catalog:cache_hit", op)
	}
}

// ── Installed-state layering ──────────────────────────────────────────

func TestEKSAddonCatalog_LayersInstalledFromAddonsCache(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	fake := &fakeEKSAddonsClient{
		versionsFn: func(_ context.Context, _ *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
			return catalogVersionsResponse([]ekstypes.AddonInfo{
				mkCatalogAddon("vpc-cni", "aws", "eks", "networking", false, nil),
				mkCatalogAddon("aws-mountpoint-s3", "aws", "eks", "storage", false, nil),
			}, ""), nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	addons := newEKSAddonsCache(time.Hour)
	addons.PutList("prod-eu-west-1", AddonsListResponse{
		Addons: []AddonSummary{
			{Name: "vpc-cni", Version: "v1.16.4", Status: "ACTIVE"},
		},
	})

	sink := &recordingSink{}
	rec := invokeAddonCatalog(t, reg, newAddonCatalogCache(time.Hour), addons, sink, "prod-eu-west-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got AddonCatalogResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]CatalogAddon{}
	for _, a := range got.Available {
		byName[a.Name] = a
	}
	if v := byName["vpc-cni"]; v.Installed == nil || v.Installed.Version != "v1.16.4" {
		t.Errorf("vpc-cni.Installed = %+v, want {Version:v1.16.4}", v.Installed)
	}
	if v := byName["aws-mountpoint-s3"]; v.Installed != nil {
		t.Errorf("aws-mountpoint-s3.Installed = %+v, want nil (not in addons cache)", v.Installed)
	}
}

// ── Pagination ────────────────────────────────────────────────────────

func TestEKSAddonCatalog_ConsumesPagination(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	var pages int32
	fake := &fakeEKSAddonsClient{
		versionsFn: func(_ context.Context, in *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
			n := atomic.AddInt32(&pages, 1)
			switch n {
			case 1:
				return catalogVersionsResponse([]ekstypes.AddonInfo{
					mkCatalogAddon("vpc-cni", "aws", "eks", "networking", false, nil),
				}, "tok-2"), nil
			case 2:
				if in.NextToken == nil || *in.NextToken != "tok-2" {
					t.Errorf("NextToken on 2nd page = %v, want tok-2", in.NextToken)
				}
				return catalogVersionsResponse([]ekstypes.AddonInfo{
					mkCatalogAddon("coredns", "aws", "eks", "networking", false, nil),
				}, ""), nil
			}
			t.Fatalf("unexpected 3rd call to DescribeAddonVersions")
			return nil, nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	rec := invokeAddonCatalog(t, reg, newAddonCatalogCache(time.Hour), newEKSAddonsCache(time.Hour), &recordingSink{}, "prod-eu-west-1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var got AddonCatalogResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Available) != 2 {
		t.Errorf("Available = %d, want 2 (across two pages)", len(got.Available))
	}
	if atomic.LoadInt32(&pages) != 2 {
		t.Errorf("pages consumed = %d, want 2", pages)
	}
}

// ── Capable-non-EKS-backends regression cover ────────────────────────

func TestEKSAddonCatalog_CapableNonEKSBackends(t *testing.T) {
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
				versionsFn: func(_ context.Context, _ *eks.DescribeAddonVersionsInput) (*eks.DescribeAddonVersionsOutput, error) {
					return catalogVersionsResponse(nil, ""), nil
				},
			}
			withFakeEKSAddonsClient(t, fake)
			rec := invokeAddonCatalog(t, reg, newAddonCatalogCache(time.Hour), newEKSAddonsCache(time.Hour), &recordingSink{}, tc.cluster)
			if rec.Code == http.StatusUnprocessableEntity {
				t.Fatalf("status = 422 (gate rejected capable cluster); want non-422")
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}
