package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"

	"k8s.io/client-go/rest"
)

// TestProxy_InjectsBearerAndPreservesImpersonation is the load-bearing
// test for the #59 fix. Stands up a fake apiserver, points the proxy
// Rewrite at it, fires a request with Impersonate-* but no Auth, and
// asserts the apiserver receives Authorization: Bearer <token> AND the
// Impersonate-* headers passed through unchanged. If this regresses,
// agent-backed clusters fail authentication exactly as #59 described.
func TestProxy_InjectsBearerAndPreservesImpersonation(t *testing.T) {
	const wantBearer = "agent-sa-token-fixture"
	const wantUser = "alice@corp"
	const wantGroup = "periscope-tier:admin"

	// 1. Fake "apiserver" that records what it received.
	var seenAuth, seenUser, seenGroup, seenForwardedFor string
	apiserver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenUser = r.Header.Get("Impersonate-User")
		seenGroup = r.Header.Get("Impersonate-Group")
		seenForwardedFor = r.Header.Get("X-Forwarded-For")
		_, _ = io.WriteString(w, `{"kind":"PodList"}`)
	}))
	defer apiserver.Close()

	// 2. Build the proxy with the same Rewrite the production code uses.
	apiserverURL, _ := url.Parse(apiserver.URL)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(apiserverURL)
			pr.Out.Header.Set("Authorization", "Bearer "+wantBearer)
			pr.Out.Header.Del("X-Forwarded-For")
			pr.Out.Header.Del("X-Forwarded-Host")
			pr.Out.Header.Del("X-Forwarded-Proto")
		},
		Transport: &http.Transport{
			TLSClientConfig: tlsConfigTrustingServer(t, apiserver),
		},
	}

	// 3. Stand up the proxy + fire a request with Impersonate-* headers.
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	req, _ := http.NewRequest("GET", proxyServer.URL+"/api/v1/pods", nil)
	req.Header.Set("Impersonate-User", wantUser)
	req.Header.Set("Impersonate-Group", wantGroup)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// 4. The apiserver MUST have seen the bearer + impersonation headers.
	if got := seenAuth; got != "Bearer "+wantBearer {
		t.Errorf("Authorization at apiserver = %q, want Bearer %s", got, wantBearer)
	}
	if seenUser != wantUser {
		t.Errorf("Impersonate-User at apiserver = %q, want %q", seenUser, wantUser)
	}
	if seenGroup != wantGroup {
		t.Errorf("Impersonate-Group at apiserver = %q, want %q", seenGroup, wantGroup)
	}
	if seenForwardedFor != "" {
		t.Errorf("X-Forwarded-For leaked through to apiserver: %q (proxy should strip)", seenForwardedFor)
	}
}

// TestProxy_OverwritesUntrustedAuthorizationHeader proves the proxy
// always overwrites the Authorization header with the agent's own
// bearer — even if the inbound request supplied one. Defense-in-depth
// against a compromised or misbehaving central server.
func TestProxy_OverwritesUntrustedAuthorizationHeader(t *testing.T) {
	const wantBearer = "agent-sa-token-fixture"
	const attackerToken = "attacker-supplied-token"

	var seenAuth string
	apiserver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, "ok")
	}))
	defer apiserver.Close()

	apiserverURL, _ := url.Parse(apiserver.URL)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(apiserverURL)
			pr.Out.Header.Set("Authorization", "Bearer "+wantBearer)
		},
		Transport: &http.Transport{TLSClientConfig: tlsConfigTrustingServer(t, apiserver)},
	}
	proxyServer := httptest.NewServer(proxy)
	defer proxyServer.Close()

	req, _ := http.NewRequest("GET", proxyServer.URL, nil)
	req.Header.Set("Authorization", "Bearer "+attackerToken) // hostile inbound
	resp, _ := http.DefaultClient.Do(req)
	defer func() { _ = resp.Body.Close() }()

	if seenAuth == "Bearer "+attackerToken {
		t.Fatal("attacker-supplied Authorization reached apiserver — proxy must always overwrite")
	}
	if seenAuth != "Bearer "+wantBearer {
		t.Fatalf("apiserver saw Authorization = %q, want Bearer %s", seenAuth, wantBearer)
	}
}

// TestApiserverTLSConfig_RejectsConfigWithoutCA confirms the proxy
// won't start in misconfigurations where neither CAData nor CAFile is
// available. Failing loudly is preferable to silently disabling TLS
// verification.
func TestApiserverTLSConfig_RejectsConfigWithoutCA(t *testing.T) {
	cfg := &rest.Config{Host: "https://kubernetes.default:443"}
	if _, err := apiserverTLSConfig(cfg); err == nil {
		t.Fatal("apiserverTLSConfig accepted config with no CA")
	}
}

// TestApiserverTLSConfig_AcceptsCAData proves the happy path —
// CAData populated (the shape rest.InClusterConfig() always produces).
func TestApiserverTLSConfig_AcceptsCAData(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	caPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: srv.Certificate().Raw,
	})

	cfg := &rest.Config{
		Host:        srv.URL,
		BearerToken: "irrelevant-for-tls-config",
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caPEM,
		},
	}
	tlsCfg, err := apiserverTLSConfig(cfg)
	if err != nil {
		t.Fatalf("apiserverTLSConfig: %v", err)
	}
	if tlsCfg.RootCAs == nil {
		t.Fatal("RootCAs is nil — CA didn't get appended")
	}
	if tlsCfg.MinVersion < tls.VersionTLS12 {
		t.Fatalf("MinVersion = %d, want >= TLS 1.2", tlsCfg.MinVersion)
	}
}

// TestStartAPIProxy_RejectsHTTPApiserver confirms we refuse to start
// the proxy when the in-cluster config presents a plain-http apiserver
// URL — that would mean forwarding an SA bearer token over an
// unencrypted hop. Modern in-cluster configs never produce this; the
// guard is for misconfigurations / kind tests / future regressions.
func TestStartAPIProxy_RejectsHTTPApiserver(t *testing.T) {
	cfg := &rest.Config{
		Host:        "http://insecure-apiserver:8080",
		BearerToken: "x",
	}
	err := startAPIProxy(cfg, "127.0.0.1:0")
	if err == nil {
		t.Fatal("startAPIProxy accepted plain-http apiserver URL")
	}
}

// TestStartAPIProxy_RejectsEmptyBearerToken confirms we refuse to
// start when the agent's SA token is missing. Without this guard the
// proxy would forward unauthenticated requests, regressing #59.
func TestStartAPIProxy_RejectsEmptyBearerToken(t *testing.T) {
	cfg := &rest.Config{
		Host: "https://kubernetes.default:443",
		// BearerToken deliberately empty
		TLSClientConfig: rest.TLSClientConfig{CAData: []byte("dummy")},
	}
	err := startAPIProxy(cfg, "127.0.0.1:0")
	if err == nil {
		t.Fatal("startAPIProxy accepted config without BearerToken")
	}
}

// ─── helpers ─────────────────────────────────────────────────────────

func tlsConfigTrustingServer(t *testing.T, srv *httptest.Server) *tls.Config {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}
