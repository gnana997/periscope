package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	iamengine "github.com/gnana997/periscope/internal/awseks/iam"
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
// Stubbable for tests.
type IAMAPI interface {
	GetRole(ctx context.Context, in *iam.GetRoleInput, opts ...func(*iam.Options)) (*iam.GetRoleOutput, error)

	// Policy-fetch surface — added in #187 so *Client satisfies
	// iam.PolicyFetcher. Each call is paginated by AWS via Marker;
	// the high-level methods on *Client below loop until truncation
	// is exhausted.
	ListRolePolicies(ctx context.Context, in *iam.ListRolePoliciesInput, opts ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error)
	GetRolePolicy(ctx context.Context, in *iam.GetRolePolicyInput, opts ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error)
	ListAttachedRolePolicies(ctx context.Context, in *iam.ListAttachedRolePoliciesInput, opts ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error)
	GetPolicy(ctx context.Context, in *iam.GetPolicyInput, opts ...func(*iam.Options)) (*iam.GetPolicyOutput, error)
	GetPolicyVersion(ctx context.Context, in *iam.GetPolicyVersionInput, opts ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error)

	// Capabilities-probe surface (#188): SimulatePrincipalPolicy
	// is called once per /identity/capabilities cold call to
	// populate the MISSING_IAM_PERMS lock with the exact missing
	// action list. Optional — when periscope-server's role lacks
	// iam:SimulatePrincipalPolicy, the capabilities response stays
	// optimistically Available=true with a Note.
	SimulatePrincipalPolicy(ctx context.Context, in *iam.SimulatePrincipalPolicyInput, opts ...func(*iam.Options)) (*iam.SimulatePrincipalPolicyOutput, error)
}

// STSAPI is the subset of the AWS STS client used by this package.
// Today only sts:GetCallerIdentity is needed — to resolve the
// principal ARN that drives the iam:SimulatePrincipalPolicy probe.
// Defined as an interface so handler tests can substitute a stub.
type STSAPI interface {
	GetCallerIdentity(ctx context.Context, in *sts.GetCallerIdentityInput, opts ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
}

// Client wraps the EKS + IAM + STS SDK calls used by the Identity
// and AWS Access surfaces. Construct with New (real SDK) or
// NewWithAPIs (tests).
type Client struct {
	eks    EKSAPI
	iam    IAMAPI
	sts    STSAPI
	region string
}

// New builds a real Client backed by AWS SDK v2 clients sharing the
// provided aws.Config (which carries the per-cluster credentials.Provider
// and Region — see cmd/periscope wiring for the construction site).
func New(cfg aws.Config) *Client {
	return &Client{
		eks:    eks.NewFromConfig(cfg),
		iam:    iam.NewFromConfig(cfg),
		sts:    sts.NewFromConfig(cfg),
		region: cfg.Region,
	}
}

// NewWithAPIs is the test seam — handler tests inject stubs for the
// EKS + IAM APIs without touching the SDK construction path. STS
// is omitted; callers that need it use NewWithAllAPIs.
func NewWithAPIs(eksAPI EKSAPI, iamAPI IAMAPI) *Client {
	return &Client{eks: eksAPI, iam: iamAPI}
}

// NewWithAllAPIs is the test seam for surfaces that also need STS
// (the AWS Access capabilities probe). When STS is nil, the
// capabilities probe falls back to its optimistic mode.
func NewWithAllAPIs(eksAPI EKSAPI, iamAPI IAMAPI, stsAPI STSAPI) *Client {
	return &Client{eks: eksAPI, iam: iamAPI, sts: stsAPI}
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

// ── PolicyFetcher implementation (#187) ──────────────────────────
//
// *Client satisfies iam.PolicyFetcher. The compile-time assertion
// in interface_assert.go enforces this — if any signature drifts,
// the package won't build.

// ListRolePolicies returns the names of every inline policy
// attached to the role. Paginates via Marker until truncation is
// exhausted. roleArn is extracted to the role name via
// roleNameFromArn; unparseable ARNs return a typed error.
func (c *Client) ListRolePolicies(ctx context.Context, roleArn string) ([]string, error) {
	name, ok := roleNameFromArn(roleArn)
	if !ok {
		return nil, fmt.Errorf("iam:ListRolePolicies: unparseable role ARN %q", roleArn)
	}
	var out []string
	var marker *string
	for {
		resp, err := c.iam.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{
			RoleName: aws.String(name),
			Marker:   marker,
		})
		if err != nil {
			return nil, fmt.Errorf("iam:ListRolePolicies %s: %w", name, err)
		}
		out = append(out, resp.PolicyNames...)
		if !resp.IsTruncated || resp.Marker == nil {
			break
		}
		marker = resp.Marker
	}
	return out, nil
}

// GetRolePolicy fetches the inline policy document attached to the
// role under policyName. AWS returns the policy URL-encoded inside
// a JSON string; this method URL-decodes before returning.
func (c *Client) GetRolePolicy(ctx context.Context, roleArn, policyName string) (json.RawMessage, error) {
	name, ok := roleNameFromArn(roleArn)
	if !ok {
		return nil, fmt.Errorf("iam:GetRolePolicy: unparseable role ARN %q", roleArn)
	}
	resp, err := c.iam.GetRolePolicy(ctx, &iam.GetRolePolicyInput{
		RoleName:   aws.String(name),
		PolicyName: aws.String(policyName),
	})
	if err != nil {
		return nil, fmt.Errorf("iam:GetRolePolicy %s/%s: %w", name, policyName, err)
	}
	if resp.PolicyDocument == nil {
		return nil, fmt.Errorf("iam:GetRolePolicy %s/%s: nil PolicyDocument", name, policyName)
	}
	return decodePolicyDocument(aws.ToString(resp.PolicyDocument))
}

// ListAttachedRolePolicies returns the managed policies attached to
// the role. Paginates via Marker. Returns iam-package AttachedPolicy
// values directly so *Client satisfies iam.PolicyFetcher without an
// adapter at the call site.
func (c *Client) ListAttachedRolePolicies(ctx context.Context, roleArn string) ([]iamengine.AttachedPolicy, error) {
	name, ok := roleNameFromArn(roleArn)
	if !ok {
		return nil, fmt.Errorf("iam:ListAttachedRolePolicies: unparseable role ARN %q", roleArn)
	}
	var out []iamengine.AttachedPolicy
	var marker *string
	for {
		resp, err := c.iam.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
			RoleName: aws.String(name),
			Marker:   marker,
		})
		if err != nil {
			return nil, fmt.Errorf("iam:ListAttachedRolePolicies %s: %w", name, err)
		}
		for _, p := range resp.AttachedPolicies {
			out = append(out, iamengine.AttachedPolicy{
				PolicyArn:  aws.ToString(p.PolicyArn),
				PolicyName: aws.ToString(p.PolicyName),
			})
		}
		if !resp.IsTruncated || resp.Marker == nil {
			break
		}
		marker = resp.Marker
	}
	return out, nil
}

// GetPolicyDocument resolves a managed-policy ARN to its current
// (DefaultVersionId) document. Two-step under the hood: GetPolicy
// to read DefaultVersionId, then GetPolicyVersion for the actual
// document. Caller receives URL-decoded JSON as a single
// RawMessage.
//
// Two AWS API calls per managed policy is documented and unavoidable;
// the engine's per-role TTL cache amortizes them.
func (c *Client) GetPolicyDocument(ctx context.Context, policyArn string) (json.RawMessage, error) {
	pol, err := c.iam.GetPolicy(ctx, &iam.GetPolicyInput{
		PolicyArn: aws.String(policyArn),
	})
	if err != nil {
		return nil, fmt.Errorf("iam:GetPolicy %s: %w", policyArn, err)
	}
	if pol.Policy == nil || pol.Policy.DefaultVersionId == nil {
		return nil, fmt.Errorf("iam:GetPolicy %s: missing DefaultVersionId", policyArn)
	}
	versionID := aws.ToString(pol.Policy.DefaultVersionId)

	ver, err := c.iam.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{
		PolicyArn: aws.String(policyArn),
		VersionId: aws.String(versionID),
	})
	if err != nil {
		return nil, fmt.Errorf("iam:GetPolicyVersion %s/%s: %w", policyArn, versionID, err)
	}
	if ver.PolicyVersion == nil || ver.PolicyVersion.Document == nil {
		return nil, fmt.Errorf("iam:GetPolicyVersion %s/%s: nil Document", policyArn, versionID)
	}
	return decodePolicyDocument(aws.ToString(ver.PolicyVersion.Document))
}

// decodePolicyDocument URL-decodes the policy-document string AWS
// returns from GetRolePolicy and GetPolicyVersion. The JSON is
// wrapped in URL-encoding for AWS API compatibility (HTTP form-
// data safety); the engine downstream expects raw JSON bytes.
func decodePolicyDocument(encoded string) (json.RawMessage, error) {
	if encoded == "" {
		return nil, fmt.Errorf("empty policy document")
	}
	decoded, err := url.QueryUnescape(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode policy document: %w", err)
	}
	return json.RawMessage(decoded), nil
}

// ── STS + SimulatePrincipalPolicy (#188 capabilities probe) ──────

// ErrSTSNotWired is returned when CallerIdentity is called on a
// Client built without an STS API (NewWithAPIs without STS, or a
// production Client with sts disabled). The capabilities handler
// maps to optimistic Available=true with a Note.
var ErrSTSNotWired = errors.New("identity: STS API not wired")

// CallerIdentity returns periscope-server's own principal ARN in
// IAM role form (collapsing the sts:assumed-role session form to
// the underlying iam:role/Name form, which is the shape
// iam:SimulatePrincipalPolicy expects as PolicySourceArn).
//
// AWS grants sts:GetCallerIdentity to every authenticated principal
// by default, so this call almost never 403s in practice — but
// callers should handle the error path defensively (the probe is
// optional).
func (c *Client) CallerIdentity(ctx context.Context) (string, error) {
	if c.sts == nil {
		return "", ErrSTSNotWired
	}
	out, err := c.sts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("sts:GetCallerIdentity: %w", err)
	}
	return CollapseSessionToRoleArn(aws.ToString(out.Arn)), nil
}

// CollapseSessionToRoleArn converts an assumed-role session ARN
// (arn:aws:sts::ACCT:assumed-role/RoleName/SessionName) to the
// underlying IAM role ARN (arn:aws:iam::ACCT:role/RoleName).
// Returns input unchanged for non-session ARNs (already a role
// ARN, a user ARN, or unparseable).
//
// Exported so cmd/periscope can use the same canonicalization when
// resolving the principal ARN for a configured override.
func CollapseSessionToRoleArn(arn string) string {
	if !strings.Contains(arn, ":sts::") || !strings.Contains(arn, ":assumed-role/") {
		return arn
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 {
		return arn
	}
	tail := strings.TrimPrefix(parts[5], "assumed-role/")
	role := tail
	if i := strings.Index(tail, "/"); i > 0 {
		role = tail[:i]
	}
	if role == "" {
		return arn
	}
	return fmt.Sprintf("arn:%s:iam::%s:role/%s", parts[1], parts[4], role)
}

// SimulateActions calls iam:SimulatePrincipalPolicy and returns the
// subset of `actions` that the principal cannot perform. resource
// is optional; pass "*" (or "") to skip resource-level evaluation
// and surface action-only permission state.
//
// Used by the /identity/capabilities probe to populate the exact
// Missing[] list on a MISSING_IAM_PERMS lock. Empty `actions`
// returns (nil, nil) without an SDK call.
func (c *Client) SimulateActions(ctx context.Context, principalArn string, actions []string, resource string) ([]string, error) {
	if len(actions) == 0 {
		return nil, nil
	}
	in := &iam.SimulatePrincipalPolicyInput{
		PolicySourceArn: aws.String(principalArn),
		ActionNames:     actions,
	}
	if resource != "" && resource != "*" {
		in.ResourceArns = []string{resource}
	}
	out, err := c.iam.SimulatePrincipalPolicy(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("iam:SimulatePrincipalPolicy: %w", err)
	}
	var missing []string
	for _, r := range out.EvaluationResults {
		if r.EvalDecision != iamtypes.PolicyEvaluationDecisionTypeAllowed {
			missing = append(missing, aws.ToString(r.EvalActionName))
		}
	}
	return missing, nil
}
