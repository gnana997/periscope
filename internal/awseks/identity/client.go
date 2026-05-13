package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// EKSAPI is the subset of the AWS EKS client used by this package.
// Defined as an interface so handler tests can substitute a stub.
type EKSAPI interface {
	ListAccessEntries(ctx context.Context, in *eks.ListAccessEntriesInput, opts ...func(*eks.Options)) (*eks.ListAccessEntriesOutput, error)
	DescribeAccessEntry(ctx context.Context, in *eks.DescribeAccessEntryInput, opts ...func(*eks.Options)) (*eks.DescribeAccessEntryOutput, error)
	ListAssociatedAccessPolicies(ctx context.Context, in *eks.ListAssociatedAccessPoliciesInput, opts ...func(*eks.Options)) (*eks.ListAssociatedAccessPoliciesOutput, error)
	ListPodIdentityAssociations(ctx context.Context, in *eks.ListPodIdentityAssociationsInput, opts ...func(*eks.Options)) (*eks.ListPodIdentityAssociationsOutput, error)
	DescribePodIdentityAssociation(ctx context.Context, in *eks.DescribePodIdentityAssociationInput, opts ...func(*eks.Options)) (*eks.DescribePodIdentityAssociationOutput, error)
}

// IAMAPI is the subset of the AWS IAM client used by this package.
type IAMAPI interface {
	GetRole(ctx context.Context, in *iam.GetRoleInput, opts ...func(*iam.Options)) (*iam.GetRoleOutput, error)
}

// Client wraps the EKS + IAM SDK calls used by the Identity surface.
// Construct with New (real SDK) or NewWithAPIs (tests).
type Client struct {
	eks    EKSAPI
	iam    IAMAPI
	region string
}

// New builds a real Client backed by AWS SDK v2 clients sharing the
// provided aws.Config (which carries the per-cluster credentials.Provider
// and Region — see cmd/periscope wiring for the construction site).
func New(cfg aws.Config) *Client {
	return &Client{
		eks:    eks.NewFromConfig(cfg),
		iam:    iam.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// NewWithAPIs is the test seam — handler tests inject stubs for both
// APIs without touching the SDK construction path.
func NewWithAPIs(eksAPI EKSAPI, iamAPI IAMAPI) *Client {
	return &Client{eks: eksAPI, iam: iamAPI}
}

// IAMRoleResolver is the narrow interface the Manager depends on for
// role-existence probes. Implemented by *Client and stubbable for
// tests that exercise the Manager without IAM at all.
type IAMRoleResolver interface {
	RoleExists(ctx context.Context, roleArn string) (bool, error)
}

// ── EKS access entries ────────────────────────────────────────────

// ListAccessEntries paginates eks:ListAccessEntries and returns the
// flat list of principal ARNs registered on the cluster. The caller
// is expected to fan out DescribeAccessEntry + ListAssociatedAccessPolicies
// for each one — pattern matches eksAddonsListHandler's parallel
// describe step.
func (c *Client) ListAccessEntries(ctx context.Context, clusterName string) ([]string, error) {
	var out []string
	var nextToken *string
	for {
		resp, err := c.eks.ListAccessEntries(ctx, &eks.ListAccessEntriesInput{
			ClusterName: aws.String(clusterName),
			NextToken:   nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("eks:ListAccessEntries: %w", err)
		}
		out = append(out, resp.AccessEntries...)
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}
	return out, nil
}

// DescribeAccessEntry returns a single access entry. AccessPolicies is
// fetched separately via ListAssociatedAccessPolicies (callers compose).
func (c *Client) DescribeAccessEntry(ctx context.Context, clusterName, principalArn string) (AccessEntry, error) {
	resp, err := c.eks.DescribeAccessEntry(ctx, &eks.DescribeAccessEntryInput{
		ClusterName:  aws.String(clusterName),
		PrincipalArn: aws.String(principalArn),
	})
	if err != nil {
		return AccessEntry{}, fmt.Errorf("eks:DescribeAccessEntry: %w", err)
	}
	if resp.AccessEntry == nil {
		return AccessEntry{}, fmt.Errorf("eks:DescribeAccessEntry returned nil entry for %s", principalArn)
	}
	return accessEntryFromSDK(*resp.AccessEntry), nil
}

// ListAssociatedAccessPolicies paginates the per-principal policy
// associations.
func (c *Client) ListAssociatedAccessPolicies(ctx context.Context, clusterName, principalArn string) ([]AccessPolicyAssoc, error) {
	var out []AccessPolicyAssoc
	var nextToken *string
	for {
		resp, err := c.eks.ListAssociatedAccessPolicies(ctx, &eks.ListAssociatedAccessPoliciesInput{
			ClusterName:  aws.String(clusterName),
			PrincipalArn: aws.String(principalArn),
			NextToken:    nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("eks:ListAssociatedAccessPolicies: %w", err)
		}
		for _, a := range resp.AssociatedAccessPolicies {
			out = append(out, accessPolicyAssocFromSDK(a))
		}
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}
	return out, nil
}

// ── EKS Pod Identity associations ────────────────────────────────

// ListPodIdentityAssociations paginates the cluster's associations.
// Each summary returned by the list call lacks the role ARN, so we
// fan out DescribePodIdentityAssociation per association inside this
// method to keep the wire response self-contained.
func (c *Client) ListPodIdentityAssociations(ctx context.Context, clusterName string) ([]PodIdentityAssoc, error) {
	var summaries []ekstypes.PodIdentityAssociationSummary
	var nextToken *string
	for {
		resp, err := c.eks.ListPodIdentityAssociations(ctx, &eks.ListPodIdentityAssociationsInput{
			ClusterName: aws.String(clusterName),
			NextToken:   nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("eks:ListPodIdentityAssociations: %w", err)
		}
		summaries = append(summaries, resp.Associations...)
		if resp.NextToken == nil || *resp.NextToken == "" {
			break
		}
		nextToken = resp.NextToken
	}

	out := make([]PodIdentityAssoc, 0, len(summaries))
	for _, s := range summaries {
		assocID := aws.ToString(s.AssociationId)
		full, err := c.eks.DescribePodIdentityAssociation(ctx, &eks.DescribePodIdentityAssociationInput{
			ClusterName:   aws.String(clusterName),
			AssociationId: aws.String(assocID),
		})
		if err != nil {
			return nil, fmt.Errorf("eks:DescribePodIdentityAssociation %s: %w", assocID, err)
		}
		if full.Association == nil {
			continue
		}
		out = append(out, podIdentityAssocFromSDK(*full.Association))
	}
	return out, nil
}

// ── IAM role existence ───────────────────────────────────────────

// RoleExists probes iam:GetRole for the role named in roleArn.
// Returns (false, nil) when the role definitively doesn't exist
// (NoSuchEntityException) so callers can distinguish "deleted" from
// "we couldn't tell" — the second case is (false, err).
//
// Returns (false, nil) for any roleArn whose name can't be parsed
// — the caller renders these as "role not found" with no IAM call.
// Cross-account ARNs that the periscope role can't read return
// (false, err) and the caller renders the ARN with a "permission
// denied" caption.
func (c *Client) RoleExists(ctx context.Context, roleArn string) (bool, error) {
	name, ok := roleNameFromArn(roleArn)
	if !ok {
		return false, nil
	}
	_, err := c.iam.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(name)})
	if err == nil {
		return true, nil
	}
	var nsee *iamtypes.NoSuchEntityException
	if errors.As(err, &nsee) {
		return false, nil
	}
	return false, fmt.Errorf("iam:GetRole %s: %w", name, err)
}

// roleNameFromArn extracts the role name (last "/"-separated segment)
// from an IAM role ARN. Returns ok=false for ARNs that don't look
// like IAM role ARNs at all.
func roleNameFromArn(arn string) (string, bool) {
	const prefix = ":role/"
	i := strings.Index(arn, prefix)
	if i < 0 {
		return "", false
	}
	tail := arn[i+len(prefix):]
	if tail == "" {
		return "", false
	}
	// Roles can have paths (arn:aws:iam::A:role/path/to/Name).
	// GetRole takes the final segment.
	if slash := strings.LastIndex(tail, "/"); slash >= 0 {
		tail = tail[slash+1:]
	}
	if tail == "" {
		return "", false
	}
	return tail, true
}

// ── SDK type → wire type adapters ───────────────────────────────

func accessEntryFromSDK(a ekstypes.AccessEntry) AccessEntry {
	return AccessEntry{
		PrincipalArn:     aws.ToString(a.PrincipalArn),
		Type:             aws.ToString(a.Type),
		KubernetesGroups: a.KubernetesGroups,
		ModifiedAt:       a.ModifiedAt,
	}
}

func accessPolicyAssocFromSDK(a ekstypes.AssociatedAccessPolicy) AccessPolicyAssoc {
	out := AccessPolicyAssoc{
		PolicyArn:  aws.ToString(a.PolicyArn),
		ModifiedAt: a.ModifiedAt,
	}
	if a.AccessScope != nil {
		out.AccessScope = string(a.AccessScope.Type)
		out.Namespaces = a.AccessScope.Namespaces
	}
	return out
}

func podIdentityAssocFromSDK(a ekstypes.PodIdentityAssociation) PodIdentityAssoc {
	return PodIdentityAssoc{
		AssociationId:  aws.ToString(a.AssociationId),
		RoleArn:        aws.ToString(a.RoleArn),
		Namespace:      aws.ToString(a.Namespace),
		ServiceAccount: aws.ToString(a.ServiceAccount),
		ClusterName:    aws.ToString(a.ClusterName),
	}
}
