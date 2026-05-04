package k8s

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/tunnel"
)

func TestSetAgentTunnelLookup_DefaultRefuses(t *testing.T) {
	defer SetAgentTunnelLookup(nil)
	SetAgentTunnelLookup(nil) // reset to default

	_, err := buildAgentRestConfig(context.Background(),
		newFakeProvider("alice", nil),
		clusters.Cluster{Name: "x", Backend: clusters.BackendAgent})
	if err == nil {
		t.Fatal("buildAgentRestConfig with no lookup installed: err=nil")
	}
	if !strings.Contains(err.Error(), "SetAgentTunnelLookup not called") {
		t.Fatalf("err = %v, want mention of missing lookup install", err)
	}
}

func TestSetAgentTunnelLookup_PropagatesNoSession(t *testing.T) {
	defer SetAgentTunnelLookup(nil)
	SetAgentTunnelLookup(func(name string) (AgentDialFunc, error) {
		return nil, tunnel.ErrNoSession
	})

	_, err := buildAgentRestConfig(context.Background(),
		newFakeProvider("alice", nil),
		clusters.Cluster{Name: "missing", Backend: clusters.BackendAgent})
	if !errors.Is(err, tunnel.ErrNoSession) {
		t.Fatalf("err = %v, want ErrNoSession to propagate", err)
	}
}

func TestSetAgentTunnelLookup_HappyPath(t *testing.T) {
	defer SetAgentTunnelLookup(nil)

	SetAgentTunnelLookup(func(name string) (AgentDialFunc, error) {
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, errors.New("test-only: dial intercepted")
		}, nil
	})

	cfg, err := buildAgentRestConfig(context.Background(),
		newFakeProvider("alice", nil),
		clusters.Cluster{Name: "prod-eu", Backend: clusters.BackendAgent})
	if err != nil {
		t.Fatalf("buildAgentRestConfig: %v", err)
	}
	if cfg.Host == "" {
		t.Fatal("rest.Config.Host is empty (clientset would refuse)")
	}
	if !strings.HasPrefix(cfg.Host, "http://") {
		t.Fatalf("Host = %q, want http:// scheme (TLS terminates at agent proxy now per #59)", cfg.Host)
	}
	if !strings.Contains(cfg.Host, "prod-eu") {
		t.Fatalf("Host = %q, want it to embed the cluster name", cfg.Host)
	}
	if cfg.Transport == nil {
		t.Fatal("rest.Config.Transport is nil — agent backend produced a directly-dialing config")
	}
}

func TestBuildAgentRestConfig_AppliesImpersonation(t *testing.T) {
	defer SetAgentTunnelLookup(nil)
	SetAgentTunnelLookup(func(name string) (AgentDialFunc, error) {
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return nil, errors.New("not dialed in this test")
		}, nil
	})

	p := newFakeProvider("alice@example.com",
		[]string{"sec-team", "platform-eng"})
	cfg, err := buildAgentRestConfig(context.Background(),
		p, clusters.Cluster{Name: "c", Backend: clusters.BackendAgent})
	if err != nil {
		t.Fatalf("buildAgentRestConfig: %v", err)
	}
	if cfg.Impersonate.UserName != "alice@example.com" {
		t.Fatalf("Impersonate.UserName = %q, want alice@example.com", cfg.Impersonate.UserName)
	}
	if len(cfg.Impersonate.Groups) != 2 {
		t.Fatalf("Impersonate.Groups = %v, want 2 entries", cfg.Impersonate.Groups)
	}
}

// fakeProviderImpl is a minimal credentials.Provider for these tests.
// We only exercise Impersonation/Actor/Retrieve; AWS Retrieve is
// stubbed because the agent backend never dials AWS.
type fakeProviderImpl struct {
	actor string
	imp   credentials.ImpersonationConfig
}

func newFakeProvider(actor string, groups []string) fakeProviderImpl {
	return fakeProviderImpl{
		actor: actor,
		imp:   credentials.ImpersonationConfig{UserName: actor, Groups: groups},
	}
}

func (f fakeProviderImpl) Retrieve(_ context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: "x", SecretAccessKey: "y"}, nil
}
func (f fakeProviderImpl) Actor() string                                  { return f.actor }
func (f fakeProviderImpl) Impersonation() credentials.ImpersonationConfig { return f.imp }
