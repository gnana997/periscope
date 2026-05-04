package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseLogLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":         slog.LevelInfo,
		"info":     slog.LevelInfo,
		"INFO":     slog.LevelInfo,
		"  Info ":  slog.LevelInfo,
		"debug":    slog.LevelDebug,
		"DEBUG":    slog.LevelDebug,
		"warn":     slog.LevelWarn,
		"warning":  slog.LevelWarn,
		"error":    slog.LevelError,
		"garbage":  slog.LevelInfo, // unknown → safe default
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := parseLogLevel(in); got != want {
				t.Fatalf("parseLogLevel(%q) = %v, want %v", in, got, want)
			}
		})
	}
}

// TestLoggingMiddleware_DebugFiresAccessLogs verifies that at DEBUG
// level, both proxy.request_in and proxy.request_out fire — the trace
// pair operators rely on for debugging.
func TestLoggingMiddleware_DebugFiresAccessLogs(t *testing.T) {
	logBuf := captureLogger(t, slog.LevelDebug)

	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	srv := httptest.NewServer(loggingMiddleware(upstream))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/pods", nil)
	req.Header.Set("X-Request-Id", "test-req-id-abc")
	req.Header.Set("Impersonate-User", "alice@corp")
	req.Header.Set("Impersonate-Group", "periscope-tier:admin")
	resp, _ := http.DefaultClient.Do(req)
	defer func() { _ = resp.Body.Close() }()

	logs := logBuf.String()
	mustContain(t, logs, `"msg":"proxy.request_in"`)
	mustContain(t, logs, `"msg":"proxy.request_out"`)
	mustContain(t, logs, `"request_id":"test-req-id-abc"`)
	mustContain(t, logs, `"impersonate_user":"alice@corp"`)
	mustContain(t, logs, `"status":200`)
}

// TestLoggingMiddleware_InfoSuppressesAccessLogs proves the access
// log pair is debug-gated. INFO operators don't see per-request noise.
func TestLoggingMiddleware_InfoSuppressesAccessLogs(t *testing.T) {
	logBuf := captureLogger(t, slog.LevelInfo)

	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	srv := httptest.NewServer(loggingMiddleware(upstream))
	defer srv.Close()

	resp, _ := http.Get(srv.URL + "/api/v1/pods")
	defer func() { _ = resp.Body.Close() }()

	logs := logBuf.String()
	if strings.Contains(logs, "proxy.request_in") || strings.Contains(logs, "proxy.request_out") {
		t.Fatalf("INFO level should suppress request_in/out; got:\n%s", logs)
	}
}

// TestLoggingMiddleware_AlwaysLogsErrors proves the WARN-level error
// log fires for >= 400 responses regardless of log level. This is the
// log line that would have made #59 obvious from the start.
func TestLoggingMiddleware_AlwaysLogsErrors(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn} {
		t.Run(level.String(), func(t *testing.T) {
			logBuf := captureLogger(t, level)

			upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
			})
			srv := httptest.NewServer(loggingMiddleware(upstream))
			defer srv.Close()

			req, _ := http.NewRequest("GET", srv.URL+"/api/v1/secrets/x/y", nil)
			req.Header.Set("Impersonate-User", "alice@corp")
			req.Header.Set("X-Request-Id", "trace-id-403")
			resp, _ := http.DefaultClient.Do(req)
			defer func() { _ = resp.Body.Close() }()

			logs := logBuf.String()
			mustContain(t, logs, `"msg":"proxy.apiserver_error"`)
			mustContain(t, logs, `"status":403`)
			mustContain(t, logs, `"impersonate_user":"alice@corp"`)
			mustContain(t, logs, `"request_id":"trace-id-403"`)
		})
	}
}

// TestLoggingMiddleware_AuthorizationNeverLogged is the security
// guarantee: even at debug level, the Authorization header value
// must never appear in logs.
func TestLoggingMiddleware_AuthorizationNeverLogged(t *testing.T) {
	const secret = "super-sensitive-bearer-token-fixture"
	logBuf := captureLogger(t, slog.LevelDebug)

	upstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	srv := httptest.NewServer(loggingMiddleware(upstream))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL, nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, _ := http.DefaultClient.Do(req)
	defer func() { _ = resp.Body.Close() }()

	logs := logBuf.String()
	if strings.Contains(logs, secret) {
		t.Fatal("the Authorization bearer token leaked into logs")
	}
}

// TestSATokenExpiresInSeconds covers JWT exp parsing — happy path,
// missing exp, malformed token.
func TestSATokenExpiresInSeconds(t *testing.T) {
	t.Run("future exp", func(t *testing.T) {
		exp := time.Now().Add(2 * time.Hour).Unix()
		token := makeFakeJWT(t, map[string]any{"exp": exp})
		got := saTokenExpiresInSeconds(token)
		if got < 7000 || got > 7300 { // ~7200s ± a small window
			t.Fatalf("expires_in = %d, want ~7200", got)
		}
	})

	t.Run("past exp", func(t *testing.T) {
		exp := time.Now().Add(-1 * time.Hour).Unix()
		token := makeFakeJWT(t, map[string]any{"exp": exp})
		if got := saTokenExpiresInSeconds(token); got != 0 {
			t.Fatalf("expired token: got = %d, want 0", got)
		}
	})

	t.Run("missing exp claim", func(t *testing.T) {
		token := makeFakeJWT(t, map[string]any{"sub": "system:serviceaccount:periscope:periscope-agent"})
		if got := saTokenExpiresInSeconds(token); got != -1 {
			t.Fatalf("no exp claim: got = %d, want -1", got)
		}
	})

	t.Run("not a JWT (legacy long-lived token)", func(t *testing.T) {
		if got := saTokenExpiresInSeconds("legacy-long-lived-token"); got != -1 {
			t.Fatalf("non-JWT: got = %d, want -1", got)
		}
	})

	t.Run("empty token", func(t *testing.T) {
		if got := saTokenExpiresInSeconds(""); got != -1 {
			t.Fatalf("empty: got = %d, want -1", got)
		}
	})
}

func TestCAFingerprint(t *testing.T) {
	if got := caFingerprint(nil); got != "" {
		t.Fatalf("empty input → %q, want empty", got)
	}
	got := caFingerprint([]byte("some CA pem bytes"))
	if !strings.HasPrefix(got, "sha256:") || len(got) != 23 { // "sha256:" + 16 hex
		t.Fatalf("fingerprint = %q, want sha256:<16-hex>", got)
	}
	// Stable across calls
	if got2 := caFingerprint([]byte("some CA pem bytes")); got2 != got {
		t.Fatalf("fingerprint not stable: %q vs %q", got, got2)
	}
}

// ─── helpers ────────────────────────────────────────────────────────

// captureLogger redirects slog default to a buffer for the test, then
// restores it. Returns the buffer for assertion. Test-local — does
// not race with other tests because each captureLogger call sets a
// fresh handler before exercising the SUT.
func captureLogger(t *testing.T, level slog.Level) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: level})))
	return &buf
}

func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("logs missing %q\n--- full logs ---\n%s", needle, haystack)
	}
}

// makeFakeJWT builds a minimal unsigned JWT. The signature is "sig"
// (not validated by saTokenExpiresInSeconds — that function only
// reads the payload claims).
func makeFakeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, _ := json.Marshal(claims)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	sig := base64.RawURLEncoding.EncodeToString([]byte("sig"))
	return header + "." + payload + "." + sig
}
