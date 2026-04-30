// Package credentials provides the Provider abstraction that supplies AWS
// credentials and identity for a request.
//
// Every operation that touches AWS or Kubernetes takes a Provider as an
// explicit argument. No global SDK config; no credentials in context.Context.
//
// v1 (SharedIrsaProvider): AWS credentials come from the pod's IRSA role
// (or the local AWS profile when running on a laptop); Actor() comes from
// the logged-in user's Okta session.
//
// v2 will add UserSsoProvider where both AWS credentials and Actor()
// derive from the user's AWS Identity Center session. The Provider
// interface stays stable; only the implementation swaps.
package credentials

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// Provider represents the credentials and identity for a single request.
type Provider interface {
	aws.CredentialsProvider
	Actor() string
}

// Factory builds a Provider for an authenticated session.
type Factory interface {
	For(ctx context.Context, session Session) (Provider, error)
}

// Session is the authenticated context extracted from a request.
type Session struct {
	// Subject is the OIDC sub claim from the Okta ID token.
	Subject string
}
