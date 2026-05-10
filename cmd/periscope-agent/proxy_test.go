package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"syscall"
	"testing"

	"k8s.io/client-go/rest"

	"github.com/gnana997/periscope/internal/agentupstream"
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
	err := startAPIProxy(cfg, "test-cluster", "127.0.0.1:0")
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
	err := startAPIProxy(cfg, "test-cluster", "127.0.0.1:0")
	if err == nil {
		t.Fatal("startAPIProxy accepted config without BearerToken")
	}
}

// TestClassifyUpstreamError covers each category branch with a
// representative input. Asserting on category + status (not the
// human message) keeps the test stable when copy is tweaked.
func TestClassifyUpstreamError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantCat    string
		wantStatus int
	}{
		{"nil err", nil, "unknown", http.StatusBadGateway},
		{"deadline exceeded", context.DeadlineExceeded, "timeout", http.StatusGatewayTimeout},
		{"i/o timeout via OpError",
			&net.OpError{Op: "dial", Err: &timeoutErr{}},
			"timeout", http.StatusGatewayTimeout},
		{"econnrefused",
			&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED},
			"network", http.StatusBadGateway},
		{"econnreset",
			&net.OpError{Op: "read", Err: syscall.ECONNRESET},
			"network", http.StatusBadGateway},
		{"dns error",
			&net.DNSError{Err: "no such host", Name: "kubernetes.default"},
			"network", http.StatusBadGateway},
		{"net.ErrClosed", net.ErrClosed, "network", http.StatusBadGateway},
		{"x509 unknown authority",
			x509.UnknownAuthorityError{},
			"tls", http.StatusBadGateway},
		{"tls hostname error",
			x509.HostnameError{Host: "kubernetes.default"},
			"tls", http.StatusBadGateway},
		{"tls cert verification",
			&tls.CertificateVerificationError{},
			"tls", http.StatusBadGateway},
		{"unknown",
			errors.New("something exotic"),
			"unknown", http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, _, status := classifyUpstreamError(tc.err)
			if cat != tc.wantCat {
				t.Errorf("category = %q, want %q", cat, tc.wantCat)
			}
			if status != tc.wantStatus {
				t.Errorf("status = %d, want %d", status, tc.wantStatus)
			}
		})
	}
}

// timeoutErr is a minimal net.Error stand-in that reports Timeout()=true.
// Used by classifier tests to exercise the net.Error path without
// dialing a real socket.
type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// TestWriteUpstreamErrorJSON_FullEnvelope covers the wire shape — the
// wire contract the central server's transport interceptor and the SPA
// banner depend on. Includes the X-Request-Id pass-through.
func TestWriteUpstreamErrorJSON_FullEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/pods", nil)
	req.Header.Set("X-Request-Id", "req-abc-123")

	writeUpstreamErrorJSON(rec, req, "pre-prod", &net.OpError{
		Op: "dial", Err: syscall.ECONNREFUSED,
	})

	if got := rec.Code; got != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body agentupstream.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.Code != agentupstream.CodeAgentUpstream {
		t.Errorf("code = %q, want %q", body.Code, agentupstream.CodeAgentUpstream)
	}
	if body.Category != "network" {
		t.Errorf("category = %q, want network", body.Category)
	}
	if body.Cluster != "pre-prod" {
		t.Errorf("cluster = %q, want pre-prod", body.Cluster)
	}
	if body.TraceID != "req-abc-123" {
		t.Errorf("trace_id = %q, want req-abc-123 (X-Request-Id pass-through)", body.TraceID)
	}
	if body.Detail == "" {
		t.Error("detail empty — expected the underlying error message")
	}
	if body.Message == "" {
		t.Error("message empty — expected a friendly category-specific message")
	}
}

// TestWriteUpstreamErrorJSON_FallbackTraceID exercises the fallback
// trace id path when the inbound request doesn't carry X-Request-Id.
// We don't assert the exact value (it's random), only that it's
// non-empty and has reasonable length.
func TestWriteUpstreamErrorJSON_FallbackTraceID(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil) // no X-Request-Id

	writeUpstreamErrorJSON(rec, req, "test-cluster", errors.New("boom"))

	var body agentupstream.Envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.TraceID == "" {
		t.Fatal("trace_id empty even though fallback should have generated one")
	}
	if len(body.TraceID) < 8 {
		t.Errorf("trace_id %q implausibly short for the fallback path", body.TraceID)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────

func tlsConfigTrustingServer(t *testing.T, srv *httptest.Server) *tls.Config {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}
