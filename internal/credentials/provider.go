// Package credentials provides the Provider abstraction that supplies AWS
// credentials and identity for a request.
//
// Every operation that touches AWS or Kubernetes takes a Provider as an
// explicit argument. No global SDK config; no credentials in context.Context.
//
// v1 (SharedIrsaProvider): AWS credentials come from the pod's IRSA role
// (or the local AWS profile when running on a laptop); Actor() comes from
// the logged-in user's OIDC session. K8s impersonation strings come from
// the authz mode resolver and ride the Provider so handlers don't need to
// know which mode is active.
//
// v2 will add UserSsoProvider where both AWS credentials and Actor()
// derive from the user's AWS Identity Center session. The Provider
// interface stays stable; only the implementation swaps.
package credentials

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// ImpersonationConfig is the K8s impersonation slice carried on the
// Provider. Mirrors authz.ImpersonationConfig but lives here so the
// k8s package can read it without importing authz (which would create
// a layering inversion).
type ImpersonationConfig struct {
	UserName string
	Groups   []string
}

// IsZero reports whether impersonation is unset (shared mode).
func (c ImpersonationConfig) IsZero() bool {
	return c.UserName == "" && len(c.Groups) == 0
}

// Provider represents the credentials and identity for a single request.
type Provider interface {
	aws.CredentialsProvider
	Actor() string
	// Impersonation returns the K8s impersonation strings the K8s client
	// should set on rest.Config. Zero value (== shared mode) means
	// "do not impersonate."
	Impersonation() ImpersonationConfig
}

// Factory builds a Provider for an authenticated session.
type Factory interface {
	For(ctx context.Context, session Session) (Provider, error)
}

// Session is the authenticated context extracted from a request.
//
// Tokens are intentionally absent — they live behind the auth package
// and never reach Providers or downstream operations. This struct is
// the *identity* slice the rest of the app consumes.
type Session struct {
	// Subject is the OIDC sub claim from the IdP ID token, or
	// "dev@local" in dev mode, or "anonymous" when no auth context
	// is present (audit lines never read empty).
	Subject string

	// Email is the user's primary email from the ID token. Empty in
	// dev mode unless the operator overrode it in auth.yaml.
	Email string

	// Groups is the configured groups claim from the IdP. Empty in dev
	// mode unless the operator overrode it.
	Groups []string
}
