package identity

import (
	iamengine "github.com/gnana997/periscope/internal/awseks/iam"
)

// Compile-time assertions that *Client satisfies the engine seams
// from the iam package (#187). If any signature drifts on either
// side — IAMRoleResolver in this package, PolicyFetcher in iam —
// the package will fail to build, surfacing the drift in CI.

var (
	_ IAMRoleResolver       = (*Client)(nil)
	_ iamengine.PolicyFetcher = (*Client)(nil)
)
