package tunnel

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"
)

// DialFunc is a tunnel-bound DialContext. It opens a single TCP
// connection through the agent's tunnel to the address `addr` (which
// the agent then dials locally — typically the cluster's apiserver).
//
// Same signature as net.Dialer.DialContext so it slots into
// http.Transport.DialContext directly.
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// RoundTripperOptions configure NewRoundTripper.
type RoundTripperOptions struct {
	// TLSClientConfig is used when the apiserver behind the tunnel
	// expects TLS (the default for any real K8s apiserver). Callers
	// pass the same *tls.Config they would for a direct connection;
	// the tunnel is transparent at the byte level so the cert chain
	// presented by the apiserver is what's verified, not anything
	// about the tunnel.
	//
	// Nil means no TLS — fine for in-process tests.
	TLSClientConfig *tls.Config

	// Tuning knobs with sensible defaults applied at NewRoundTripper.
	// Override only when you know why.
	IdleConnTimeout       time.Duration // default 30s
	ResponseHeaderTimeout time.Duration // default 30s
	MaxIdleConnsPerHost   int           // default 4

	// DisableKeepAlives forces a fresh tunnel dial per request. False
	// in production (default) — clientset reuses connections through
	// the multiplexed WebSocket. True is useful in tests that assert
	// per-request behavior.
	DisableKeepAlives bool
}

// NewRoundTripper builds an http.RoundTripper whose transport opens
// connections via the supplied DialFunc instead of the host network.
// Callers (notably internal/k8s/client.go::buildAgentRestConfig once
// #42d lands) drop this into rest.Config.Transport and existing
// handlers continue to work unmodified.
//
// Why http.Transport rather than rolling our own RoundTrip:
// http.Transport already handles connection pooling, request body
// rewriting, response buffering, deadline propagation, HTTP/2
// negotiation, and the dozen other minor protocol concerns. We get
// all of that for free by injecting DialContext.
func NewRoundTripper(dial DialFunc, opts RoundTripperOptions) http.RoundTripper {
	if opts.IdleConnTimeout == 0 {
		opts.IdleConnTimeout = 30 * time.Second
	}
	if opts.ResponseHeaderTimeout == 0 {
		opts.ResponseHeaderTimeout = 30 * time.Second
	}
	if opts.MaxIdleConnsPerHost == 0 {
		opts.MaxIdleConnsPerHost = 4
	}

	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dial(ctx, network, addr)
		},
		TLSClientConfig:       opts.TLSClientConfig,
		IdleConnTimeout:       opts.IdleConnTimeout,
		ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
		MaxIdleConnsPerHost:   opts.MaxIdleConnsPerHost,
		DisableKeepAlives:     opts.DisableKeepAlives,
		// We intentionally do NOT set Proxy / ProxyFromEnvironment —
		// the tunnel IS the proxy, env vars don't apply.
		Proxy: nil,
	}
}
