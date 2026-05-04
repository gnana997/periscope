package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/rancher/remotedialer"
)

// LocalDialer is the function the agent invokes to fulfil a tunneled
// request — typically "dial the local apiserver." Same shape as a
// stdlib DialContext.
//
// In production cmd/periscope-agent passes a closure that resolves
// kubernetes.default.svc on the agent's pod network and opens a TCP
// connection there. Tests pass a closure that dials a local
// httptest.Server.
type LocalDialer func(ctx context.Context, network, addr string) (net.Conn, error)

// ClientOptions configure the agent-side tunnel client.
type ClientOptions struct {
	// ServerURL is the central server's tunnel endpoint, e.g.
	//   wss://periscope.example.com/api/agents/connect
	// Both ws:// and wss:// are accepted; production should always
	// be wss://.
	ServerURL string

	// ClientName is the cluster name the agent claims. The server's
	// authorizer validates this against the agent's mTLS cert; here
	// it just gets sent in a header so the server can index the
	// session by it.
	ClientName string

	// TLSClientConfig carries the per-cluster mTLS cert + the
	// server's CA. In #42a this is filled by tests with httptest's
	// generated cert; in #42c the agent loads it from a Secret
	// written by the registration handler.
	TLSClientConfig *tls.Config

	// LocalDial is the function invoked when the server asks the
	// agent to dial somewhere (typically the local apiserver).
	LocalDial LocalDialer

	// Headers are added to the WebSocket upgrade request. Used by
	// tests to plant a fixture identity when TLS isn't in play, and
	// by #42c to send the cluster-name claim header.
	Headers http.Header

	// HandshakeTimeout caps the WebSocket upgrade dial. Default 10s.
	HandshakeTimeout time.Duration

	// Reconnect tuning. Zero values get sensible defaults.
	InitialBackoff time.Duration // default 1s
	MaxBackoff     time.Duration // default 30s
	BackoffJitter  float64       // default 0.3 (±30%)
}

// Client is the agent-side dial-out tunnel. One Client holds one
// long-lived WebSocket to the central server, automatically
// reconnecting on drop.
type Client struct {
	opts   ClientOptions
	wsDial *websocket.Dialer
}

// NewClient constructs an agent tunnel client. Call Run to start the
// dial-out + reconnect loop; it blocks until ctx is cancelled.
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.ServerURL == "" {
		return nil, errors.New("tunnel: ClientOptions.ServerURL is required")
	}
	if opts.ClientName == "" {
		return nil, errors.New("tunnel: ClientOptions.ClientName is required")
	}
	if opts.LocalDial == nil {
		return nil, errors.New("tunnel: ClientOptions.LocalDial is required")
	}
	if opts.InitialBackoff == 0 {
		opts.InitialBackoff = 1 * time.Second
	}
	if opts.MaxBackoff == 0 {
		opts.MaxBackoff = 30 * time.Second
	}
	if opts.BackoffJitter == 0 {
		opts.BackoffJitter = 0.3
	}
	if opts.HandshakeTimeout == 0 {
		opts.HandshakeTimeout = 10 * time.Second
	}

	wsDial := &websocket.Dialer{
		HandshakeTimeout: opts.HandshakeTimeout,
		TLSClientConfig:  opts.TLSClientConfig,
	}
	return &Client{opts: opts, wsDial: wsDial}, nil
}

// Run blocks until ctx is cancelled, holding the tunnel open and
// reconnecting on drops with jittered exponential backoff. Returns
// nil when ctx is cancelled cleanly.
func (c *Client) Run(ctx context.Context) error {
	backoff := c.opts.InitialBackoff
	headers := c.opts.Headers
	if headers == nil {
		headers = make(http.Header)
	}

	// remotedialer.Dialer is just a typed alias for our LocalDialer
	// shape — direct cast.
	localDialer := remotedialer.Dialer(c.opts.LocalDial)

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		// ConnectToProxyWithDialer blocks for the duration of the
		// session; returns when the WebSocket closes (server kicks,
		// network drops, ctx cancelled).
		err := remotedialer.ConnectToProxyWithDialer(
			ctx,
			c.opts.ServerURL,
			headers,
			c.allowDial,
			c.wsDial,
			localDialer,
			c.onConnect,
		)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			slog.WarnContext(ctx, "tunnel.client_disconnected",
				"server", c.opts.ServerURL, "err", err, "next_backoff_ms", backoff.Milliseconds())
		}

		// Sleep with jitter; the jitter prevents reconnect storms
		// when many agents simultaneously notice a server restart.
		sleep := jittered(backoff, c.opts.BackoffJitter)
		select {
		case <-time.After(sleep):
		case <-ctx.Done():
			return nil
		}

		// Exponential, capped at MaxBackoff.
		backoff *= 2
		if backoff > c.opts.MaxBackoff {
			backoff = c.opts.MaxBackoff
		}
	}
}

// allowDial is remotedialer's per-dial authorizer (ConnectAuthorizer).
// It runs every time the server asks the agent to dial something.
// Returning false aborts the dial.
//
// For v1.x.0 we accept all dials the server requests — the trust
// boundary is the WebSocket handshake's mTLS, not per-dial
// allow-listing. A future hardening could restrict to the local
// apiserver's host/port; tracked separately when we have a real attack
// model to design against.
func (c *Client) allowDial(proto, address string) bool {
	return true
}

// onConnect runs once per established session. Used as the visibility
// hook for "agent established the tunnel."
func (c *Client) onConnect(ctx context.Context, _ *remotedialer.Session) error {
	slog.InfoContext(ctx, "tunnel.client_connected",
		"server", c.opts.ServerURL, "client_name", c.opts.ClientName)
	return nil
}

// jittered adds ±jitter*100% randomness to base, useful to prevent
// thundering-herd reconnects across an agent fleet.
func jittered(base time.Duration, jitter float64) time.Duration {
	if jitter <= 0 {
		return base
	}
	delta := float64(base) * jitter * (2*rand.Float64() - 1)
	return base + time.Duration(delta)
}

// Compile-time interface assertion: ensure Client doesn't drift away
// from being usable as the agent's only tunnel handle.
var _ interface {
	Run(context.Context) error
} = (*Client)(nil)
