package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/go-chi/chi/v5"
	authv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/gnana997/periscope/internal/audit"
	iamengine "github.com/gnana997/periscope/internal/awseks/iam"
	identitypkg "github.com/gnana997/periscope/internal/awseks/identity"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

const testCallerArn = "arn:aws:iam::111111111111:role/periscope-server"

// withAllRBACAllowed installs a fake k8s clientset that approves
// every SelfSubjectAccessReview. The capabilities probe walks the
// RBAC path before the IAM probe; without a satisfied SAR, every
// IAM-probe test would short-circuit on RBAC_DENIED. The original
// newClientFn is restored on cleanup.
func withAllRBACAllowed(t *testing.T) {
	t.Helper()
	fakeCS := fake.NewSimpleClientset()
	fakeCS.PrependReactor("create", "selfsubjectaccessreviews",
		func(_ k8stesting.Action) (bool, runtime.Object, error) {
			return true, &authv1.SelfSubjectAccessReview{
				Status: authv1.SubjectAccessReviewStatus{Allowed: true},
			}, nil
		})
	restore := k8s.SetNewClientFnForTest(fakeCS)
	t.Cleanup(restore)
}

// warmIdentityManager forces the per-cluster identity.Manager and
// its SA informer to sync before the test exercises the capabilities
// handler. Without this, the first call enters the
// INFORMER_WARMING branch deterministically (the SA-informer sync
// goroutine hasn't flipped yet) and clobbers the IAM-probe result.
//
// Polls Ensure() with a short deadline; against the empty fake
// clientset the informer syncs in milliseconds.
func warmIdentityManager(t *testing.T, cache *iamEngineCache, cluster string) {
	t.Helper()
	mgr, err := cache.identityC.For(context.Background(), clusters.Cluster{Name: cluster})
	if err != nil {
		t.Fatalf("warmIdentityManager: identityCache.For: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := mgr.Ensure(context.Background()); err == nil || !errors.Is(err, identityErrIRSAListerNotReady) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Log("warmIdentityManager: informer didn't sync within 2s; tests may see INFORMER_WARMING")
}

// identityErrIRSAListerNotReady aliases the package-level sentinel
// so the polling helper above can errors.Is against it without
// littering the rest of the test file with an extra import.
var identityErrIRSAListerNotReady = identitypkg.ErrIRSAListerNotReady

// callerIdentityOK returns a fake STS that responds with the test
// caller ARN. The capabilities probe normalizes assumed-role
// session ARNs to role ARNs; this stub already returns role form.
func callerIdentityOK() *fakeIdentitySTS {
	return &fakeIdentitySTS{
		getCallerIdentity: func(*sts.GetCallerIdentityInput) (*sts.GetCallerIdentityOutput, error) {
			return &sts.GetCallerIdentityOutput{Arn: aws.String(testCallerArn)}, nil
		},
	}
}

// simulateAll builds an IAM SimulatePrincipalPolicy stub that
// returns decision for every action in the request. Pass
// iamtypes.PolicyEvaluationDecisionTypeAllowed for the happy path.
func simulateAll(decision iamtypes.PolicyEvaluationDecisionType) func(*iam.SimulatePrincipalPolicyInput) (*iam.SimulatePrincipalPolicyOutput, error) {
	return func(in *iam.SimulatePrincipalPolicyInput) (*iam.SimulatePrincipalPolicyOutput, error) {
		out := &iam.SimulatePrincipalPolicyOutput{}
		for _, a := range in.ActionNames {
			out.EvaluationResults = append(out.EvaluationResults, iamtypes.EvaluationResult{
				EvalActionName: aws.String(a),
				EvalDecision:   decision,
			})
		}
		return out, nil
	}
}

// simulateDeniedSubset builds a stub that denies only the named
// subset; remaining actions return Allowed. Tests use this to
// exercise the MISSING_IAM_PERMS path with a realistic shape.
func simulateDeniedSubset(denied ...string) func(*iam.SimulatePrincipalPolicyInput) (*iam.SimulatePrincipalPolicyOutput, error) {
	deniedSet := map[string]struct{}{}
	for _, d := range denied {
		deniedSet[d] = struct{}{}
	}
	return func(in *iam.SimulatePrincipalPolicyInput) (*iam.SimulatePrincipalPolicyOutput, error) {
		out := &iam.SimulatePrincipalPolicyOutput{}
		for _, a := range in.ActionNames {
			decision := iamtypes.PolicyEvaluationDecisionTypeAllowed
			if _, ok := deniedSet[a]; ok {
				decision = iamtypes.PolicyEvaluationDecisionTypeImplicitDeny
			}
			out.EvaluationResults = append(out.EvaluationResults, iamtypes.EvaluationResult{
				EvalActionName: aws.String(a),
				EvalDecision:   decision,
			})
		}
		return out, nil
	}
}

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

func TestCapabilitiesHandler_IAMProbe_AllAllowed(t *testing.T) {
	resetCallerArnCache()
	t.Cleanup(resetCallerArnCache)
	withAllRBACAllowed(t)

	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	fIAM := &fakeIdentityIAM{
		simulatePrincipalPolicy: simulateAll(iamtypes.PolicyEvaluationDecisionTypeAllowed),
	}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, fIAM, callerIdentityOK())
	defer cleanup()
	warmIdentityManager(t, cache, "test")
	probe := newCapabilitiesCache()

	h := identityCapabilitiesHandler(reg, cache, probe, awsAccessConfig{IAMProbe: true}, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/capabilities")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got iamengine.CapabilitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	tab := got.Features[iamengine.FeatureAwsAccessTab]
	if !tab.Available {
		t.Errorf("awsAccessTab.Available = false, want true (probe says all perms allowed); got %+v", tab)
	}
	if tab.Reason != "" {
		t.Errorf("awsAccessTab.Reason = %q, want empty when all allowed", tab.Reason)
	}
	if len(tab.Missing) != 0 {
		t.Errorf("awsAccessTab.Missing = %v, want empty", tab.Missing)
	}
}

func TestCapabilitiesHandler_IAMProbe_MissingPerms(t *testing.T) {
	resetCallerArnCache()
	t.Cleanup(resetCallerArnCache)
	withAllRBACAllowed(t)

	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	fIAM := &fakeIdentityIAM{
		simulatePrincipalPolicy: simulateDeniedSubset("iam:GetPolicy", "iam:GetPolicyVersion"),
	}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, fIAM, callerIdentityOK())
	defer cleanup()
	warmIdentityManager(t, cache, "test")
	probe := newCapabilitiesCache()

	h := identityCapabilitiesHandler(reg, cache, probe, awsAccessConfig{IAMProbe: true}, audit.New(sink))
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
		t.Errorf("awsAccessTab.Available = true, want false when perms are denied; got %+v", tab)
	}
	if tab.Reason != iamengine.ReasonMissingIAMPerms {
		t.Errorf("awsAccessTab.Reason = %q, want %q", tab.Reason, iamengine.ReasonMissingIAMPerms)
	}
	wantMissing := map[string]bool{"iam:GetPolicy": true, "iam:GetPolicyVersion": true}
	if len(tab.Missing) != 2 {
		t.Fatalf("awsAccessTab.Missing = %v, want exactly 2 entries", tab.Missing)
	}
	for _, m := range tab.Missing {
		if !wantMissing[m] {
			t.Errorf("unexpected Missing entry %q", m)
		}
	}
	// Reverse-lookup lock mirrors the tab lock.
	rev := got.Features[iamengine.FeatureReverseLookup]
	if rev.Available || rev.Reason != iamengine.ReasonMissingIAMPerms {
		t.Errorf("reverseLookup missing-perms lock not applied: %+v", rev)
	}
}

func TestCapabilitiesHandler_IAMProbe_ProbeItselfDenied(t *testing.T) {
	resetCallerArnCache()
	t.Cleanup(resetCallerArnCache)
	withAllRBACAllowed(t)

	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	// SimulatePrincipalPolicy itself denied — falls back to optimistic.
	fIAM := &fakeIdentityIAM{
		simulatePrincipalPolicy: func(*iam.SimulatePrincipalPolicyInput) (*iam.SimulatePrincipalPolicyOutput, error) {
			return nil, errors.New("AccessDenied: User is not authorized to perform iam:SimulatePrincipalPolicy")
		},
	}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, fIAM, callerIdentityOK())
	defer cleanup()
	warmIdentityManager(t, cache, "test")
	probe := newCapabilitiesCache()

	h := identityCapabilitiesHandler(reg, cache, probe, awsAccessConfig{IAMProbe: true}, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/capabilities")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got iamengine.CapabilitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	tab := got.Features[iamengine.FeatureAwsAccessTab]
	if !tab.Available {
		t.Errorf("awsAccessTab.Available = false, want true (optimistic fallback); got %+v", tab)
	}
	if tab.Note == "" {
		t.Errorf("awsAccessTab.Note is empty; should mention probe couldn't run")
	}
}

func TestCapabilitiesHandler_IAMProbeDisabled_OptimisticAvailable(t *testing.T) {
	resetCallerArnCache()
	t.Cleanup(resetCallerArnCache)
	withAllRBACAllowed(t)

	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{}, callerIdentityOK())
	defer cleanup()
	warmIdentityManager(t, cache, "test")
	probe := newCapabilitiesCache()

	// Probe disabled — handler must NOT call SimulatePrincipalPolicy.
	h := identityCapabilitiesHandler(reg, cache, probe, awsAccessConfig{IAMProbe: false}, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/capabilities")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got iamengine.CapabilitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	tab := got.Features[iamengine.FeatureAwsAccessTab]
	if !tab.Available {
		t.Errorf("awsAccessTab.Available = false, want true (probe disabled, optimistic); got %+v", tab)
	}
	if tab.Reason != iamengine.ReasonIAMProbeDisabled {
		t.Errorf("awsAccessTab.Reason = %q, want %q", tab.Reason, iamengine.ReasonIAMProbeDisabled)
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
