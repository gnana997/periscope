// Local HTTP reverse proxy that the agent runs to authenticate
// apiserver requests on behalf of the central server (#59).
//
// Architecture:
//
//   server's http.Client
//      ↓ (Impersonate-User: alice, Impersonate-Group: admin, NO Authorization)
//   tunnel.RoundTripper → tunnel → agent's localDial
//      ↓ (dial 127.0.0.1:proxyPort instead of the apiserver directly)
//   this proxy.Handler.ServeHTTP
//      ↓ injects Authorization: Bearer <agent SA token>
//      ↓ preserves Impersonate-* headers
//      ↓ forwards over HTTPS with kubelet-mounted apiserver CA
//   local apiserver
//      → authenticates: agent SA
//      → authorises: agent SA has impersonate verb (granted by chart's ClusterRole)
//      → re-evaluates as alice@corp + admin group
//      → returns the response
//
// Pre-#59 the agent dialed the apiserver directly with no auth-aware
// layer between server and apiserver. The apiserver rejected every
// request with 401/403 before impersonation was even considered.
// This file fixes that by terminating HTTP on the agent and re-issuing
// each request with the agent's own SA credentials, leaving the
// impersonation headers intact for the apiserver to evaluate normally.

package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// startAPIProxy stands up a localhost HTTP server that forwards every
// request to the local apiserver with the agent's SA bearer token
// attached. The bind address is what the agent's localDial routes
// to instead of the apiserver directly.
//
// Returns when the server shuts down or fails to bind.
func startAPIProxy(inClusterCfg *rest.Config, listenAddr string) error {
	apiserverURL, err := url.Parse(strings.TrimRight(inClusterCfg.Host, "/"))
	if err != nil {
		return fmt.Errorf("parse apiserver URL %q: %w", inClusterCfg.Host, err)
	}
	if apiserverURL.Scheme != "https" {
		// Modern in-cluster configs always present HTTPS. If we ever
		// see plain http here it's a misconfiguration (or kind/test
		// scenario); refuse rather than silently forwarding a bearer
		// token over an unencrypted hop.
		return fmt.Errorf("apiserver URL %q is not https", inClusterCfg.Host)
	}

	if inClusterCfg.BearerToken == "" {
		return errors.New("in-cluster config has no BearerToken — agent SA token missing")
	}

	tlsCfg, err := apiserverTLSConfig(inClusterCfg)
	if err != nil {
		return fmt.Errorf("apiserver TLS config: %w", err)
	}

	upstream := &http.Transport{
		TLSClientConfig: tlsCfg,
		// Match client-go defaults so streaming reads (watch, logs,
		// SSE) don't get unexpected timeouts.
		ForceAttemptHTTP2: true,
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Point at the local apiserver — overrides whatever Host
			// the server's request used (the sentinel "apiserver.<c>.tunnel").
			pr.SetURL(apiserverURL)

			// Inject the agent's SA bearer token. ALWAYS overwrite —
			// we don't trust whatever the server may have sent. The
			// server is supposed to send no Authorization at all
			// (only Impersonate-* headers), but defensive overwrite
			// closes the "what if the server gets compromised and
			// tries to substitute a token" hole.
			pr.Out.Header.Set("Authorization", "Bearer "+inClusterCfg.BearerToken)

			// Strip any X-Forwarded-* the ReverseProxy adds by default;
			// the apiserver doesn't need them and they leak the tunnel
			// internals into the apiserver's audit log.
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")

			// Note: Impersonate-User / Impersonate-Group / Impersonate-Extra-*
			// are NOT in the hop-by-hop header list (RFC 7230 §6.1)
			// so ReverseProxy passes them through unchanged. That's
			// the load-bearing behaviour for #59 — the impersonation
			// chain reaches the apiserver intact.
		},
		Transport: upstream,
		// FlushInterval=-1 means flush after every Write — required
		// for SSE / watch / logs streaming. Without it, ReverseProxy
		// would buffer responses and the SPA's watch streams would
		// stall indefinitely waiting for the buffer to fill.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("proxy.upstream_error",
				"path", r.URL.Path, "method", r.Method, "err", err)
			http.Error(w, "agent → apiserver: "+err.Error(), http.StatusBadGateway)
		},
	}

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           loggingMiddleware(proxy),
		ReadHeaderTimeout: 30 * time.Second,
	}

	slog.Info("agent.api_proxy_listening",
		"addr", listenAddr, "apiserver", apiserverURL.String())

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("api proxy: %w", err)
	}
	return nil
}

// apiserverTLSConfig builds the TLS config the proxy uses for its
// outbound HTTPS to the apiserver. Trust anchor is the kubelet-
// mounted CA bundle from inClusterCfg.CAData (or CAFile).
func apiserverTLSConfig(inClusterCfg *rest.Config) (*tls.Config, error) {
	pool := x509.NewCertPool()
	switch {
	case len(inClusterCfg.CAData) > 0:
		if !pool.AppendCertsFromPEM(inClusterCfg.CAData) {
			return nil, errors.New("could not append apiserver CA from CAData")
		}
	case inClusterCfg.CAFile != "":
		// rest.InClusterConfig() typically sets CAData (loads file
		// contents inline); CAFile fallback for completeness.
		ca, err := readFile(inClusterCfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file %q: %w", inClusterCfg.CAFile, err)
		}
		if !pool.AppendCertsFromPEM(ca) {
			return nil, errors.New("could not append apiserver CA from CAFile")
		}
	default:
		return nil, errors.New("in-cluster config has neither CAData nor CAFile")
	}
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, nil
}

// readFile is a thin indirection so tests can stub the CAFile load
// without touching the filesystem. Production points at os.ReadFile.
var readFile = os.ReadFile

