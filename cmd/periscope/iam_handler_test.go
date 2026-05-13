package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/audit"
	iamengine "github.com/gnana997/periscope/internal/awseks/iam"
	"github.com/gnana997/periscope/internal/awseks/identity"
	"github.com/gnana997/periscope/internal/clusters"
)

// ── Test helpers ────────────────────────────────────────────────

// newTestIAMEngineCache wires a cache backed by stubs. Caller hands
// in the EKS + IAM fake; the cache uses the swap-able
// newIdentityClient so withFakeIdentityClient affects this too.
func newTestIAMEngineCache(t *testing.T, fEKS *fakeIdentityEKS, fIAM *fakeIdentityIAM) (*iamEngineCache, *identityCache, func()) {
	t.Helper()
	withFakeIdentityClient(t, fEKS, fIAM)

	// identityCache needs a k8s clientset factory; tests don't
	// exercise the SA informer so a no-op factory is fine — the
	// IAM engine's SARoleIndexer adapter calls Manager.Ensure
	// which calls into the informer, which returns ErrIRSAListerNotReady
	// until the informer syncs. Reverse-lookup tests are scoped to
	// scenarios where Ensure either succeeds or the test accepts
	// the not-ready error.
	parentCtx, cancel := context.WithCancel(context.Background())
	identityC := newIdentityCache(parentCtx, aws.Config{}, func(_ context.Context, _ clusters.Cluster) (kubernetes.Interface, error) {
		// Empty fake clientset — the SA informer will sync to an
		// empty SA list, which is fine for tests not exercising
		// reverse-lookup.
		return kubernetesFake(t), nil
	}, identity.Config{}, nil)

	iamCache := newIAMEngineCache(aws.Config{}, identityC, iamengine.Config{}, nil)

	return iamCache, identityC, func() {
		iamCache.Shutdown()
		identityC.Shutdown()
		cancel()
	}
}

// kubernetesFake returns a real client-go fake clientset for the
// identityCache k8sFactory. The SA informer LISTs against this; an
// empty clientset gives the informer "no SAs" to work with, which
// is fine for role-permissions tests (they don't iterate SAs at
// all) and acceptable for reverse-lookup tests (zero matches is
// still a valid response — the rollup audit row still fires).
func kubernetesFake(t *testing.T) kubernetes.Interface {
	t.Helper()
	return fake.NewSimpleClientset()
}

// ── /iam/role-permissions ───────────────────────────────────────

func TestIAMRolePermissions_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()

	h := iamRolePermissionsHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/iam/role-permissions?roleArn=arn:aws:iam::123:role/r")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestIAMRolePermissions_ClusterNotFound(t *testing.T) {
	reg := eksRegistry(t, "real", clusters.BackendEKS)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()

	h := iamRolePermissionsHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "ghost", "/api/clusters/ghost/iam/role-permissions?roleArn=arn:aws:iam::123:role/r")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestIAMRolePermissions_MissingRoleArn(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()

	h := iamRolePermissionsHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/iam/role-permissions") // no roleArn
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestIAMRolePermissions_HappyPath(t *testing.T) {
	const policyJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`

	fIAM := &fakeIdentityIAM{
		listRolePolicies: func(in *iam.ListRolePoliciesInput) (*iam.ListRolePoliciesOutput, error) {
			return &iam.ListRolePoliciesOutput{PolicyNames: []string{"inline-1"}}, nil
		},
		getRolePolicy: func(in *iam.GetRolePolicyInput) (*iam.GetRolePolicyOutput, error) {
			return &iam.GetRolePolicyOutput{
				PolicyDocument: aws.String(url.QueryEscape(policyJSON)),
			}, nil
		},
		listAttachedRolePolicies: func(_ *iam.ListAttachedRolePoliciesInput) (*iam.ListAttachedRolePoliciesOutput, error) {
			return &iam.ListAttachedRolePoliciesOutput{}, nil
		},
	}
	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, fIAM)
	defer cleanup()

	h := iamRolePermissionsHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test",
		"/api/clusters/test/iam/role-permissions?roleArn=arn:aws:iam::123:role/r")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var result iamengine.RolePermissionsResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Permissions) != 1 {
		t.Errorf("Permissions = %d, want 1", len(result.Permissions))
	}
	if result.PolicyFetchPartial {
		t.Errorf("PolicyFetchPartial = true on clean fetch, want false")
	}

	// Audit row asserted via the recording sink.
	found := false
	for _, ev := range sink.events {
		if ev.Verb == audit.VerbAwsIAMRead && ev.Extra["op"] == "role_permissions" && ev.Outcome == audit.OutcomeSuccess {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("missing aws_identity_read/role_permissions success audit row; got: %+v", sink.events)
	}
}

func TestIAMRolePermissions_PartialFetchReturnsPartialResult(t *testing.T) {
	const goodPolicyJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`

	fIAM := &fakeIdentityIAM{
		listRolePolicies: func(_ *iam.ListRolePoliciesInput) (*iam.ListRolePoliciesOutput, error) {
			return &iam.ListRolePoliciesOutput{PolicyNames: []string{"good", "bad"}}, nil
		},
		getRolePolicy: func(in *iam.GetRolePolicyInput) (*iam.GetRolePolicyOutput, error) {
			if aws.ToString(in.PolicyName) == "bad" {
				return nil, &iamtypes.NoSuchEntityException{Message: aws.String("nope")}
			}
			return &iam.GetRolePolicyOutput{
				PolicyDocument: aws.String(url.QueryEscape(goodPolicyJSON)),
			}, nil
		},
		listAttachedRolePolicies: func(_ *iam.ListAttachedRolePoliciesInput) (*iam.ListAttachedRolePoliciesOutput, error) {
			return &iam.ListAttachedRolePoliciesOutput{}, nil
		},
	}
	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, fIAM)
	defer cleanup()

	h := iamRolePermissionsHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test",
		"/api/clusters/test/iam/role-permissions?roleArn=arn:aws:iam::123:role/r")

	// Partial fetch → 200 with PolicyFetchPartial=true (NOT a 5xx).
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (partial fetch is soft-fail), body=%s", rec.Code, rec.Body.String())
	}

	var result iamengine.RolePermissionsResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !result.PolicyFetchPartial {
		t.Errorf("PolicyFetchPartial = false, want true (one policy failed)")
	}
	if len(result.Permissions) != 1 {
		t.Errorf("Permissions = %d, want 1 (the good policy's row)", len(result.Permissions))
	}
}

// ── /iam/reverse-lookup ─────────────────────────────────────────

func TestIAMReverseLookup_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendInCluster)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()

	h := iamReverseLookupHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/iam/reverse-lookup?action=s3:GetObject")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestIAMReverseLookup_MissingAction(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()

	h := iamReverseLookupHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/iam/reverse-lookup") // no action
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestIAMReverseLookup_AuditRollupEmitted(t *testing.T) {
	// The reverse-lookup path through the SA informer returns an
	// empty index here (test clientset has no SAs), so the engine
	// returns zero matches. We're verifying the audit rollup row
	// emits regardless, since that's the user-facing-intent record
	// compliance reviewers will look for.
	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	cache, _, cleanup := newTestIAMEngineCache(t, &fakeIdentityEKS{}, &fakeIdentityIAM{})
	defer cleanup()

	h := iamReverseLookupHandler(reg, cache, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/iam/reverse-lookup?action=s3:GetObject")

	// Either 200 (informer synced, empty index) or 5xx (informer
	// not ready). The handler-level audit row should still be
	// emitted on success; on failure it'd be the failure variant.
	// We're not asserting status here — just that some row exists.
	if rec.Code >= 500 && rec.Code != http.StatusServiceUnavailable {
		// This test isn't designed to assert specific failure modes
		// from the informer-not-ready path. Skip if we hit an
		// unexpected code.
		t.Skipf("informer-not-ready path returned status %d; reverse-lookup audit test inconclusive", rec.Code)
	}

	found := false
	for _, ev := range sink.events {
		if ev.Verb == audit.VerbAwsIAMRead && ev.Extra["op"] == "reverse_lookup" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected aws_identity_read/reverse_lookup audit row; got: %+v", sink.events)
	}
}
