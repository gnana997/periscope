package k8s

import (
	"context"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/gnana997/periscope/internal/clusters"
)

// stubProvider is a credentials.Provider with hardcoded credentials,
// used by tests in this package.
type stubProvider struct{}

func (stubProvider) Retrieve(_ context.Context) (aws.Credentials, error) {
	return aws.Credentials{
		AccessKeyID:     "AKIATEST",
		SecretAccessKey: "secretsecret",
		SessionToken:    "session",
		Source:          "stub",
	}, nil
}

func (stubProvider) Actor() string { return "test@example.com" }

func TestMintEKSToken_format(t *testing.T) {
	c := clusters.Cluster{
		Name:   "demo",
		ARN:    "arn:aws:eks:us-east-1:123456789012:cluster/demo-eks",
		Region: "us-east-1",
	}

	tok, err := MintEKSToken(context.Background(), stubProvider{}, c)
	if err != nil {
		t.Fatalf("MintEKSToken: %v", err)
	}

	if !strings.HasPrefix(tok, tokenPrefix) {
		t.Fatalf("token missing prefix %q: %q", tokenPrefix, tok)
	}

	body := strings.TrimPrefix(tok, tokenPrefix)
	decoded, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("decode token body: %v", err)
	}

	u, err := url.Parse(string(decoded))
	if err != nil {
		t.Fatalf("parse decoded url: %v", err)
	}

	if got, want := u.Host, "sts.us-east-1.amazonaws.com"; got != want {
		t.Errorf("token host = %q, want %q", got, want)
	}

	q := u.Query()
	if got := q.Get("Action"); got != "GetCallerIdentity" {
		t.Errorf("Action = %q, want GetCallerIdentity", got)
	}
	if q.Get("X-Amz-Algorithm") == "" {
		t.Error("X-Amz-Algorithm missing — request was not signed")
	}
	if q.Get("X-Amz-Signature") == "" {
		t.Error("X-Amz-Signature missing")
	}
	if signed := q.Get("X-Amz-SignedHeaders"); !strings.Contains(signed, "x-k8s-aws-id") {
		t.Errorf("X-Amz-SignedHeaders = %q, must include x-k8s-aws-id", signed)
	}
}
