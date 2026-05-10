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
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"

	"k8s.io/client-go/rest"

	"github.com/gnana997/periscope/internal/agentupstream"
)

// startAPIProxy stands up a localhost HTTP server that forwards every
// request to the local apiserver with the agent's SA bearer token
// attached. The bind address is what the agent's localDial routes
// to instead of the apiserver directly.
//
// clusterName is stamped into structured error responses + slog so the
// central server (and operators reading kubectl logs) can tell which
// cluster a transport failure came from without having to grep for
// pod identity.
//
// Returns when the server shuts down or fails to bind.
func startAPIProxy(inClusterCfg *rest.Config, clusterName, listenAddr string) error {
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
			// are NOT in the hop-by-hop header list (RFC 7230 6.1)
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
			writeUpstreamErrorJSON(w, r, clusterName, err)
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

// writeUpstreamErrorJSON classifies err, logs a structured warning line,
// and writes a JSON body the central server can parse. Status code
// follows category (504 for timeout; 502 for everything else) so the
// kubectl-style 502/504 distinction stays meaningful for non-Periscope
// HTTP clients (k8s curl, debug shells) too.
//
// The on-wire shape is agentupstream.Envelope; the central server's
// internal/k8s/agent_upstream_error.go inspects bodies whose `code`
// is in agentupstream.RecognizedCodes and converts them to a typed
// *AgentUpstreamError before they reach handlers.
func writeUpstreamErrorJSON(w http.ResponseWriter, r *http.Request, cluster string, err error) {
	category, message, status := classifyUpstreamError(err)

	traceID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
	if traceID == "" {
		traceID = newFallbackTraceID()
	}

	slog.Warn("proxy.upstream_error",
		"path", r.URL.Path,
		"method", r.Method,
		"category", category,
		"cluster", cluster,
		"trace_id", traceID,
		"err", err,
	)

	body := agentupstream.Envelope{
		Code:     agentupstream.CodeAgentUpstream,
		Message:  message,
		Category: category,
		Cluster:  cluster,
		Detail:   err.Error(),
		TraceID:  traceID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// classifyUpstreamError maps a reverse-proxy transport error into the
// (category, friendly message, http status) triple the JSON body and
// access log share.
//
// Priority order is intentional and load-bearing:
//  1. TLS first — *tls.CertificateVerificationError /
//     tls.RecordHeaderError / x509.UnknownAuthorityError /
//     x509.HostnameError. Always category=tls regardless of how the
//     error eventually unwraps.
//  2. Timeout next — context.DeadlineExceeded and any net.Error whose
//     Timeout()=true (covers *net.OpError wrapping syscall.ETIMEDOUT
//     plus dial / read / write deadlines). A slow TLS handshake that
//     itself hits ctx.DeadlineExceeded therefore reports as `timeout`,
//     not `tls` — operators see "timeout" and check network/tls
//     latency, which is the correct triage path.
//  3. Network — *net.DNSError + the connect/reset syscall set, then a
//     fallback *net.OpError catch-all so anything not specifically
//     classified still surfaces as actionable network trouble.
//  4. unknown — last resort. Operators are unlikely to see this in
//     practice; it exists so the function is total.
//
// Auth (401/403) is intentionally NOT a category here — apiserver auth
// failures arrive as normal HTTP responses, never reach ErrorHandler,
// and are surfaced on the access-log path via proxy.apiserver_error.
func classifyUpstreamError(err error) (category, message string, status int) {
	if err == nil {
		return "unknown", "agent could not reach the cluster's apiserver", http.StatusBadGateway
	}

	// TLS classifications first — these show up as wrapped *url.Error
	// containing tls/x509 types, so errors.As reaches them through the
	// wrapping chain.
	var (
		certVerifyErr *tls.CertificateVerificationError
		recordErr     tls.RecordHeaderError
		unkAuthority  x509.UnknownAuthorityError
		hostnameErr   x509.HostnameError
	)
	switch {
	case errors.As(err, &certVerifyErr),
		errors.As(err, &recordErr),
		errors.As(err, &unkAuthority),
		errors.As(err, &hostnameErr):
		return "tls", "agent could not verify the cluster's apiserver TLS certificate", http.StatusBadGateway
	}

	// Timeout — both deadline-exceeded contexts and i/o timeouts. The
	// `os.IsTimeout` check covers *net.OpError wrapping a syscall
	// ETIMEDOUT plus net.Error.Timeout() implementers.
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "request to the cluster's apiserver timed out", http.StatusGatewayTimeout
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", "request to the cluster's apiserver timed out", http.StatusGatewayTimeout
	}

	// Network — refused, DNS failure, generic OpError that isn't a
	// timeout. ECONNRESET counts here too; the apiserver dropped us.
	var (
		dnsErr *net.DNSError
		opErr  *net.OpError
	)
	switch {
	case errors.As(err, &dnsErr):
		return "network", "agent could not resolve the cluster's apiserver address", http.StatusBadGateway
	case errors.Is(err, syscall.ECONNREFUSED),
		errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, syscall.EHOSTUNREACH),
		errors.Is(err, syscall.ENETUNREACH),
		errors.Is(err, net.ErrClosed):
		return "network", "agent could not reach the cluster's apiserver", http.StatusBadGateway
	case errors.As(err, &opErr):
		return "network", "agent could not reach the cluster's apiserver", http.StatusBadGateway
	}

	return "unknown", "agent could not reach the cluster's apiserver", http.StatusBadGateway
}

// newFallbackTraceID returns a short hex token used when the inbound
// request didn't carry an X-Request-Id (e.g. operator-issued curl,
// non-Periscope clients). Eight bytes of randomness is plenty to make
// log lines greppable without colliding inside a single agent process.
func newFallbackTraceID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Random source failure on a Linux box is essentially impossible;
		// fall back to a timestamp so we still produce *something*
		// greppable rather than empty string.
		return "fallback-" + time.Now().UTC().Format("150405.000000000")
	}
	return hex.EncodeToString(b[:])
}
