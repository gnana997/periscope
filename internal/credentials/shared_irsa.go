package credentials

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

// SharedIrsaProvider is the v1 Provider implementation. AWS credentials
// come from the dashboard pod's IRSA role (or the local AWS profile when
// running outside a pod); Actor identifies the logged-in user from the
// Okta session.
type SharedIrsaProvider struct {
	awsCreds aws.CredentialsProvider
	actor    string
}

func (p *SharedIrsaProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	return p.awsCreds.Retrieve(ctx)
}

func (p *SharedIrsaProvider) Actor() string {
	return p.actor
}

// SharedIrsaFactory loads the AWS default config once at startup and
// builds SharedIrsaProvider per request. The AWS principal is shared
// across all requests; only Actor varies.
type SharedIrsaFactory struct {
	awsCfg aws.Config
}

// NewSharedIrsaFactory loads the AWS default config — IRSA in a pod,
// AWS_PROFILE / ~/.aws/config locally — and returns a factory ready to
// build per-request providers.
func NewSharedIrsaFactory(ctx context.Context) (*SharedIrsaFactory, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &SharedIrsaFactory{awsCfg: cfg}, nil
}

func (f *SharedIrsaFactory) For(_ context.Context, session Session) (Provider, error) {
	return &SharedIrsaProvider{
		awsCreds: f.awsCfg.Credentials,
		actor:    session.Subject,
	}, nil
}
