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
	"crypto/tls"
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
// bytes flow differently.
//
// The Host is a sentinel ("https://apiserver.<cluster>.tunnel") that
// the agent ignores when it dials the local apiserver — it's there
// because rest.Config.Host is required, but the tunnel transport
// short-circuits the actual dial so the value never leaves the
// process.
func buildAgentRestConfig(_ context.Context, p credentials.Provider, c clusters.Cluster) (*rest.Config, error) {
	dial, err := currentAgentLookup()(c.Name)
	if err != nil {
		return nil, fmt.Errorf("agent backend %q: %w", c.Name, err)
	}

	cfg := &rest.Config{
		Host: agentHostSentinel(c.Name),
		// Disable TLS verification on the *outer* hop because the
		// outer hop never leaves this process — the tunnel
		// transport hands the bytes straight to a tunneled net.Conn.
		// The inner hop (agent → real apiserver) still uses the
		// agent's in-cluster CA bundle, which is the actual TLS
		// trust boundary.
		TLSClientConfig: rest.TLSClientConfig{Insecure: true},
		// rest.Config.Transport pre-empts every other dial setting,
		// which is exactly what we want.
		Transport: tunnel.NewRoundTripper(
			func(ctx context.Context, network, addr string) (net.Conn, error) {
				return dial(ctx, network, addr)
			},
			tunnel.RoundTripperOptions{
				// Skip TLS on the tunnel-side connection; see the
				// rationale in TLSClientConfig above. The inner
				// hop is what actually carries TLS.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		),
	}
	applyImpersonation(cfg, p)
	return cfg, nil
}

// agentHostSentinel produces the placeholder Host the rest.Config
// requires. The transport short-circuits the dial, so the actual
// hostname is never resolved or contacted — it only needs to be a
// syntactically-valid URL for client-go's request building.
func agentHostSentinel(cluster string) string {
	return "https://apiserver." + cluster + ".tunnel"
}
