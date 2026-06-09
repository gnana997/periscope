package awsssm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)

type stubSTS struct {
	out *sts.AssumeRoleWithWebIdentityOutput
	err error
	got *sts.AssumeRoleWithWebIdentityInput
}

func (s *stubSTS) AssumeRoleWithWebIdentity(_ context.Context, in *sts.AssumeRoleWithWebIdentityInput, _ ...func(*sts.Options)) (*sts.AssumeRoleWithWebIdentityOutput, error) {
	s.got = in
	return s.out, s.err
}

func TestSanitizeSessionName(t *testing.T) {
	cases := map[string]string{
		"auth0|abc123":      "auth0-abc123",
		"alice@corp.com":    "alice@corp.com", // @ . - are allowed
		"":                  "periscope-node-shell",
		"plain":             "plain",
		strings.Repeat("x", 80): strings.Repeat("x", 64),
	}
	for in, want := range cases {
		if got := SanitizeSessionName(in); got != want {
			t.Errorf("SanitizeSessionName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAssumeWebIdentity_Success(t *testing.T) {
	stub := &stubSTS{out: &sts.AssumeRoleWithWebIdentityOutput{
		Credentials: &ststypes.Credentials{
			AccessKeyId:     aws.String("AKIAEXAMPLE"),
			SecretAccessKey: aws.String("secret"),
			SessionToken:    aws.String("token"),
		},
		AssumedRoleUser: &ststypes.AssumedRoleUser{
			Arn: aws.String("arn:aws:sts::123:assumed-role/node-shell/periscope-poc-auth0-abc"),
		},
	}}

	provider, id, err := assumeWebIdentity(context.Background(), stub, "arn:aws:iam::123:role/node-shell", "the.id.token", "auth0|abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if aws.ToString(stub.got.RoleSessionName) != "auth0-abc" {
		t.Errorf("session name not sanitized: %q", aws.ToString(stub.got.RoleSessionName))
	}
	if aws.ToString(stub.got.WebIdentityToken) != "the.id.token" {
		t.Errorf("id_token not forwarded")
	}
	if id.AssumedRoleARN != "arn:aws:sts::123:assumed-role/node-shell/periscope-poc-auth0-abc" {
		t.Errorf("assumed role arn = %q", id.AssumedRoleARN)
	}
	creds, err := provider.Retrieve(context.Background())
	if err != nil || creds.AccessKeyID != "AKIAEXAMPLE" || creds.SessionToken != "token" {
		t.Fatalf("creds = %+v err=%v", creds, err)
	}
}

func TestAssumeWebIdentity_Rejected(t *testing.T) {
	stub := &stubSTS{err: errors.New("AccessDenied")}
	if _, _, err := assumeWebIdentity(context.Background(), stub, "role", "tok", "sub"); err == nil {
		t.Fatal("expected error when STS rejects the token")
	}
}
