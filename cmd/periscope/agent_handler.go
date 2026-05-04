// Server-side wiring for the agent backend (#42).
//
// What lives here:
//   - bootstrapAgentCA:    load (or generate + persist) the per-deployment
//                          CA from a K8s Secret in the server's namespace.
//   - serverTLSConfig:     mint a server cert from the CA and build the
//                          *tls.Config the tunnel listener uses (with
//                          ClientAuth=RequireAndVerifyClientCert).
//   - mountAgentRoutes:    mount /api/agents/tokens (admin-only) and
//                          /api/agents/register (unauth, token-gated)
//                          on the main HTTP router.
//   - runTunnelListener:   start the dedicated TLS listener that hosts
//                          /api/agents/connect, runs until ctx cancels.
//   - registerAgentTunnel: top-level wire-up called from main(), composes
//                          all of the above and installs the lookup hook
//                          into internal/k8s/client.go.
//
// Operator flow:
//   1. Helm install creates an empty K8s Secret (default
//      "periscope-agent-ca") plus RBAC granting the server SA
//      get/update on it.
//   2. On first start, the server reads the Secret, sees it's empty,
//      generates a CA, writes the cert+key back via UPDATE.
//   3. Subsequent starts read the populated Secret directly. The CA
//      stays stable across pod restarts so all previously-issued
//      agent certs continue to validate.
//   4. The Secret is also the source for the bundle we send back to
//      agents in the registration response (CABundle field).
//
// Operators who want their own externally-managed PKI can pre-populate
// the Secret with their own ca.crt + ca.key; the server treats it
// load-only when both keys are present.

package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/gnana997/periscope/internal/auth"
	"github.com/gnana997/periscope/internal/authz"
	"github.com/gnana997/periscope/internal/clusters"
	internalk8s "github.com/gnana997/periscope/internal/k8s"
	"github.com/gnana997/periscope/internal/tunnel"
)

// agentTunnelOptions captures everything the agent-tunnel wiring
// needs from main(). Zero values disable the feature so a fresh
// deploy without agent backends doesn't open any extra listeners.
type agentTunnelOptions struct {
	// Enabled is true when at least one cluster in the registry uses
	// BackendAgent OR when PERISCOPE_AGENT_LISTEN_ADDR is set
	// explicitly (the second case is for "I want to register
	// agent-backed clusters at runtime, none in YAML yet").
	Enabled bool

	// ListenAddr is the bind address for the dedicated TLS listener
	// hosting /api/agents/connect. Default ":8443". The chart
	// renders this from values.agent.listenAddr.
	ListenAddr string

	// CASecretNamespace + CASecretName identify the K8s Secret that
	// stores the per-deployment CA. The Helm chart pre-creates it
	// empty; the server fills it on first boot.
	CASecretNamespace string
	CASecretName      string

	// TunnelDNSNames are the SANs baked into the server cert
	// presented on the tunnel listener. Agents validate the server's
	// cert against these. Default ["localhost"] (kind/dev only);
	// production passes the real DNS name (e.g.
	// "agents.periscope.example.com").
	TunnelDNSNames []string
}

// _ is a compile-time silencer for the corev1 import — used only via
// type alias inside the bootstrap helper, but go vet wants the
// reference. Removing this once the typed Secret struct shows up in
// new helpers (RBAC update flow) will surface organically.
var _ = corev1.SecretTypeOpaque

// registerAgentTunnel wires up the full agent-backend story:
// CA bootstrap, server cert, tunnel.Server, mTLS authorizer, listener,
// route mounting, and the internal/k8s lookup hook.
//
// Returns a stop function the caller defers; a no-op when not enabled.
func registerAgentTunnel(
	ctx context.Context,
	router chi.Router,
	opts agentTunnelOptions,
	registry *clusters.Registry,
	resolver *authz.Resolver,
	sessionStore auth.SessionStore,
	authCfg auth.Config,
) (stop func(), err error) {
	if !opts.Enabled {
		return func() {}, nil
	}
	if opts.ListenAddr == "" {
		opts.ListenAddr = ":8443"
	}
	if opts.CASecretName == "" {
		opts.CASecretName = "periscope-agent-ca"
	}
	if len(opts.TunnelDNSNames) == 0 {
		opts.TunnelDNSNames = []string{"localhost"}
	}

	// In-cluster client for the CA Secret. Doesn't reuse internal/k8s
	// — that package builds clientsets for managed clusters, not the
	// server's own namespace.
	inCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("agent tunnel: in-cluster config: %w", err)
	}
	kc, err := kubernetes.NewForConfig(inCfg)
	if err != nil {
		return nil, fmt.Errorf("agent tunnel: kube client: %w", err)
	}
	if opts.CASecretNamespace == "" {
		opts.CASecretNamespace, err = ownNamespace()
		if err != nil {
			return nil, fmt.Errorf("agent tunnel: derive namespace: %w", err)
		}
	}

	ca, err := bootstrapAgentCA(ctx, kc, opts.CASecretNamespace, opts.CASecretName)
	if err != nil {
		return nil, fmt.Errorf("agent tunnel: CA bootstrap: %w", err)
	}
	slog.Info("agent CA loaded",
		"namespace", opts.CASecretNamespace, "secret", opts.CASecretName,
		"cn", ca.Cert().Subject.CommonName,
		"expires_at", ca.Cert().NotAfter.Format(time.RFC3339))

	// Token store + reaper.
	tokenStore := tunnel.NewTokenStore(tunnel.TokenStoreOptions{})
	tokenStopCh := make(chan struct{})
	go tokenStore.Run(tokenStopCh)

	// mTLS authorizer: only registry-listed BackendAgent clusters
	// pass — even a cert with a valid CN gets rejected if the
	// operator has since dropped the cluster from the registry.
	authorizer := &tunnel.MTLSAuthorizer{
		NameAllowed: func(name string) bool {
			c, ok := registry.ByName(name)
			return ok && c.Backend == clusters.BackendAgent
		},
	}
	tunnelSrv := tunnel.NewServer(tunnel.ServerOptions{
		Authorizer: authorizer.Authorize,
		Observer: func(e tunnel.SessionEvent) {
			slog.Info("agent.session_event",
				"cluster", e.ClusterName, "connected", e.Connected,
				"at", e.At.Format(time.RFC3339))
		},
	})

	// Wire the lookup hook so internal/k8s/client.go's
	// buildAgentRestConfig can resolve cluster name → DialFunc.
	internalk8s.SetAgentTunnelLookup(tunnelSrv.DialerFor)

	// Start the loopback CONNECT proxy that exec uses to reach
	// agent-managed clusters. Required because client-go's WebSocket
	// and SPDY executors bypass rest.Config.Transport but honour
	// rest.Config.Proxy — the proxy translates per-cluster CONNECT
	// requests into tunnel dials. See internal/k8s/agent_exec_proxy.go.
	execProxyStop, perr := internalk8s.StartAgentExecProxy(ctx)
	if perr != nil {
		return nil, fmt.Errorf("agent exec proxy: %w", perr)
	}


	mountAgentRoutes(router, tokenStore, ca, resolver, sessionStore, authCfg)

	// Mint the server cert + bind the TLS listener.
	serverCertPEM, serverKeyPEM, err := ca.SignServer(
		"periscope-server", opts.TunnelDNSNames, 0)
	if err != nil {
		return nil, fmt.Errorf("agent tunnel: server cert: %w", err)
	}
	tlsCfg, err := serverTLSConfig(ca, serverCertPEM, serverKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("agent tunnel: TLS config: %w", err)
	}

	tunnelStop := runTunnelListener(opts.ListenAddr, tlsCfg, tunnelSrv)

	return func() {
		close(tokenStopCh)
		// Stop the exec proxy first so its in-flight CONNECT-tunneled
		// connections finish their io.Copy before tunnel teardown — see
		// internal/k8s/agent_exec_proxy.go.
		execProxyStop()
		tunnelStop()
	}, nil
}

// bootstrapAgentCA loads the CA from the named Secret. If the Secret
// exists but is empty (chart-installed placeholder, first boot),
// generates a fresh CA and writes the bundle back via UPDATE.
//
// Operators who want to use externally-managed PKI can pre-populate
// the Secret's ca.crt + ca.key keys; the server treats it as load-
// only when both are non-empty.
func bootstrapAgentCA(ctx context.Context, kc kubernetes.Interface, namespace, name string) (*tunnel.CA, error) {
	const (
		keyCert = "ca.crt"
		keyKey  = "ca.key"
	)
	sec, err := kc.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("CA Secret %s/%s not found — Helm chart should pre-create it as an empty placeholder", namespace, name)
	}
	if err != nil {
		return nil, fmt.Errorf("get CA Secret: %w", err)
	}

	cert := sec.Data[keyCert]
	key := sec.Data[keyKey]
	if len(cert) > 0 && len(key) > 0 {
		ca, err := tunnel.LoadCA(&tunnel.CABundle{CertPEM: cert, KeyPEM: key})
		if err != nil {
			return nil, fmt.Errorf("load existing CA: %w", err)
		}
		return ca, nil
	}

	slog.Info("agent CA Secret is empty — generating fresh CA",
		"namespace", namespace, "secret", name)

	ca, bundle, err := tunnel.GenerateCA("periscope-agent", tunnel.CertValidity{})
	if err != nil {
		return nil, fmt.Errorf("generate CA: %w", err)
	}

	if sec.Data == nil {
		sec.Data = map[string][]byte{}
	}
	sec.Data[keyCert] = bundle.CertPEM
	sec.Data[keyKey] = bundle.KeyPEM
	if sec.Annotations == nil {
		sec.Annotations = map[string]string{}
	}
	sec.Annotations["periscope.dev/ca-bootstrap-at"] = time.Now().UTC().Format(time.RFC3339)

	if _, err := kc.CoreV1().Secrets(namespace).Update(ctx, sec, metav1.UpdateOptions{}); err != nil {
		return nil, fmt.Errorf("persist freshly-generated CA: %w", err)
	}
	return ca, nil
}

// serverTLSConfig builds the *tls.Config the tunnel listener uses.
// Server cert + key from the CA, ClientAuth set so every inbound
// connection MUST present a client cert that chains to the same CA.
func serverTLSConfig(ca *tunnel.CA, serverCertPEM, serverKeyPEM []byte) (*tls.Config, error) {
	pair, err := tls.X509KeyPair(serverCertPEM, serverKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse server cert/key: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{pair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.ClusterPool(),
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// mountAgentRoutes wires the two HTTP-side registration routes on
// the main router. /tokens is admin-tier-only; /register is
// unauthenticated (the bootstrap token IS the auth).
func mountAgentRoutes(
	router chi.Router,
	store *tunnel.TokenStore,
	ca *tunnel.CA,
	resolver *authz.Resolver,
	sessionStore auth.SessionStore,
	authCfg auth.Config,
) {
	router.With(adminOnlyMiddleware(sessionStore, authCfg, resolver)).
		Post("/api/agents/tokens", tunnel.MintTokenHandler(store))

	router.Post("/api/agents/register", tunnel.RegisterHandler(store, ca, 0))
}

// adminOnlyMiddleware refuses requests whose session does not resolve
// to the admin tier. Used to gate /api/agents/tokens — minting a
// bootstrap token can register a cluster, so it must not be a surface
// anyone signed in can hit.
func adminOnlyMiddleware(_ auth.SessionStore, _ auth.Config, resolver *authz.Resolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s, ok := auth.SessionFromContext(r.Context())
			if !ok {
				http.Error(w, "unauthenticated", http.StatusUnauthorized)
				return
			}
			if resolver == nil {
				http.Error(w, "agent tokens require tier authz", http.StatusForbidden)
				return
			}
			tier := resolver.ResolvedTier(authz.Identity{Subject: s.Subject, Groups: s.Groups})
			if tier != "admin" {
				http.Error(w, "admin tier required", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// runTunnelListener starts the dedicated TLS listener for
// /api/agents/connect. Runs in its own goroutine; returned stop fn
// shuts it down.
//
// Why a separate listener instead of mounting on the main router:
// the main router serves browser traffic + JSON APIs that go through
// an HTTP-terminating ingress (ALB strips client certs). The tunnel
// MUST see client certs end-to-end, so we expose it on a separate
// port that operators wire with TLS passthrough (NLB or TCP LB).
func runTunnelListener(addr string, tlsCfg *tls.Config, tunnelSrv *tunnel.Server) func() {
	mux := http.NewServeMux()
	mux.Handle("/api/agents/connect", http.HandlerFunc(tunnelSrv.Connect))

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
	}

	listener, err := tls.Listen("tcp", addr, tlsCfg)
	if err != nil {
		slog.Error("agent tunnel: listen failed", "addr", addr, "err", err)
		return func() {}
	}
	slog.Info("agent tunnel listening", "addr", addr)

	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("agent tunnel: serve exited", "err", err)
		}
	}()

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
}

// registryHasAgentBackend reports whether at least one entry in the
// registry uses BackendAgent. Cmd-side decision used to auto-enable
// the tunnel listener when the operator has registered agent
// clusters in YAML; otherwise the chart's `agent.enabled` value
// drives the decision via PERISCOPE_AGENT_LISTEN_ADDR.
func registryHasAgentBackend(reg *clusters.Registry) bool {
	for _, c := range reg.List() {
		if c.Backend == clusters.BackendAgent {
			return true
		}
	}
	return false
}

// ownNamespace reads the in-pod namespace file. The kubelet mounts
// this on every pod with a ServiceAccount.
func ownNamespace() (string, error) {
	const path = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// parseAgentTunnelSANs splits a comma-separated env var into a slice
// of DNS names baked into the server cert SAN. Empty input returns
// nil so registerAgentTunnel falls back to its ["localhost"] default.
func parseAgentTunnelSANs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		s := strings.TrimSpace(p)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// agentBackedNames returns the names of all BackendAgent clusters in
// the registry. Used for an informational log line at startup.
func agentBackedNames(reg *clusters.Registry) []string {
	out := []string{}
	for _, c := range reg.List() {
		if c.Backend == clusters.BackendAgent {
			out = append(out, c.Name)
		}
	}
	return out
}
