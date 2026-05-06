package main

import (
	"bytes"
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

// invokeAddonInstall runs the install handler in-process. Mirrors
// invokeAddonCatalog's shape but POSTs a JSON body.
func invokeAddonInstall(t *testing.T, reg *clusters.Registry, addons *eksAddonsCache, sink *recordingSink, cluster string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	emitter := audit.New(sink)
	h := eksAddonInstallHandler(reg, addons, emitter)
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/"+cluster+"/eks/addons", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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

// invokeAddonConfiguration runs the configuration-schema handler.
func invokeAddonConfiguration(t *testing.T, reg *clusters.Registry, schemas *addonConfigSchemaCache, sink *recordingSink, cluster, name, version string) *httptest.ResponseRecorder {
	t.Helper()
	emitter := audit.New(sink)
	h := eksAddonConfigurationHandler(reg, schemas, emitter)
	url := "/api/clusters/" + cluster + "/eks/addons/catalog/" + name + "/configuration"
	if version != "" {
		url += "?version=" + version
	}
	req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", cluster)
	rctx.URLParams.Add("name", name)
	req = req.WithContext(credentials.WithSession(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		credentials.Session{Subject: "alice@corp", Email: "alice@corp", Groups: []string{"eng"}},
	))
	rec := httptest.NewRecorder()
	h(rec, req, fakeProvider{actor: "alice@corp"})
	return rec
}

// ── Install endpoint ────────────────────────────────────────────────

func TestEKSAddonInstall_HappyPath(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	fake := &fakeEKSAddonsClient{
		createFn: func(_ context.Context, in *eks.CreateAddonInput) (*eks.CreateAddonOutput, error) {
			if *in.ClusterName != "prod-eu-west-1" {
				t.Errorf("ClusterName = %q", *in.ClusterName)
			}
			if *in.AddonName != "vpc-cni" {
				t.Errorf("AddonName = %q", *in.AddonName)
			}
			if *in.AddonVersion != "v1.18.0" {
				t.Errorf("AddonVersion = %q", *in.AddonVersion)
			}
			if in.ResolveConflicts != ekstypes.ResolveConflictsOverwrite {
				t.Errorf("ResolveConflicts = %q, want OVERWRITE", in.ResolveConflicts)
			}
			if in.ConfigurationValues == nil || *in.ConfigurationValues != `{"k":"v"}` {
				t.Errorf("ConfigurationValues = %v", in.ConfigurationValues)
			}
			if in.ServiceAccountRoleArn == nil || *in.ServiceAccountRoleArn != "arn:aws:iam::111:role/x" {
				t.Errorf("ServiceAccountRoleArn = %v", in.ServiceAccountRoleArn)
			}
			return &eks.CreateAddonOutput{
				Addon: &ekstypes.Addon{
					AddonName:    strPtrAddons("vpc-cni"),
					AddonVersion: strPtrAddons("v1.18.0"),
					Status:       ekstypes.AddonStatusCreating,
					AddonArn:     strPtrAddons("arn:aws:eks:eu-west-1:111:addon/prod-eu-west-1/vpc-cni/abc"),
				},
			}, nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	addons := newEKSAddonsCache(time.Hour)
	// Pre-populate the cache so we can verify invalidation.
	addons.PutList("prod-eu-west-1", AddonsListResponse{Addons: []AddonSummary{{Name: "old"}}})
	addons.PutDetail("prod-eu-west-1", "vpc-cni", AddonDetail{AddonSummary: AddonSummary{Name: "vpc-cni", Version: "stale"}})

	sink := &recordingSink{}
	body := []byte(`{
		"addonName":"vpc-cni",
		"addonVersion":"v1.18.0",
		"configurationValues":"{\"k\":\"v\"}",
		"serviceAccountRoleArn":"arn:aws:iam::111:role/x",
		"resolveConflicts":"OVERWRITE"
	}`)
	rec := invokeAddonInstall(t, reg, addons, sink, "prod-eu-west-1", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
	}
	var got AddonDetail
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != string(ekstypes.AddonStatusCreating) {
		t.Errorf("Status = %q, want CREATING", got.Status)
	}
	if got.Name != "vpc-cni" {
		t.Errorf("Name = %q", got.Name)
	}

	// Cache invalidated.
	if _, ok := addons.GetList("prod-eu-west-1"); ok {
		t.Errorf("list cache should be invalidated post-install")
	}
	if _, ok := addons.GetDetail("prod-eu-west-1", "vpc-cni"); ok {
		t.Errorf("detail cache should be invalidated post-install")
	}

	// Audit pair: intent, then outcome.
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2 (intent + outcome)", len(events))
	}
	if events[0].Verb != audit.VerbEKSAddonInstallIntent {
		t.Errorf("event[0].Verb = %q, want eks_addon_install_intent", events[0].Verb)
	}
	if events[1].Verb != audit.VerbEKSAddonInstall {
		t.Errorf("event[1].Verb = %q, want eks_addon_install", events[1].Verb)
	}
	if events[1].Outcome != audit.OutcomeSuccess {
		t.Errorf("event[1].Outcome = %q, want success", events[1].Outcome)
	}
	if v, _ := events[1].Extra["addonName"].(string); v != "vpc-cni" {
		t.Errorf("event[1].Extra[addonName] = %q, want vpc-cni", v)
	}
}

func TestEKSAddonInstall_AWSFailureEmitsOutcome(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	fake := &fakeEKSAddonsClient{
		createFn: func(_ context.Context, _ *eks.CreateAddonInput) (*eks.CreateAddonOutput, error) {
			return nil, &ekstypes.AccessDeniedException{Message: strPtrAddons("denied")}
		},
	}
	withFakeEKSAddonsClient(t, fake)

	sink := &recordingSink{}
	addons := newEKSAddonsCache(time.Hour)
	rec := invokeAddonInstall(t, reg, addons, sink, "prod-eu-west-1",
		[]byte(`{"addonName":"vpc-cni","addonVersion":"v1.18.0"}`))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
	}
	var body apiError
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "E_AWS_FORBIDDEN" {
		t.Errorf("code = %q, want E_AWS_FORBIDDEN", body.Code)
	}

	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2 (intent + outcome)", len(events))
	}
	if events[1].Outcome != audit.OutcomeFailure {
		t.Errorf("outcome = %q, want failure", events[1].Outcome)
	}
}

func TestEKSAddonInstall_ValidatesBody(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	withFakeEKSAddonsClient(t, &fakeEKSAddonsClient{})

	cases := []struct {
		name string
		body string
		want int
	}{
		{"missing addonName", `{"addonVersion":"v1.18.0"}`, http.StatusBadRequest},
		{"missing addonVersion", `{"addonName":"vpc-cni"}`, http.StatusBadRequest},
		{"invalid resolveConflicts", `{"addonName":"vpc-cni","addonVersion":"v1.18.0","resolveConflicts":"FORCE"}`, http.StatusBadRequest},
		{"malformed JSON", `not json`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := invokeAddonInstall(t, reg, newEKSAddonsCache(time.Hour), &recordingSink{}, "prod-eu-west-1", []byte(tc.body))
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestEKSAddonInstall_AcceptsAllResolveConflictValues(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	for _, val := range []string{"", "NONE", "OVERWRITE", "PRESERVE"} {
		t.Run("rc="+val, func(t *testing.T) {
			fake := &fakeEKSAddonsClient{
				createFn: func(_ context.Context, _ *eks.CreateAddonInput) (*eks.CreateAddonOutput, error) {
					return &eks.CreateAddonOutput{Addon: &ekstypes.Addon{
						AddonName: strPtrAddons("vpc-cni"),
						Status:    ekstypes.AddonStatusCreating,
					}}, nil
				},
			}
			withFakeEKSAddonsClient(t, fake)
			body := `{"addonName":"vpc-cni","addonVersion":"v1.18.0","resolveConflicts":"` + val + `"}`
			rec := invokeAddonInstall(t, reg, newEKSAddonsCache(time.Hour), &recordingSink{}, "prod-eu-west-1", []byte(body))
			if rec.Code != http.StatusAccepted {
				t.Errorf("status = %d, want 202; body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestEKSAddonInstall_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "kind-local", clusters.BackendInCluster)
	rec := invokeAddonInstall(t, reg, newEKSAddonsCache(time.Hour), &recordingSink{}, "kind-local",
		[]byte(`{"addonName":"vpc-cni","addonVersion":"v1.18.0"}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	var body apiError
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != errBackendNotEKSCode {
		t.Errorf("code = %q, want %q", body.Code, errBackendNotEKSCode)
	}
}

func TestEKSAddonInstall_RejectsOversizedBody(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	withFakeEKSAddonsClient(t, &fakeEKSAddonsClient{})
	huge := make([]byte, addonInstallMaxBodyBytes+1024)
	for i := range huge {
		huge[i] = 'x'
	}
	rec := invokeAddonInstall(t, reg, newEKSAddonsCache(time.Hour), &recordingSink{}, "prod-eu-west-1", huge)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (oversized body)", rec.Code)
	}
}

// ── Configuration schema endpoint ────────────────────────────────────

func TestEKSAddonConfiguration_HappyPath(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)

	wantSchema := `{"type":"object","properties":{"enableNetworkPolicy":{"type":"boolean"}}}`
	fake := &fakeEKSAddonsClient{
		configFn: func(_ context.Context, in *eks.DescribeAddonConfigurationInput) (*eks.DescribeAddonConfigurationOutput, error) {
			if *in.AddonName != "vpc-cni" || *in.AddonVersion != "v1.18.0" {
				t.Errorf("inputs = (%q, %q)", *in.AddonName, *in.AddonVersion)
			}
			return &eks.DescribeAddonConfigurationOutput{
				ConfigurationSchema: strPtrAddons(wantSchema),
			}, nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	schemas := newAddonConfigSchemaCache(time.Hour)
	sink := &recordingSink{}
	rec := invokeAddonConfiguration(t, reg, schemas, sink, "prod-eu-west-1", "vpc-cni", "v1.18.0")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got AddonConfigurationResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ConfigurationSchema != wantSchema {
		t.Errorf("schema mismatch")
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	if op, _ := events[0].Extra["op"].(string); op != "configuration" {
		t.Errorf("op = %q, want configuration", op)
	}
}

func TestEKSAddonConfiguration_CacheHitSkipsAWS(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	var configCalls int32
	fake := &fakeEKSAddonsClient{
		configFn: func(_ context.Context, _ *eks.DescribeAddonConfigurationInput) (*eks.DescribeAddonConfigurationOutput, error) {
			atomic.AddInt32(&configCalls, 1)
			return &eks.DescribeAddonConfigurationOutput{
				ConfigurationSchema: strPtrAddons(`{"type":"object"}`),
			}, nil
		},
	}
	withFakeEKSAddonsClient(t, fake)

	schemas := newAddonConfigSchemaCache(time.Hour)
	sink := &recordingSink{}
	rec1 := invokeAddonConfiguration(t, reg, schemas, sink, "prod-eu-west-1", "vpc-cni", "v1.18.0")
	rec2 := invokeAddonConfiguration(t, reg, schemas, sink, "prod-eu-west-1", "vpc-cni", "v1.18.0")
	if rec1.Code != http.StatusOK || rec2.Code != http.StatusOK {
		t.Fatalf("status1 = %d, status2 = %d", rec1.Code, rec2.Code)
	}
	if got := atomic.LoadInt32(&configCalls); got != 1 {
		t.Errorf("DescribeAddonConfiguration calls = %d, want 1", got)
	}
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit events = %d, want 2", len(events))
	}
	if op, _ := events[1].Extra["op"].(string); op != "configuration:cache_hit" {
		t.Errorf("event[1].op = %q, want configuration:cache_hit", op)
	}
}

func TestEKSAddonConfiguration_StickyError(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	var configCalls int32
	fake := &fakeEKSAddonsClient{
		configFn: func(_ context.Context, _ *eks.DescribeAddonConfigurationInput) (*eks.DescribeAddonConfigurationOutput, error) {
			atomic.AddInt32(&configCalls, 1)
			return nil, errors.New("transient AWS error")
		},
	}
	withFakeEKSAddonsClient(t, fake)
	schemas := newAddonConfigSchemaCache(time.Hour)
	rec1 := invokeAddonConfiguration(t, reg, schemas, &recordingSink{}, "prod-eu-west-1", "vpc-cni", "v1.18.0")
	rec2 := invokeAddonConfiguration(t, reg, schemas, &recordingSink{}, "prod-eu-west-1", "vpc-cni", "v1.18.0")
	if rec1.Code != http.StatusBadGateway || rec2.Code != http.StatusBadGateway {
		t.Fatalf("status1 = %d, status2 = %d, want 502 + 502", rec1.Code, rec2.Code)
	}
	if got := atomic.LoadInt32(&configCalls); got != 1 {
		t.Errorf("AWS calls = %d, want 1 (sticky error reuse)", got)
	}
}

func TestEKSAddonConfiguration_MissingVersionParam(t *testing.T) {
	reg := eksRegistry(t, "prod-eu-west-1", clusters.BackendEKS)
	rec := invokeAddonConfiguration(t, reg, newAddonConfigSchemaCache(time.Hour), &recordingSink{}, "prod-eu-west-1", "vpc-cni", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestEKSAddonConfiguration_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "kind-local", clusters.BackendInCluster)
	rec := invokeAddonConfiguration(t, reg, newAddonConfigSchemaCache(time.Hour), &recordingSink{}, "kind-local", "vpc-cni", "v1.18.0")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}
