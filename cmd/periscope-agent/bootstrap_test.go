package main

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
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

// TestRegisterAndSign_UsesRegistrationURLOverServerURL proves that
// when both URLs are set, the registration POST goes to
// RegistrationURL (the ALB+NLB topology fix from #48). We stand up
// two servers; the agent should hit the registration one and ignore
// the tunnel one.
func TestRegisterAndSign_UsesRegistrationURLOverServerURL(t *testing.T) {
	store := tunnel.NewTokenStore(tunnel.TokenStoreOptions{ReapInterval: -1})
	ca, _, _ := tunnel.GenerateCA("test-ca", tunnel.CertValidity{})

	// Real registration server (mounts the handler).
	regMux := http.NewServeMux()
	regMux.HandleFunc("/api/agents/register", tunnel.RegisterHandler(store, ca, 0))
	regSrv := httptest.NewServer(regMux)
	defer regSrv.Close()

	// "Tunnel" server that intentionally fails — if the agent ever
	// hits this URL for registration the test fails fast.
	tunnelSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("agent posted registration to ServerURL %q (path %q); should have used RegistrationURL", r.Host, r.URL.Path)
		http.Error(w, "wrong endpoint", http.StatusTeapot)
	}))
	defer tunnelSrv.Close()

	iss, err := store.MintToken("prod-eu")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}

	cfg := agentConfig{
		ServerURL:         tunnelSrv.URL, // would be wrong
		RegistrationURL:   regSrv.URL,    // expected
		ClusterName:       "prod-eu",
		RegistrationToken: iss.Token,
	}
	if _, err := registerAndSign(context.Background(), cfg); err != nil {
		t.Fatalf("registerAndSign: %v", err)
	}
}

// TestRegisterAndSign_FallsBackToServerURL confirms backward
// compatibility — when only ServerURL is set, registration uses it.
func TestRegisterAndSign_FallsBackToServerURL(t *testing.T) {
	store := tunnel.NewTokenStore(tunnel.TokenStoreOptions{ReapInterval: -1})
	ca, _, _ := tunnel.GenerateCA("test-ca", tunnel.CertValidity{})

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents/register", tunnel.RegisterHandler(store, ca, 0))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	iss, _ := store.MintToken("prod-eu")
	cfg := agentConfig{
		ServerURL:         srv.URL, // RegistrationURL deliberately unset
		ClusterName:       "prod-eu",
		RegistrationToken: iss.Token,
	}
	if _, err := registerAndSign(context.Background(), cfg); err != nil {
		t.Fatalf("registerAndSign: %v", err)
	}
}

// TestRegisterAndSign_SPKIPinning proves the agent succeeds against
// a self-signed registration endpoint when a correct hash is
// supplied, and fails clearly with a wrong one.
func TestRegisterAndSign_SPKIPinning(t *testing.T) {
	store := tunnel.NewTokenStore(tunnel.TokenStoreOptions{ReapInterval: -1})
	ca, _, _ := tunnel.GenerateCA("test-ca", tunnel.CertValidity{})

	// httptest.NewTLSServer uses a self-signed cert — exactly the
	// shape SPKI pinning is meant for.
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents/register", tunnel.RegisterHandler(store, ca, 0))
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	leaf := srv.Certificate()
	sum := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	correctHash := "sha256:" + hex.EncodeToString(sum[:])

	t.Run("matching pin", func(t *testing.T) {
		iss, _ := store.MintToken("c")
		cfg := agentConfig{
			ServerURL:         srv.URL,
			ClusterName:       "c",
			RegistrationToken: iss.Token,
			ServerCAHash:      correctHash,
		}
		if _, err := registerAndSign(context.Background(), cfg); err != nil {
			t.Fatalf("registerAndSign with matching pin: %v", err)
		}
	})

	t.Run("mismatched pin", func(t *testing.T) {
		iss, _ := store.MintToken("c2")
		cfg := agentConfig{
			ServerURL:         srv.URL,
			ClusterName:       "c2",
			RegistrationToken: iss.Token,
			ServerCAHash:      "sha256:" + strings.Repeat("0", 64),
		}
		if _, err := registerAndSign(context.Background(), cfg); err == nil {
			t.Fatal("registerAndSign should have failed with wrong pin")
		} else if !strings.Contains(err.Error(), "SPKI pin mismatch") {
			t.Fatalf("err = %v, want SPKI pin mismatch", err)
		}
	})

	t.Run("malformed pin", func(t *testing.T) {
		iss, _ := store.MintToken("c3")
		cfg := agentConfig{
			ServerURL:         srv.URL,
			ClusterName:       "c3",
			RegistrationToken: iss.Token,
			ServerCAHash:      "garbage",
		}
		if _, err := registerAndSign(context.Background(), cfg); err == nil {
			t.Fatal("registerAndSign should have failed with malformed pin")
		}
	})
}
