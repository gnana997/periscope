package credentials

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	"github.com/gnana997/periscope/internal/authz"
)

// SharedIrsaProvider is the v1 Provider implementation. AWS credentials
// come from the dashboard pod's IRSA / Pod Identity role (or the local
// AWS profile when running outside a pod); Actor identifies the
// logged-in user from the IdP session; Impersonation carries the K8s
// impersonation strings the authz mode resolver derived for this user.
type SharedIrsaProvider struct {
	awsCreds aws.CredentialsProvider
	actor    string
	imperson ImpersonationConfig
}

func (p *SharedIrsaProvider) Retrieve(ctx context.Context) (aws.Credentials, error) {
	return p.awsCreds.Retrieve(ctx)
}

func (p *SharedIrsaProvider) Actor() string { return p.actor }

func (p *SharedIrsaProvider) Impersonation() ImpersonationConfig { return p.imperson }

// SharedIrsaFactory loads the AWS default config once at startup and
// builds SharedIrsaProvider per request. The AWS principal is shared
// across all requests; the Actor and Impersonation slice vary per
// session.
type SharedIrsaFactory struct {
	awsCfg   aws.Config
	resolver *authz.Resolver
}

// NewSharedIrsaFactory loads the AWS default config — IRSA / Pod
// Identity in a pod, AWS_PROFILE / ~/.aws/config locally — and returns
// a factory ready to build per-request providers. The authz Resolver
// determines the K8s impersonation strings for each request based on
// the configured mode (shared / tier / raw).
// NewSharedIrsaFactoryFromConfig wraps an already-loaded aws.Config.
// Useful when startup needs to share the same default-chain creds
// across the factory and other consumers (e.g. the secrets resolver)
// before the authz resolver is built.
func NewSharedIrsaFactoryFromConfig(awsCfg aws.Config, resolver *authz.Resolver) *SharedIrsaFactory {
	return &SharedIrsaFactory{awsCfg: awsCfg, resolver: resolver}
}

// AttachResolver swaps in an authz Resolver after construction.
// Used when the resolver is built from an auth.yaml that itself
// references AWS-backed secrets — chicken-and-egg avoided by
// constructing the factory early and resolving later.
func (f *SharedIrsaFactory) AttachResolver(r *authz.Resolver) { f.resolver = r }

func NewSharedIrsaFactory(ctx context.Context, resolver *authz.Resolver) (*SharedIrsaFactory, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &SharedIrsaFactory{awsCfg: cfg, resolver: resolver}, nil
}

// AWSConfig returns the AWS SDK config the factory was constructed
// with. Exposed so other startup machinery (e.g. the secrets resolver)
// can share the same default credential chain.
func (f *SharedIrsaFactory) AWSConfig() aws.Config { return f.awsCfg }

func (f *SharedIrsaFactory) For(_ context.Context, session Session) (Provider, error) {
	imp := authz.ImpersonationConfig{}
	if f.resolver != nil {
		imp = f.resolver.Resolve(authz.Identity{
			Subject: session.Subject,
			Groups:  session.Groups,
		})
	}
	return &SharedIrsaProvider{
		awsCreds: f.awsCfg.Credentials,
		actor:    session.Subject,
		imperson: ImpersonationConfig{
			UserName: imp.UserName,
			Groups:   imp.Groups,
		},
	}, nil
}
