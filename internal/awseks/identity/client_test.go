package identity

import (
	"context"
	"errors"
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

// stubIAM lets us drive RoleExists' decision tree without touching AWS.
type stubIAM struct {
	resp *iam.GetRoleOutput
	err  error
}

func (s *stubIAM) GetRole(ctx context.Context, in *iam.GetRoleInput, opts ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	return s.resp, s.err
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
