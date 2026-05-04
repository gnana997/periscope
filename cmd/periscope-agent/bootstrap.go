package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gnana997/periscope/internal/tunnel"
)

// registerAndSign generates a fresh ECDSA P-256 keypair, builds a
// CSR with CN=<cluster>, POSTs the bootstrap token + CSR to the
// central server's /api/agents/register endpoint, and returns the
// signed cert + server CA bundle.
//
// The keypair is generated locally and the private key never leaves
// the agent's process — only the CSR (containing the public key)
// crosses the wire. After this call returns, the caller (ensureState)
// is responsible for persisting the cert + key + CA bundle into the
// agent's K8s Secret.
//
// Server URL handling: registration is over HTTPS (not WebSocket).
// We translate ws:// → http:// and wss:// → https:// so the operator
// only has to set one URL in the Helm values.
func registerAndSign(ctx context.Context, cfg agentConfig) (*agentState, error) {
	// 1. Generate keypair.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("ecdsa keygen: %w", err)
	}

	// 2. Build CSR. CN here is informational — the server overwrites
	//    it at signing time with the cluster name claimed in the
	//    request body. Setting it for log readability nonetheless.
	csrTmpl := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cfg.ClusterName, Organization: []string{"periscope-agent"}},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTmpl, key)
	if err != nil {
		return nil, fmt.Errorf("create CSR: %w", err)
	}

	// 3. POST to /api/agents/register.
	registerURL, err := registerEndpoint(cfg.ServerURL)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(tunnel.RegisterRequest{
		Token:   cfg.RegistrationToken,
		Cluster: cfg.ClusterName,
		CSR:     base64.StdEncoding.EncodeToString(csrDER),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build register request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		// On first boot we don't yet know the server's CA — fall
		// back to system roots, which works in the common case where
		// the operator put a public-CA cert on the ALB / ingress.
		// For self-signed central-server scenarios we'd need a
		// CABundle Helm value to seed; tracked as a follow-up.
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST register: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, fmt.Errorf("read register response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("register %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
	}

	var reg tunnel.RegisterResponse
	if err := json.Unmarshal(respBody, &reg); err != nil {
		return nil, fmt.Errorf("decode register response: %w", err)
	}
	if reg.Cert == "" || reg.CABundle == "" {
		return nil, errors.New("register response missing cert or CABundle")
	}

	// 4. Marshal the local private key to PEM for persistence.
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal client key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return &agentState{
		ClientCertPEM: []byte(reg.Cert),
		ClientKeyPEM:  keyPEM,
		ServerCAPEM:   []byte(reg.CABundle),
	}, nil
}

// registerEndpoint builds the full registration URL from the operator-
// supplied PERISCOPE_SERVER_URL. Accepts ws://, wss://, http://, https://.
func registerEndpoint(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", fmt.Errorf("parse server URL: %w", err)
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	case "ws":
		u.Scheme = "http"
	case "http", "https":
		// already correct
	default:
		return "", fmt.Errorf("server URL: unexpected scheme %q (want ws/wss/http/https)", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/api/agents/register"
	return u.String(), nil
}
