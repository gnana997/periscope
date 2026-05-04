// periscope-agent — the per-cluster tunnel client.
//
// Runs as a Deployment in any K8s cluster you want Periscope to
// manage. Dials out to the central server over WebSocket, presents
// an mTLS client cert (per-cluster identity, signed by the server's
// CA at registration), and serves whatever bytes the server sends
// by dialing the local apiserver.
//
// Boot states:
//
//  1. First run, no Secret: read PERISCOPE_REGISTRATION_TOKEN, generate
//     keypair + CSR, POST /api/agents/register, persist cert + key +
//     server CA into the agent's state Secret, drop into state #2.
//
//  2. Secret present: load cert/key/CA, build TLS config, run the
//     tunnel client. Blocks for the lifetime of the pod with jittered
//     reconnect on drops.
//
//  3. Cert near expiry → re-register. Deferred to a follow-up.
//
// Configuration (env vars, all required unless noted):
//
//	PERISCOPE_SERVER_URL            wss://periscope.example.com
//	PERISCOPE_CLUSTER_NAME          name registered with the server
//	PERISCOPE_REGISTRATION_TOKEN    bootstrap; only needed on first boot
//	PERISCOPE_AGENT_NAMESPACE       defaults to current pod's namespace
//	PERISCOPE_AGENT_SECRET_NAME     defaults to "periscope-agent-state"
//	PERISCOPE_AGENT_HEALTH_ADDR     defaults to ":8081"
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/gnana997/periscope/internal/tunnel"
)

// secret data keys for the persisted agent state.
const (
	secretKeyClientCert = "client.crt"
	secretKeyClientKey  = "client.key"
	secretKeyServerCA   = "server-ca.crt"
)

// kubelet-mounted SA paths.
const (
	saNamespacePath = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"
)

func main() {
	if err := run(); err != nil {
		slog.Error("agent exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadAgentConfig()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	slog.Info("periscope-agent starting",
		"server", cfg.ServerURL,
		"cluster", cfg.ClusterName,
		"namespace", cfg.Namespace,
		"secret", cfg.SecretName)

	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	inClusterCfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("in-cluster config: %w", err)
	}
	kc, err := kubernetes.NewForConfig(inClusterCfg)
	if err != nil {
		return fmt.Errorf("kube client: %w", err)
	}

	state, err := ensureState(ctx, kc, cfg)
	if err != nil {
		return fmt.Errorf("state: %w", err)
	}
	slog.Info("agent identity ready",
		"cert_subject_cn", cfg.ClusterName,
		"server_ca_bytes", len(state.ServerCAPEM))

	tlsCfg, err := buildTLSConfig(state)
	if err != nil {
		return fmt.Errorf("tls: %w", err)
	}

	// Local apiserver dialer — every server-requested dial routes
	// here. We ignore the requested address (it's the
	// "apiserver.<cluster>.tunnel" sentinel) and always connect to
	// the kubelet-published apiserver host.
	apiHost := apiserverHost(inClusterCfg)
	localDial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, apiHost)
	}

	client, err := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL:       cfg.ServerURL,
		ClientName:      cfg.ClusterName,
		TLSClientConfig: tlsCfg,
		LocalDial:       localDial,
		Headers:         http.Header{"X-Agent-Name": []string{cfg.ClusterName}},
	})
	if err != nil {
		return fmt.Errorf("tunnel client: %w", err)
	}

	go serveHealth(ctx, cfg.HealthAddr)

	slog.Info("opening tunnel", "server", cfg.ServerURL, "apiserver_host", apiHost)
	return client.Run(ctx)
}

// agentConfig captures all knobs (env-driven).
type agentConfig struct {
	ServerURL         string
	ClusterName       string
	RegistrationToken string
	Namespace         string
	SecretName        string
	HealthAddr        string

	// RegistrationURL overrides the URL used for the unauth
	// registration POST (#48). Defaults to ServerURL with wss/ws
	// translated to https/http. Set explicitly when the server
	// hosts the registration endpoint behind a different load
	// balancer than the tunnel listener (e.g. ALB for HTTPS API +
	// NLB for mTLS tunnel).
	RegistrationURL string

	// ServerCAHash, if set, enables SPKI-hash pinning on the
	// registration TLS dial (kubeadm-style). Format: "sha256:<64
	// hex chars>". Bypasses standard CA-chain validation; use
	// when the registration endpoint presents a self-signed cert
	// the agent has no prior trust anchor for. After registration
	// the agent has the real CA bundle and uses it for the
	// long-lived tunnel — the pin is only for the bootstrap dial.
	ServerCAHash string
}

func loadAgentConfig() (agentConfig, error) {
	cfg := agentConfig{
		ServerURL:         strings.TrimSpace(os.Getenv("PERISCOPE_SERVER_URL")),
		ClusterName:       strings.TrimSpace(os.Getenv("PERISCOPE_CLUSTER_NAME")),
		RegistrationToken: strings.TrimSpace(os.Getenv("PERISCOPE_REGISTRATION_TOKEN")),
		Namespace:         strings.TrimSpace(os.Getenv("PERISCOPE_AGENT_NAMESPACE")),
		SecretName:        strings.TrimSpace(os.Getenv("PERISCOPE_AGENT_SECRET_NAME")),
		HealthAddr:        strings.TrimSpace(os.Getenv("PERISCOPE_AGENT_HEALTH_ADDR")),
		RegistrationURL:   strings.TrimSpace(os.Getenv("PERISCOPE_REGISTRATION_URL")),
		ServerCAHash:      strings.TrimSpace(os.Getenv("PERISCOPE_SERVER_CA_HASH")),
	}
	if cfg.ServerURL == "" {
		return cfg, errors.New("PERISCOPE_SERVER_URL required")
	}
	if cfg.ClusterName == "" {
		return cfg, errors.New("PERISCOPE_CLUSTER_NAME required")
	}
	if cfg.SecretName == "" {
		cfg.SecretName = "periscope-agent-state"
	}
	if cfg.HealthAddr == "" {
		cfg.HealthAddr = ":8081"
	}
	if cfg.Namespace == "" {
		ns, err := os.ReadFile(saNamespacePath)
		if err != nil {
			return cfg, fmt.Errorf("read in-pod namespace: %w", err)
		}
		cfg.Namespace = strings.TrimSpace(string(ns))
	}
	return cfg, nil
}

// agentState is the persistable identity tuple.
type agentState struct {
	ClientCertPEM []byte
	ClientKeyPEM  []byte
	ServerCAPEM   []byte
}

// ensureState loads the agent's state from its Secret. If the Secret
// doesn't exist (first boot), runs the registration flow with the
// bootstrap token and writes the result.
//
// Refusing to overwrite an existing Secret is intentional: if an
// operator accidentally re-runs the agent with a leftover token in
// the env, we don't want to silently re-register and burn the
// previous identity.
func ensureState(ctx context.Context, kc kubernetes.Interface, cfg agentConfig) (*agentState, error) {
	sec, err := kc.CoreV1().Secrets(cfg.Namespace).
		Get(ctx, cfg.SecretName, metav1.GetOptions{})
	if err == nil {
		slog.Info("loaded existing agent state from Secret",
			"namespace", cfg.Namespace, "name", cfg.SecretName)
		return &agentState{
			ClientCertPEM: sec.Data[secretKeyClientCert],
			ClientKeyPEM:  sec.Data[secretKeyClientKey],
			ServerCAPEM:   sec.Data[secretKeyServerCA],
		}, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("get agent secret: %w", err)
	}

	if cfg.RegistrationToken == "" {
		return nil, errors.New("no agent state Secret found and PERISCOPE_REGISTRATION_TOKEN is empty — first boot needs a bootstrap token")
	}
	slog.Info("first boot: registering with central server",
		"server", cfg.ServerURL)

	state, err := registerAndSign(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("registration: %w", err)
	}

	if _, err := kc.CoreV1().Secrets(cfg.Namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cfg.SecretName,
			Namespace: cfg.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "periscope-agent",
				"app.kubernetes.io/managed-by": "periscope-agent",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			secretKeyClientCert: state.ClientCertPEM,
			secretKeyClientKey:  state.ClientKeyPEM,
			secretKeyServerCA:   state.ServerCAPEM,
		},
	}, metav1.CreateOptions{}); err != nil {
		return nil, fmt.Errorf("persist agent state: %w", err)
	}
	slog.Info("agent registration complete; state persisted",
		"namespace", cfg.Namespace, "name", cfg.SecretName)

	return state, nil
}

// buildTLSConfig wires the persisted cert + CA into a *tls.Config the
// agent presents on every WebSocket reconnect.
func buildTLSConfig(state *agentState) (*tls.Config, error) {
	if len(state.ClientCertPEM) == 0 || len(state.ClientKeyPEM) == 0 {
		return nil, errors.New("agent state missing client cert or key")
	}
	pair, err := tls.X509KeyPair(state.ClientCertPEM, state.ClientKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("parse client cert/key: %w", err)
	}
	if len(state.ServerCAPEM) == 0 {
		return nil, errors.New("agent state missing server CA")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(state.ServerCAPEM) {
		return nil, errors.New("server CA PEM did not parse into the trust pool")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{pair},
		RootCAs:      pool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// apiserverHost returns "host:port" the agent should dial to reach
// its local apiserver. Pulled from in-cluster config so we don't
// hardcode kubernetes.default.svc — works on kind, EKS, GKE, etc.
func apiserverHost(cfg *rest.Config) string {
	host := strings.TrimPrefix(cfg.Host, "https://")
	host = strings.TrimPrefix(host, "http://")
	if !strings.Contains(host, ":") {
		host += ":443"
	}
	return host
}

// serveHealth runs a tiny readiness/liveness probe handler. Reports
// 200 once the agent is past bootstrap; the tunnel may still be
// (re)connecting and that's fine — operationally the pod is "alive."
func serveHealth(ctx context.Context, addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Warn("health server exited", "err", err)
	}
}
