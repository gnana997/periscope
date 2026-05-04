// Agent backend wiring. The tunnel package owns the WebSocket
// transport + http.RoundTripper plumbing; this file is the glue that
// lets the existing handlers reach an agent-backed cluster's
// apiserver via that transport.
//
// Lifetime model:
//
//   - cmd/periscope wires up a *tunnel.Server at startup and calls
//     SetAgentTunnelLookup(tunnelServer.DialerFor) so the lookup hook
//     points at the live session map.
//   - On every request to an agent-backed cluster, buildAgentRestConfig
//     calls the lookup, gets a tunnel-bound DialFunc, wraps it in a
//     RoundTripper, and stuffs it into rest.Config.Transport.
//   - The clientset built from that rest.Config makes apiserver calls
//     transparently through the tunnel — handlers, watch streams, and
//     SSE all work unmodified.
//
// Tests stub the lookup via SetAgentTunnelLookup just like newClientFn.

package k8s

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"k8s.io/client-go/rest"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/tunnel"
)

// AgentDialFunc is the tunnel-bound dial function the lookup hook
// returns. Aliased here so callers don't import tunnel just to satisfy
// the signature.
type AgentDialFunc = tunnel.DialFunc

// AgentTunnelLookup returns the tunnel-bound dialer for the given
// cluster name, or an error (typically tunnel.ErrNoSession) if no
// agent is currently connected.
type AgentTunnelLookup func(clusterName string) (AgentDialFunc, error)

// agent tunnel lookup hook. Defaults to "no agents configured" so
// production with the eks-only path never accidentally accepts an
// agent backend it can't service.
var (
	agentLookupMu sync.RWMutex
	agentLookup   AgentTunnelLookup = noAgentLookup
)

func noAgentLookup(name string) (AgentDialFunc, error) {
	return nil, fmt.Errorf("agent backend not configured (cluster %q): %w",
		name, errors.New("SetAgentTunnelLookup not called"))
}

// SetAgentTunnelLookup installs the lookup function used by the
// agent backend. cmd/periscope calls this once at startup with the
// tunnel server's DialerFor; tests substitute their own.
func SetAgentTunnelLookup(lookup AgentTunnelLookup) {
	if lookup == nil {
		lookup = noAgentLookup
	}
	agentLookupMu.Lock()
	defer agentLookupMu.Unlock()
	agentLookup = lookup
}

func currentAgentLookup() AgentTunnelLookup {
	agentLookupMu.RLock()
	defer agentLookupMu.RUnlock()
	return agentLookup
}

// buildAgentRestConfig constructs a *rest.Config whose Transport is
// the tunnel-bound RoundTripper. The clientset built from this config
// looks identical to a kubeconfig-built one to handler code; only the
//
// The Host is a sentinel ("http://apiserver.<cluster>.tunnel") that
// the agent's local HTTP proxy receives — it's there because
// rest.Config.Host is required, but the tunnel transport short-
// circuits the actual dial so the value never leaves the process.
//
// Why http:// (not https://): TLS now terminates at the agent's
// local reverse proxy on the managed cluster (#59 fix). The agent
// re-issues each incoming request to the local apiserver over
// HTTPS using the kubelet-mounted CA bundle, with the agent's SA
// bearer token attached. Pre-#59 the server tried to do end-to-end
// TLS to the apiserver through the tunnel with no auth headers —
// the apiserver rejected every request with 401/403 before
// impersonation was even evaluated.
func buildAgentRestConfig(_ context.Context, p credentials.Provider, c clusters.Cluster) (*rest.Config, error) {
	dial, err := currentAgentLookup()(c.Name)
	if err != nil {
		return nil, fmt.Errorf("agent backend %q: %w", c.Name, err)
	}

	cfg := &rest.Config{
		Host: agentHostSentinel(c.Name),
		// rest.Config.Transport pre-empts every other dial/TLS setting.
		// We deliberately set NO TLSClientConfig — client-go refuses
		// to build a clientset when both Transport and TLSClientConfig
		// are set, AND the tunnel + agent-proxy chain handles auth and
		// TLS itself: the outer hop (server → tunnel) is plain bytes
		// inside this process; the inner hop (agent → apiserver) is
		// HTTPS with the kubelet's CA bundle, terminated at the agent.
		Transport: tunnel.NewRoundTripper(
			func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dial(ctx, network, addr)
			},
			tunnel.RoundTripperOptions{},
		),
	}
	applyImpersonation(cfg, p)
	return cfg, nil
}

// agentHostSentinel produces the placeholder Host the rest.Config
// requires. The transport short-circuits the dial, so the actual
// hostname is never resolved or contacted — it only needs to be a
// syntactically-valid URL for client-go's request building. The
// http:// scheme matters: client-go uses it to decide whether to
// negotiate TLS on the (intercepted) outbound dial, and we want it
// to NOT, since TLS terminates at the agent's reverse proxy.
func agentHostSentinel(cluster string) string {
	return "http://apiserver." + cluster + ".tunnel"
}
