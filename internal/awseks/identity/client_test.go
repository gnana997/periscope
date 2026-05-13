package identity

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func TestRoleNameFromArn(t *testing.T) {
	cases := []struct {
		in   string
		name string
		ok   bool
	}{
		{"arn:aws:iam::123:role/eks-pod", "eks-pod", true},
		{"arn:aws:iam::123:role/path/to/eks-pod", "eks-pod", true},
		{"arn:aws-us-gov:iam::123:role/Foo", "Foo", true},
		{"arn:aws:iam::123:user/alice", "", false},
		{"arn:aws:sts::123:assumed-role/foo/sess", "", false},
		{"not-an-arn", "", false},
		{"", "", false},
		{"arn:aws:iam::123:role/", "", false},
	}
	for _, tc := range cases {
		gotName, gotOk := roleNameFromArn(tc.in)
		if gotName != tc.name || gotOk != tc.ok {
			t.Errorf("roleNameFromArn(%q) = (%q, %v), want (%q, %v)", tc.in, gotName, gotOk, tc.name, tc.ok)
		}
	}
}

// stubIAM lets us drive RoleExists' decision tree (and now the
// policy-fetch methods added in #187) without touching AWS.
//
// Each AWS SDK method is backed by an optional closure so a given
// test can opt into stubbing just the method(s) it exercises. The
// legacy resp / err fields drive GetRole, kept for backward
// compatibility with the original RoleExists tests.
type stubIAM struct {
	resp *iam.GetRoleOutput
	err  error

	listRolePolicies         func(*iam.ListRolePoliciesInput) (*iam.ListRolePoliciesOutput, error)
	getRolePolicy            func(*iam.GetRolePolicyInput) (*iam.GetRolePolicyOutput, error)
	listAttachedRolePolicies func(*iam.ListAttachedRolePoliciesInput) (*iam.ListAttachedRolePoliciesOutput, error)
	getPolicy                func(*iam.GetPolicyInput) (*iam.GetPolicyOutput, error)
	getPolicyVersion         func(*iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error)
}

func (s *stubIAM) GetRole(ctx context.Context, in *iam.GetRoleInput, opts ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	return s.resp, s.err
}

func (s *stubIAM) ListRolePolicies(ctx context.Context, in *iam.ListRolePoliciesInput, opts ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	if s.listRolePolicies == nil {
		return &iam.ListRolePoliciesOutput{}, nil
	}
	return s.listRolePolicies(in)
}

func (s *stubIAM) GetRolePolicy(ctx context.Context, in *iam.GetRolePolicyInput, opts ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error) {
	if s.getRolePolicy == nil {
		return nil, fmt.Errorf("stub: GetRolePolicy not configured")
	}
	return s.getRolePolicy(in)
}

func (s *stubIAM) ListAttachedRolePolicies(ctx context.Context, in *iam.ListAttachedRolePoliciesInput, opts ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	if s.listAttachedRolePolicies == nil {
		return &iam.ListAttachedRolePoliciesOutput{}, nil
	}
	return s.listAttachedRolePolicies(in)
}

func (s *stubIAM) GetPolicy(ctx context.Context, in *iam.GetPolicyInput, opts ...func(*iam.Options)) (*iam.GetPolicyOutput, error) {
	if s.getPolicy == nil {
		return nil, fmt.Errorf("stub: GetPolicy not configured")
	}
	return s.getPolicy(in)
}

func (s *stubIAM) GetPolicyVersion(ctx context.Context, in *iam.GetPolicyVersionInput, opts ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error) {
	if s.getPolicyVersion == nil {
		return nil, fmt.Errorf("stub: GetPolicyVersion not configured")
	}
	return s.getPolicyVersion(in)
}

func TestRoleExists_FoundReturnsTrueNil(t *testing.T) {
	c := NewWithAPIs(nil, &stubIAM{
		resp: &iam.GetRoleOutput{Role: &iamtypes.Role{RoleName: aws.String("x")}},
	})
	got, err := c.RoleExists(context.Background(), "arn:aws:iam::123:role/x")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !got {
		t.Errorf("got false, want true")
	}
}

func TestRoleExists_NotFoundReturnsFalseNil(t *testing.T) {
	c := NewWithAPIs(nil, &stubIAM{
		err: &iamtypes.NoSuchEntityException{Message: aws.String("nope")},
	})
	got, err := c.RoleExists(context.Background(), "arn:aws:iam::123:role/deleted")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got {
		t.Errorf("got true, want false (NoSuchEntity)")
	}
}

func TestRoleExists_OtherErrPropagates(t *testing.T) {
	c := NewWithAPIs(nil, &stubIAM{err: errors.New("throttled")})
	_, err := c.RoleExists(context.Background(), "arn:aws:iam::123:role/x")
	if err == nil {
		t.Fatalf("want error, got nil")
	}
}

func TestRoleExists_BadArnReturnsFalseNil(t *testing.T) {
	// A non-IAM ARN shouldn't trigger an IAM call at all.
	c := NewWithAPIs(nil, &stubIAM{err: errors.New("should not be called")})
	got, err := c.RoleExists(context.Background(), "not-an-arn")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got {
		t.Errorf("got true, want false")
	}
}

// stubEKS — only the methods we need for the SDK adapter tests.
type stubEKS struct {
	listAccessEntries          func(*eks.ListAccessEntriesInput) (*eks.ListAccessEntriesOutput, error)
	describeAccessEntry        func(*eks.DescribeAccessEntryInput) (*eks.DescribeAccessEntryOutput, error)
	listAssociatedAccessPolicy func(*eks.ListAssociatedAccessPoliciesInput) (*eks.ListAssociatedAccessPoliciesOutput, error)
	listPodIdentity            func(*eks.ListPodIdentityAssociationsInput) (*eks.ListPodIdentityAssociationsOutput, error)
	describePodIdentity        func(*eks.DescribePodIdentityAssociationInput) (*eks.DescribePodIdentityAssociationOutput, error)
}

func (s *stubEKS) ListAccessEntries(ctx context.Context, in *eks.ListAccessEntriesInput, opts ...func(*eks.Options)) (*eks.ListAccessEntriesOutput, error) {
	return s.listAccessEntries(in)
}
func (s *stubEKS) DescribeAccessEntry(ctx context.Context, in *eks.DescribeAccessEntryInput, opts ...func(*eks.Options)) (*eks.DescribeAccessEntryOutput, error) {
	return s.describeAccessEntry(in)
}
func (s *stubEKS) ListAssociatedAccessPolicies(ctx context.Context, in *eks.ListAssociatedAccessPoliciesInput, opts ...func(*eks.Options)) (*eks.ListAssociatedAccessPoliciesOutput, error) {
	return s.listAssociatedAccessPolicy(in)
}
func (s *stubEKS) ListPodIdentityAssociations(ctx context.Context, in *eks.ListPodIdentityAssociationsInput, opts ...func(*eks.Options)) (*eks.ListPodIdentityAssociationsOutput, error) {
	return s.listPodIdentity(in)
}
func (s *stubEKS) DescribePodIdentityAssociation(ctx context.Context, in *eks.DescribePodIdentityAssociationInput, opts ...func(*eks.Options)) (*eks.DescribePodIdentityAssociationOutput, error) {
	return s.describePodIdentity(in)
}

func TestListAccessEntries_Paginates(t *testing.T) {
	page := 0
	s := &stubEKS{
		listAccessEntries: func(in *eks.ListAccessEntriesInput) (*eks.ListAccessEntriesOutput, error) {
			page++
			switch page {
			case 1:
				return &eks.ListAccessEntriesOutput{
					AccessEntries: []string{"a", "b"},
					NextToken:     aws.String("p2"),
				}, nil
			case 2:
				return &eks.ListAccessEntriesOutput{
					AccessEntries: []string{"c"},
				}, nil
			}
			t.Fatalf("page %d unexpected", page)
			return nil, nil
		},
	}
	c := NewWithAPIs(s, nil)
	got, err := c.ListAccessEntries(context.Background(), "cluster1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("got %+v", got)
	}
}

func TestListPodIdentityAssociations_FetchesFullDetails(t *testing.T) {
	s := &stubEKS{
		listPodIdentity: func(in *eks.ListPodIdentityAssociationsInput) (*eks.ListPodIdentityAssociationsOutput, error) {
			return &eks.ListPodIdentityAssociationsOutput{
				Associations: []ekstypes.PodIdentityAssociationSummary{
					{AssociationId: aws.String("a-1")},
					{AssociationId: aws.String("a-2")},
				},
			}, nil
		},
		describePodIdentity: func(in *eks.DescribePodIdentityAssociationInput) (*eks.DescribePodIdentityAssociationOutput, error) {
			id := aws.ToString(in.AssociationId)
			return &eks.DescribePodIdentityAssociationOutput{
				Association: &ekstypes.PodIdentityAssociation{
					AssociationId:  aws.String(id),
					RoleArn:        aws.String("arn:aws:iam::123:role/r-" + id),
					Namespace:      aws.String("ns"),
					ServiceAccount: aws.String("sa-" + id),
					ClusterName:    aws.String("c1"),
				},
			}, nil
		},
	}
	c := NewWithAPIs(s, nil)
	got, err := c.ListPodIdentityAssociations(context.Background(), "c1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 assocs, got %d", len(got))
	}
	if got[0].RoleArn != "arn:aws:iam::123:role/r-a-1" {
		t.Errorf("got[0].RoleArn = %q", got[0].RoleArn)
	}
}

func TestListAssociatedAccessPolicies_DecodesScope(t *testing.T) {
	s := &stubEKS{
		listAssociatedAccessPolicy: func(in *eks.ListAssociatedAccessPoliciesInput) (*eks.ListAssociatedAccessPoliciesOutput, error) {
			return &eks.ListAssociatedAccessPoliciesOutput{
				AssociatedAccessPolicies: []ekstypes.AssociatedAccessPolicy{
					{
						PolicyArn: aws.String("arn:aws:eks::aws:cluster-access-policy/AmazonEKSAdminPolicy"),
						AccessScope: &ekstypes.AccessScope{
							Type:       ekstypes.AccessScopeTypeNamespace,
							Namespaces: []string{"dev", "staging"},
						},
					},
				},
			}, nil
		},
	}
	c := NewWithAPIs(s, nil)
	got, err := c.ListAssociatedAccessPolicies(context.Background(), "c1", "arn:aws:iam::123:role/foo")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1, got %d", len(got))
	}
	if got[0].AccessScope != "namespace" {
		t.Errorf("scope = %q", got[0].AccessScope)
	}
	if len(got[0].Namespaces) != 2 {
		t.Errorf("namespaces = %v", got[0].Namespaces)
	}
}

// ── PolicyFetcher implementations (#187) ─────────────────────────

// Helper: URL-encode a policy document the way AWS would in
// GetRolePolicy/GetPolicyVersion responses, so test fixtures match
// the wire shape the client must decode.
func urlEncodePolicy(jsonDoc string) string {
	return url.QueryEscape(jsonDoc)
}

const samplePolicyJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":"*"}]}`

// ListRolePolicies: single page, names returned in input order.
func TestListRolePolicies_SinglePage(t *testing.T) {
	called := 0
	c := NewWithAPIs(nil, &stubIAM{
		listRolePolicies: func(in *iam.ListRolePoliciesInput) (*iam.ListRolePoliciesOutput, error) {
			called++
			if aws.ToString(in.RoleName) != "my-role" {
				t.Errorf("RoleName = %q, want my-role (extracted from ARN)", aws.ToString(in.RoleName))
			}
			return &iam.ListRolePoliciesOutput{
				PolicyNames: []string{"inline-a", "inline-b"},
			}, nil
		},
	})
	names, err := c.ListRolePolicies(context.Background(), "arn:aws:iam::123:role/my-role")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if called != 1 {
		t.Errorf("called %d times, want 1", called)
	}
	if len(names) != 2 || names[0] != "inline-a" || names[1] != "inline-b" {
		t.Errorf("names = %v, want [inline-a, inline-b]", names)
	}
}

// ListRolePolicies: paginated across multiple Marker pages.
func TestListRolePolicies_Pagination(t *testing.T) {
	page := 0
	c := NewWithAPIs(nil, &stubIAM{
		listRolePolicies: func(in *iam.ListRolePoliciesInput) (*iam.ListRolePoliciesOutput, error) {
			page++
			switch page {
			case 1:
				return &iam.ListRolePoliciesOutput{
					PolicyNames: []string{"p1"},
					IsTruncated: true,
					Marker:      aws.String("page2"),
				}, nil
			case 2:
				if aws.ToString(in.Marker) != "page2" {
					t.Errorf("page 2 Marker = %q, want page2", aws.ToString(in.Marker))
				}
				return &iam.ListRolePoliciesOutput{
					PolicyNames: []string{"p2", "p3"},
					IsTruncated: false,
				}, nil
			}
			t.Fatalf("unexpected page %d", page)
			return nil, nil
		},
	})
	names, err := c.ListRolePolicies(context.Background(), "arn:aws:iam::123:role/r")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(names) != 3 || names[0] != "p1" || names[2] != "p3" {
		t.Errorf("names = %v, want [p1, p2, p3]", names)
	}
}

// ListRolePolicies: bad role ARN → typed error, no AWS call.
func TestListRolePolicies_BadARN(t *testing.T) {
	c := NewWithAPIs(nil, &stubIAM{
		listRolePolicies: func(_ *iam.ListRolePoliciesInput) (*iam.ListRolePoliciesOutput, error) {
			t.Fatal("stub called for bad ARN; expected early return")
			return nil, nil
		},
	})
	_, err := c.ListRolePolicies(context.Background(), "not-an-arn")
	if err == nil {
		t.Fatal("want error for bad ARN")
	}
}

// ListRolePolicies: AWS error propagates with role-name context.
func TestListRolePolicies_ErrorPropagates(t *testing.T) {
	c := NewWithAPIs(nil, &stubIAM{
		listRolePolicies: func(_ *iam.ListRolePoliciesInput) (*iam.ListRolePoliciesOutput, error) {
			return nil, errors.New("AccessDenied")
		},
	})
	_, err := c.ListRolePolicies(context.Background(), "arn:aws:iam::123:role/r")
	if err == nil || !errorContains(err, "AccessDenied") {
		t.Errorf("err = %v, want wrap of AccessDenied", err)
	}
}

// GetRolePolicy: URL-encoded document is decoded into raw JSON bytes
// that downstream ParsePolicyDocument can consume directly.
func TestGetRolePolicy_URLDecodes(t *testing.T) {
	encoded := urlEncodePolicy(samplePolicyJSON)
	c := NewWithAPIs(nil, &stubIAM{
		getRolePolicy: func(in *iam.GetRolePolicyInput) (*iam.GetRolePolicyOutput, error) {
			return &iam.GetRolePolicyOutput{
				PolicyDocument: aws.String(encoded),
			}, nil
		},
	})
	doc, err := c.GetRolePolicy(context.Background(), "arn:aws:iam::123:role/r", "inline-1")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(doc) != samplePolicyJSON {
		t.Errorf("decoded doc mismatch\n got:  %s\n want: %s", string(doc), samplePolicyJSON)
	}
}

// GetRolePolicy: nil PolicyDocument → typed error.
func TestGetRolePolicy_NilDocument(t *testing.T) {
	c := NewWithAPIs(nil, &stubIAM{
		getRolePolicy: func(_ *iam.GetRolePolicyInput) (*iam.GetRolePolicyOutput, error) {
			return &iam.GetRolePolicyOutput{}, nil
		},
	})
	_, err := c.GetRolePolicy(context.Background(), "arn:aws:iam::123:role/r", "inline")
	if err == nil {
		t.Fatal("want error for nil PolicyDocument")
	}
}

// ListAttachedRolePolicies: paginated, returns iam-package
// AttachedPolicy values directly.
func TestListAttachedRolePolicies_Pagination(t *testing.T) {
	page := 0
	c := NewWithAPIs(nil, &stubIAM{
		listAttachedRolePolicies: func(in *iam.ListAttachedRolePoliciesInput) (*iam.ListAttachedRolePoliciesOutput, error) {
			page++
			switch page {
			case 1:
				return &iam.ListAttachedRolePoliciesOutput{
					AttachedPolicies: []iamtypes.AttachedPolicy{
						{PolicyArn: aws.String("arn:aws:iam::aws:policy/A"), PolicyName: aws.String("A")},
					},
					IsTruncated: true,
					Marker:      aws.String("p2"),
				}, nil
			case 2:
				return &iam.ListAttachedRolePoliciesOutput{
					AttachedPolicies: []iamtypes.AttachedPolicy{
						{PolicyArn: aws.String("arn:aws:iam::aws:policy/B"), PolicyName: aws.String("B")},
					},
					IsTruncated: false,
				}, nil
			}
			t.Fatalf("page %d unexpected", page)
			return nil, nil
		},
	})
	got, err := c.ListAttachedRolePolicies(context.Background(), "arn:aws:iam::123:role/r")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
	if got[0].PolicyName != "A" || got[1].PolicyArn != "arn:aws:iam::aws:policy/B" {
		t.Errorf("attribution: got = %+v", got)
	}
}

// GetPolicyDocument: two-step (GetPolicy for DefaultVersionId, then
// GetPolicyVersion). Both calls happen with the correct ARN/version.
func TestGetPolicyDocument_TwoStep(t *testing.T) {
	getPolicyCalls := 0
	getPolicyVersionCalls := 0
	encoded := urlEncodePolicy(samplePolicyJSON)
	c := NewWithAPIs(nil, &stubIAM{
		getPolicy: func(in *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
			getPolicyCalls++
			return &iam.GetPolicyOutput{
				Policy: &iamtypes.Policy{
					Arn:              aws.String("arn:aws:iam::aws:policy/X"),
					DefaultVersionId: aws.String("v3"),
				},
			}, nil
		},
		getPolicyVersion: func(in *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
			getPolicyVersionCalls++
			if aws.ToString(in.VersionId) != "v3" {
				t.Errorf("VersionId = %q, want v3", aws.ToString(in.VersionId))
			}
			return &iam.GetPolicyVersionOutput{
				PolicyVersion: &iamtypes.PolicyVersion{
					Document: aws.String(encoded),
				},
			}, nil
		},
	})
	doc, err := c.GetPolicyDocument(context.Background(), "arn:aws:iam::aws:policy/X")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if getPolicyCalls != 1 || getPolicyVersionCalls != 1 {
		t.Errorf("calls: GetPolicy=%d GetPolicyVersion=%d, want 1/1",
			getPolicyCalls, getPolicyVersionCalls)
	}
	if string(doc) != samplePolicyJSON {
		t.Errorf("doc mismatch: %s", string(doc))
	}
}

// GetPolicyDocument: missing DefaultVersionId → typed error, no
// GetPolicyVersion call.
func TestGetPolicyDocument_MissingDefaultVersion(t *testing.T) {
	versionCalled := false
	c := NewWithAPIs(nil, &stubIAM{
		getPolicy: func(_ *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
			return &iam.GetPolicyOutput{Policy: &iamtypes.Policy{}}, nil
		},
		getPolicyVersion: func(_ *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
			versionCalled = true
			return nil, nil
		},
	})
	_, err := c.GetPolicyDocument(context.Background(), "arn:aws:iam::aws:policy/X")
	if err == nil {
		t.Fatal("want error for missing DefaultVersionId")
	}
	if versionCalled {
		t.Error("GetPolicyVersion called despite missing DefaultVersionId; want early return")
	}
}

// GetPolicyDocument: GetPolicy error propagates without calling
// GetPolicyVersion.
func TestGetPolicyDocument_GetPolicyErrors(t *testing.T) {
	c := NewWithAPIs(nil, &stubIAM{
		getPolicy: func(_ *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
			return nil, errors.New("Throttling")
		},
	})
	_, err := c.GetPolicyDocument(context.Background(), "arn:aws:iam::aws:policy/X")
	if err == nil || !errorContains(err, "Throttling") {
		t.Errorf("err = %v, want wrap of Throttling", err)
	}
}

// decodePolicyDocument: invalid URL encoding returns error.
func TestDecodePolicyDocument_BadEscape(t *testing.T) {
	_, err := decodePolicyDocument("%ZZ-not-valid-percent")
	if err == nil {
		t.Fatal("want error for malformed URL escape")
	}
}

// decodePolicyDocument: empty document is an error (defensive).
func TestDecodePolicyDocument_Empty(t *testing.T) {
	_, err := decodePolicyDocument("")
	if err == nil {
		t.Fatal("want error for empty document")
	}
}

// errorContains reports whether err is non-nil and its message
// contains sub. Tiny helper kept local to client_test.go.
func errorContains(err error, sub string) bool {
	return err != nil && strings.Contains(err.Error(), sub)
}
