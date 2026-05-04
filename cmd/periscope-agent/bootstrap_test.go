package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gnana997/periscope/internal/tunnel"
)

// TestRegisterAndSign_HappyPath proves the agent boot dance:
// generate keypair → CSR → POST → cert. Spins up a real
// tunnel.RegisterHandler in front of a real CA so the cert that
// comes back is genuinely signed by the trust anchor.
func TestRegisterAndSign_HappyPath(t *testing.T) {
	store := tunnel.NewTokenStore(tunnel.TokenStoreOptions{ReapInterval: -1})
	ca, _, err := tunnel.GenerateCA("test-ca", tunnel.CertValidity{})
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents/register", tunnel.RegisterHandler(store, ca, 0))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Mint a token bound to the cluster name we'll claim below.
	iss, err := store.MintToken("prod-eu")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	cfg := agentConfig{
		ServerURL:         srv.URL, // http://… is also accepted
		ClusterName:       "prod-eu",
		RegistrationToken: iss.Token,
	}
	state, err := registerAndSign(context.Background(), cfg)
	if err != nil {
		t.Fatalf("registerAndSign: %v", err)
	}

	// Cert should parse and have CN = cluster name.
	block, _ := pem.Decode(state.ClientCertPEM)
	if block == nil {
		t.Fatal("returned cert is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse returned cert: %v", err)
	}
	if cert.Subject.CommonName != "prod-eu" {
		t.Fatalf("CN = %q, want prod-eu", cert.Subject.CommonName)
	}

	// CA bundle in the response should chain-verify the cert.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(state.ServerCAPEM) {
		t.Fatal("server CA bundle did not parse")
	}
	if _, err := cert.Verify(x509.VerifyOptions{
		Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		t.Fatalf("returned cert chain failed verification: %v", err)
	}

	// Private key should be a non-empty EC key PEM.
	keyBlock, _ := pem.Decode(state.ClientKeyPEM)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		t.Fatal("returned key is not EC PRIVATE KEY PEM")
	}
}

func TestRegisterAndSign_TokenRejected(t *testing.T) {
	store := tunnel.NewTokenStore(tunnel.TokenStoreOptions{ReapInterval: -1})
	ca, _, _ := tunnel.GenerateCA("test-ca", tunnel.CertValidity{})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents/register", tunnel.RegisterHandler(store, ca, 0))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := agentConfig{
		ServerURL:         srv.URL,
		ClusterName:       "no-such",
		RegistrationToken: "definitely-not-minted",
	}
	if _, err := registerAndSign(context.Background(), cfg); err == nil {
		t.Fatal("registerAndSign accepted unknown token")
	} else if !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want a 401-shaped error", err)
	}
}

func TestRegisterEndpoint_SchemeMapping(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://periscope.example.com", "https://periscope.example.com/api/agents/register"},
		{"http://localhost:8088", "http://localhost:8088/api/agents/register"},
		{"wss://periscope.example.com/", "https://periscope.example.com/api/agents/register"},
		{"ws://localhost:8088", "http://localhost:8088/api/agents/register"},
	}
	for _, tc := range cases {
		got, err := registerEndpoint(tc.in)
		if err != nil {
			t.Fatalf("registerEndpoint(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("registerEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRegisterEndpoint_RejectsBadScheme(t *testing.T) {
	if _, err := registerEndpoint("ftp://nope"); err == nil {
		t.Fatal("registerEndpoint accepted ftp:// scheme")
	}
}
