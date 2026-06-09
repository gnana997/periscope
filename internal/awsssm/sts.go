package awsssm

import (
	"context"
	"fmt"
	"regexp"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// STSAPI is the subset of the STS client awsssm uses. An interface so
// tests can stub the web-identity exchange without real AWS.
type STSAPI interface {
	AssumeRoleWithWebIdentity(ctx context.Context, in *sts.AssumeRoleWithWebIdentityInput, opts ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error)
}

// AssumedIdentity describes who a session was opened as — surfaced into
// the audit row so Periscope's log cross-references the CloudTrail entry
// AWS records under the same assumed-role session.
type AssumedIdentity struct {
	RoleSessionName string
	AssumedRoleARN  string
}

// AssumeWebIdentity exchanges a user's OIDC id_token for short-lived,
// per-user AWS credentials via sts:AssumeRoleWithWebIdentity. The IAM
// trust policy on roleARN is the access gate; a token whose claims don't
// satisfy it is rejected here, before any session is created.
//
// This call needs no AWS credentials of its own — the web-identity token
// IS the credential — so cfg only supplies the region.
func AssumeWebIdentity(ctx context.Context, cfg aws.Config, roleARN, idToken, sessionName string) (aws.CredentialsProvider, AssumedIdentity, error) {
	return assumeWebIdentity(ctx, sts.NewFromConfig(cfg), roleARN, idToken, sessionName)
}

func assumeWebIdentity(ctx context.Context, api STSAPI, roleARN, idToken, sessionName string) (aws.CredentialsProvider, AssumedIdentity, error) {
	name := SanitizeSessionName(sessionName)
	out, err := api.AssumeRoleWithWebIdentity(ctx, &sts.AssumeRoleWithWebIdentityInput{
		RoleArn:          &roleARN,
		RoleSessionName:  &name,
		WebIdentityToken: &idToken,
	})
	if err != nil {
		return nil, AssumedIdentity{}, fmt.Errorf("assume role with web identity: %w", err)
	}
	if out.Credentials == nil {
		return nil, AssumedIdentity{}, fmt.Errorf("assume role with web identity: empty credentials")
	}
	c := out.Credentials
	provider := credentials.NewStaticCredentialsProvider(
		aws.ToString(c.AccessKeyId), aws.ToString(c.SecretAccessKey), aws.ToString(c.SessionToken))

	id := AssumedIdentity{RoleSessionName: name}
	if out.AssumedRoleUser != nil {
		id.AssumedRoleARN = aws.ToString(out.AssumedRoleUser.Arn)
	}
	return provider, id, nil
}

// sessionNameInvalid matches anything outside the STS roleSessionName
// charset [\w+=,.@-].
var sessionNameInvalid = regexp.MustCompile(`[^\w+=,.@-]`)

// SanitizeSessionName coerces a raw identifier (typically the OIDC sub,
// e.g. "auth0|abc") into a valid STS roleSessionName: disallowed
// characters become '-', the result is truncated to STS's 64-char limit,
// and an empty result falls back to a constant. The sanitized name is
// what appears in CloudTrail and the SSM session id.
func SanitizeSessionName(s string) string {
	s = sessionNameInvalid.ReplaceAllString(s, "-")
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" {
		return "periscope-node-shell"
	}
	return s
}
