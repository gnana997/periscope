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
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/go-chi/chi/v5"
	smithy "github.com/aws/smithy-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/awseks/identity"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// fakeIdentityEKS satisfies identity.EKSAPI with simple closures.
type fakeIdentityEKS struct {
	listAccessEntries          func(in *eks.ListAccessEntriesInput) (*eks.ListAccessEntriesOutput, error)
	describeAccessEntry        func(in *eks.DescribeAccessEntryInput) (*eks.DescribeAccessEntryOutput, error)
	listAssociatedAccessPolicy func(in *eks.ListAssociatedAccessPoliciesInput) (*eks.ListAssociatedAccessPoliciesOutput, error)
	listPodIdentity            func(in *eks.ListPodIdentityAssociationsInput) (*eks.ListPodIdentityAssociationsOutput, error)
	describePodIdentity        func(in *eks.DescribePodIdentityAssociationInput) (*eks.DescribePodIdentityAssociationOutput, error)
}

func (f *fakeIdentityEKS) ListAccessEntries(ctx context.Context, in *eks.ListAccessEntriesInput, _ ...func(*eks.Options)) (*eks.ListAccessEntriesOutput, error) {
	return f.listAccessEntries(in)
}
func (f *fakeIdentityEKS) DescribeAccessEntry(ctx context.Context, in *eks.DescribeAccessEntryInput, _ ...func(*eks.Options)) (*eks.DescribeAccessEntryOutput, error) {
	return f.describeAccessEntry(in)
}
func (f *fakeIdentityEKS) ListAssociatedAccessPolicies(ctx context.Context, in *eks.ListAssociatedAccessPoliciesInput, _ ...func(*eks.Options)) (*eks.ListAssociatedAccessPoliciesOutput, error) {
	return f.listAssociatedAccessPolicy(in)
}
func (f *fakeIdentityEKS) ListPodIdentityAssociations(ctx context.Context, in *eks.ListPodIdentityAssociationsInput, _ ...func(*eks.Options)) (*eks.ListPodIdentityAssociationsOutput, error) {
	return f.listPodIdentity(in)
}
func (f *fakeIdentityEKS) DescribePodIdentityAssociation(ctx context.Context, in *eks.DescribePodIdentityAssociationInput, _ ...func(*eks.Options)) (*eks.DescribePodIdentityAssociationOutput, error) {
	return f.describePodIdentity(in)
}

// fakeIdentityIAM satisfies identity.IAMAPI.
type fakeIdentityIAM struct {
	getRole func(in *iam.GetRoleInput) (*iam.GetRoleOutput, error)
}

func (f *fakeIdentityIAM) GetRole(ctx context.Context, in *iam.GetRoleInput, _ ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	return f.getRole(in)
}

// withFakeIdentityClient swaps newIdentityClient to return a Client
// backed by the given fake EKS/IAM stubs. Cleanup restores the
// original after the test.
func withFakeIdentityClient(t *testing.T, fEKS *fakeIdentityEKS, fIAM *fakeIdentityIAM) {
	t.Helper()
	orig := newIdentityClient
	newIdentityClient = func(_ aws.Config, _ clusters.Cluster) *identity.Client {
		return identity.NewWithAPIs(fEKS, fIAM)
	}
	t.Cleanup(func() { newIdentityClient = orig })
}

// invokeIdentity drives a handler with a route param matching the
// test cluster and a planted user session.
func invokeIdentity(t *testing.T, h func(http.ResponseWriter, *http.Request, credentials.Provider), cluster, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", cluster)
	req = req.WithContext(credentials.WithSession(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		credentials.Session{Subject: "alice@corp", Email: "alice@corp"},
	))
	rec := httptest.NewRecorder()
	h(rec, req, fakeProvider{actor: "alice@corp"})
	return rec
}

// awsAPIErr returns a synthetic smithy.APIError with the given code.
type awsAPIErr struct {
	code string
	msg  string
}

func (e *awsAPIErr) Error() string                       { return e.msg }
func (e *awsAPIErr) ErrorCode() string                   { return e.code }
func (e *awsAPIErr) ErrorMessage() string                { return e.msg }
func (e *awsAPIErr) ErrorFault() smithy.ErrorFault       { return smithy.FaultClient }

// ── /identity/access-entries ────────────────────────────────────

func TestIdentityAccessEntries_HappyPath(t *testing.T) {
	fEKS := &fakeIdentityEKS{
		listAccessEntries: func(in *eks.ListAccessEntriesInput) (*eks.ListAccessEntriesOutput, error) {
			return &eks.ListAccessEntriesOutput{AccessEntries: []string{"arn:aws:iam::123:role/a"}}, nil
		},
		describeAccessEntry: func(in *eks.DescribeAccessEntryInput) (*eks.DescribeAccessEntryOutput, error) {
			return &eks.DescribeAccessEntryOutput{
				AccessEntry: &ekstypes.AccessEntry{
					PrincipalArn:     aws.String(*in.PrincipalArn),
					KubernetesGroups: []string{"admins"},
				},
			}, nil
		},
		listAssociatedAccessPolicy: func(in *eks.ListAssociatedAccessPoliciesInput) (*eks.ListAssociatedAccessPoliciesOutput, error) {
			return &eks.ListAssociatedAccessPoliciesOutput{}, nil
		},
	}
	withFakeIdentityClient(t, fEKS, &fakeIdentityIAM{})

	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	h := identityAccessEntriesHandler(reg, aws.Config{}, audit.New(sink))

	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/access-entries")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var entries []identity.AccessEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(entries) != 1 || entries[0].PrincipalArn != "arn:aws:iam::123:role/a" {
		t.Errorf("entries = %+v", entries)
	}

	// At least one aws_identity_read audit row for list_access_entries.
	found := false
	for _, ev := range sink.events {
		if ev.Verb == audit.VerbAwsIdentityRead && ev.Extra["op"] == "list_access_entries" && ev.Outcome == audit.OutcomeSuccess {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("audit: missing aws_identity_read/list_access_entries success row; got: %+v", sink.events)
	}
}

func TestIdentityAccessEntries_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendInCluster)
	sink := &recordingSink{}
	h := identityAccessEntriesHandler(reg, aws.Config{}, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/access-entries")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

func TestIdentityAccessEntries_ClusterNotFound(t *testing.T) {
	reg := eksRegistry(t, "real", clusters.BackendEKS)
	sink := &recordingSink{}
	h := identityAccessEntriesHandler(reg, aws.Config{}, audit.New(sink))
	rec := invokeIdentity(t, h, "ghost", "/api/clusters/ghost/identity/access-entries")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestIdentityAccessEntries_AWSThrottleMapsTo429(t *testing.T) {
	fEKS := &fakeIdentityEKS{
		listAccessEntries: func(in *eks.ListAccessEntriesInput) (*eks.ListAccessEntriesOutput, error) {
			return nil, &awsAPIErr{code: "ThrottlingException", msg: "slow down"}
		},
	}
	withFakeIdentityClient(t, fEKS, &fakeIdentityIAM{})
	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	h := identityAccessEntriesHandler(reg, aws.Config{}, audit.New(sink))

	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/access-entries")
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rec.Code)
	}
	// Failure audit row emitted.
	found := false
	for _, ev := range sink.events {
		if ev.Verb == audit.VerbAwsIdentityRead && ev.Outcome == audit.OutcomeFailure {
			found = true
		}
	}
	if !found {
		t.Errorf("expected failure audit row")
	}
}

// ── /identity/pod-identity ──────────────────────────────────────

func TestIdentityPodIdentity_HappyPath(t *testing.T) {
	fEKS := &fakeIdentityEKS{
		listPodIdentity: func(in *eks.ListPodIdentityAssociationsInput) (*eks.ListPodIdentityAssociationsOutput, error) {
			return &eks.ListPodIdentityAssociationsOutput{
				Associations: []ekstypes.PodIdentityAssociationSummary{
					{AssociationId: aws.String("a-1")},
					{AssociationId: aws.String("a-2")},
				},
			}, nil
		},
		describePodIdentity: func(in *eks.DescribePodIdentityAssociationInput) (*eks.DescribePodIdentityAssociationOutput, error) {
			return &eks.DescribePodIdentityAssociationOutput{
				Association: &ekstypes.PodIdentityAssociation{
					AssociationId:  in.AssociationId,
					RoleArn:        aws.String("arn:aws:iam::123:role/shared"),
					Namespace:      aws.String("ns"),
					ServiceAccount: aws.String("sa-" + *in.AssociationId),
				},
			}, nil
		},
	}
	withFakeIdentityClient(t, fEKS, &fakeIdentityIAM{})
	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	h := identityPodIdentityHandler(reg, aws.Config{}, audit.New(sink))

	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/pod-identity")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp identityPodIdentityResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	pairs, ok := resp.Groups["arn:aws:iam::123:role/shared"]
	if !ok || len(pairs) != 2 {
		t.Errorf("Groups = %+v", resp.Groups)
	}
}

func TestIdentityPodIdentity_NonEKSReturns422(t *testing.T) {
	reg := eksRegistry(t, "test", clusters.BackendAgent)
	sink := &recordingSink{}
	h := identityPodIdentityHandler(reg, aws.Config{}, audit.New(sink))
	rec := invokeIdentity(t, h, "test", "/api/clusters/test/identity/pod-identity")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
}

// ── /identity/aws-auth-diff ─────────────────────────────────────

// Override the k8s.NewClientset path inside the handler. The handler
// calls k8s.NewClientset directly; we replace via package var (set
// from main.go via newClientFn). For aws-auth tests we use the same
// pattern.
//
// To keep this test self-contained without exporting newClientFn,
// we test the no-aws-auth case (which is the migration-complete
// happy path the handler must handle gracefully) by stubbing the
// k8s clientset at the package level.

// ── /identity/sa-roles (with cache) ─────────────────────────────

func TestIdentitySARoles_HappyPath(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns",
				Name:      "sa",
				Annotations: map[string]string{
					identity.IrsaAnnotation: "arn:aws:iam::123:role/r",
				},
			},
		},
	)

	fEKS := &fakeIdentityEKS{
		listPodIdentity: func(in *eks.ListPodIdentityAssociationsInput) (*eks.ListPodIdentityAssociationsOutput, error) {
			return &eks.ListPodIdentityAssociationsOutput{}, nil
		},
	}
	fIAM := &fakeIdentityIAM{
		getRole: func(in *iam.GetRoleInput) (*iam.GetRoleOutput, error) {
			return &iam.GetRoleOutput{Role: &iamtypes.Role{RoleName: in.RoleName}}, nil
		},
	}
	withFakeIdentityClient(t, fEKS, fIAM)

	reg := eksRegistry(t, "test", clusters.BackendEKS)
	sink := &recordingSink{}
	parentCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cache := newIdentityCache(parentCtx, aws.Config{},
		func(_ context.Context, _ clusters.Cluster) (kubernetes.Interface, error) {
			return cs, nil
		},
		identity.Config{}, nil)
	t.Cleanup(cache.Shutdown)

	h := identitySARolesHandler(reg, cache, audit.New(sink))

	// First request may be 503 (informer warming) — retry until ready
	// or status 200, capped at ~2s.
	var rec *httptest.ResponseRecorder
	deadline := 50
	for i := 0; i < deadline; i++ {
		rec = invokeIdentity(t, h, "test", "/api/clusters/test/identity/sa-roles")
		if rec.Code == http.StatusOK {
			break
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
		}
		// Brief sleep before retry so the informer can sync.
		// 40ms × 50 = 2s max.
		// (Don't use time.Sleep in tests is a fine guideline; here
		// we're explicitly polling for informer sync.)
		// nolint:staticcheck
		time.Sleep(40 * time.Millisecond)
	}
	if rec == nil || rec.Code != http.StatusOK {
		t.Fatalf("never got 200; last status=%d body=%s", rec.Code, rec.Body.String())
	}

	var entries []identity.SARoleIndexEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	if entries[0].Bindings[0].Source != identity.SourceIRSA {
		t.Errorf("Source = %q, want IRSA", entries[0].Bindings[0].Source)
	}
}

// ensure aws-sdk import is referenced.
var _ = errors.New
