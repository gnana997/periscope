package k8s

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

const (
	// emptyPayloadSHA256 is the SHA256 of the empty string. STS
	// GetCallerIdentity is a GET with no body.
	emptyPayloadSHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	// tokenPrefix is the EKS bearer token prefix the auth webhook expects.
	tokenPrefix = "k8s-aws-v1."

	// tokenLifetime is the X-Amz-Expires window. EKS validates the
	// presigned URL within this window.
	tokenLifetime = 60 * time.Second

	// clusterIDHeader binds the presigned STS URL to a specific cluster.
	// Must be set on the request before signing so SigV4 includes it.
	clusterIDHeader = "x-k8s-aws-id"
)

// MintEKSToken returns an EKS bearer token derived from the Provider's
// AWS credentials and the target cluster. The token is a presigned
// sts:GetCallerIdentity URL, base64-url-encoded with no padding, and
// prefixed with "k8s-aws-v1.".
func MintEKSToken(ctx context.Context, p credentials.Provider, c clusters.Cluster) (string, error) {
	creds, err := p.Retrieve(ctx)
	if err != nil {
		return "", fmt.Errorf("retrieve aws credentials: %w", err)
	}

	stsURL := fmt.Sprintf(
		"https://sts.%s.amazonaws.com/?Action=GetCallerIdentity&Version=2011-06-15&X-Amz-Expires=%d",
		c.Region, int(tokenLifetime.Seconds()),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stsURL, nil)
	if err != nil {
		return "", fmt.Errorf("build sts request: %w", err)
	}
	req.Header.Set(clusterIDHeader, c.EKSName())

	signedURL, _, err := v4.NewSigner().PresignHTTP(
		ctx, creds, req, emptyPayloadSHA256, "sts", c.Region, time.Now().UTC(),
	)
	if err != nil {
		return "", fmt.Errorf("sign sts presigned url: %w", err)
	}

	return tokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(signedURL)), nil
}
