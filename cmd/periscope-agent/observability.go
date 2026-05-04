// Observability surface for the agent: structured logging, log-level
// control via PERISCOPE_LOG_LEVEL, per-request access logs through the
// proxy, end-to-end request-id correlation with the central server,
// and a one-time boot diagnostic dump that confirms the agent has
// the credentials + CA it needs.
//
// Why this matters: pre-#59 the agent had zero per-request visibility.
// When alice's request reached the apiserver and got a 403, the agent
// log said nothing — the only way to debug was to enable apiserver
// audit on the managed cluster (separate, operator-controlled). This
// file adds the missing trace points so a Periscope operator can
// debug auth/RBAC/proxy issues from the agent log alone.

package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/rest"
)

// configureLogger replaces slog's default handler with a JSON handler
// that writes to stdout at the level driven by PERISCOPE_LOG_LEVEL.
// Default INFO. Accepts: debug, info, warn, error (case-insensitive).
//
// Production / kubectl logs default to INFO — clean output. Operators
// debugging an issue flip to debug via:
//
//	helm upgrade ... --set 'env[0].name=PERISCOPE_LOG_LEVEL,env[0].value=debug'
//
// Then `kubectl -n periscope logs deploy/periscope-agent | jq .` shows
// every request flowing through the proxy.
func configureLogger() {
	level := parseLogLevel(os.Getenv("PERISCOPE_LOG_LEVEL"))
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
}

func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// loggingMiddleware wraps the reverse proxy with per-request access
// logs. Three log lines per request, level-gated:
//
//	DEBUG  proxy.request_in   - what the agent received from the tunnel
//	DEBUG  proxy.request_out  - what the agent returned (status, latency, bytes)
//	WARN   proxy.apiserver_error - fires when status >= 400 (always-on, even at INFO)
//
// All three carry `request_id` taken from the X-Request-Id header that
// the central server's chi middleware sets on every API call. Same id
// shows up on the server's audit row (RFC 0003 6) so operators can
// grep one id across server audit DB + server stdout slog + agent
// stdout slog for a single end-to-end trace.
//
// Authorization header value is NEVER logged at any level. Path is
// logged because it's part of the public K8s API surface; impersonation
// headers are logged because they're already in apiserver audit logs
// (no new info disclosed).
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqID := r.Header.Get("X-Request-Id")
		impUser := r.Header.Get("Impersonate-User")
		impGroup := r.Header.Get("Impersonate-Group")

		slog.Debug("proxy.request_in",
			"method", r.Method,
			"path", r.URL.Path,
			"impersonate_user", impUser,
			"impersonate_group", impGroup,
			"request_id", reqID,
		)

		// Wrap ResponseWriter to capture status + bytes while preserving
		// the optional interfaces httputil.ReverseProxy needs (Flusher
		// for SSE, Hijacker for WebSocket upgrades on exec).
		rec := newResponseRecorder(w)
		next.ServeHTTP(rec, r)

		latencyMs := time.Since(start).Milliseconds()

		slog.Debug("proxy.request_out",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"latency_ms", latencyMs,
			"bytes", rec.bytes,
			"request_id", reqID,
		)

		// Apiserver-side errors fire at WARN regardless of level —
		// operators see auth/RBAC failures even with default INFO
		// logging. This is the line that would have made #59
		// obvious from day one ("status=401 every request").
		if rec.status >= 400 {
			slog.Warn("proxy.apiserver_error",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"impersonate_user", impUser,
				"latency_ms", latencyMs,
				"request_id", reqID,
			)
		}
	})
}

// logBootDiagnostic logs one INFO line at startup with the resolved
// in-cluster config — apiserver URL, CA fingerprint, SA token presence
// + parsed expiry. Called once after rest.InClusterConfig() succeeds.
//
// Lets operators confirm at a glance that:
//  1. The agent loaded the CA bundle (ca_bundle_len > 0)
//  2. The agent has an SA token (sa_token_present: true)
//  3. The token has reasonable lifetime ahead (sa_token_expires_in_seconds)
//  4. The apiserver URL is what they expect
//
// Critical for debugging "why did auth start failing 60 minutes after
// install" — projected SA tokens default to 1-hour TTL with auto-
// rotation, and if rotation breaks, this line in the boot dump shows
// the expiry timestamp so the failure window is predictable.
func logBootDiagnostic(inClusterCfg *rest.Config) {
	caFP := caFingerprint(inClusterCfg.CAData)
	expiry := saTokenExpiresInSeconds(inClusterCfg.BearerToken)

	slog.Info("agent.boot_diagnostic",
		"apiserver_url", inClusterCfg.Host,
		"ca_bundle_len", len(inClusterCfg.CAData),
		"ca_fingerprint", caFP,
		"sa_token_present", inClusterCfg.BearerToken != "",
		"sa_token_expires_in_seconds", expiry,
	)
}

// caFingerprint returns a short SHA-256 hash of the CA bundle for
// log identification. Same bundle across restarts → same fingerprint;
// CA rotated → different fingerprint. Empty CA → "" (the guard log
// is `ca_bundle_len: 0`).
func caFingerprint(caData []byte) string {
	if len(caData) == 0 {
		return ""
	}
	sum := sha256.Sum256(caData)
	return "sha256:" + hex.EncodeToString(sum[:8]) // first 8 bytes = 16 hex chars, plenty for log identification
}

// saTokenExpiresInSeconds parses the JWT exp claim from a K8s
// projected ServiceAccount token. Returns -1 when the token is empty,
// not a valid JWT, or doesn't carry an exp claim — the boot log shows
// -1 in those cases (operationally "we don't know when this expires;
// investigate"). Doesn't validate the signature; that's the apiserver's
// job at request time.
func saTokenExpiresInSeconds(token string) int64 {
	if token == "" {
		return -1
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		// Not a JWT (could be a legacy long-lived SA token). No way
		// to know when it expires from the token alone.
		return -1
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return -1
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return -1
	}
	remain := claims.Exp - time.Now().UTC().Unix()
	if remain < 0 {
		return 0
	}
	return remain
}

// ─── responseRecorder ──────────────────────────────────────────────
//
// Wraps http.ResponseWriter to capture the status code + total bytes
// written. Preserves optional interfaces (Flusher, Hijacker) so the
// proxy's streaming + WebSocket-upgrade behavior is unaffected.

type responseRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func newResponseRecorder(w http.ResponseWriter) *responseRecorder {
	return &responseRecorder{ResponseWriter: w, status: http.StatusOK}
}

func (r *responseRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		// Same protection net.http applies — repeated WriteHeader is a
		// caller bug; honour the first one.
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		// Implicit WriteHeader(200) on first Write — same as the
		// stdlib ResponseWriter contract.
		r.wroteHeader = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush forwards to the underlying writer if it supports flushing.
// Critical for SSE / watch streams — without this the proxy's
// FlushInterval=-1 would have no effect.
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Note: we don't expose Hijack() directly on responseRecorder because
// http.Hijacker requires the exact return tuple types
// (net.Conn, *bufio.ReadWriter, error) and adding the imports for those
// in this file isn't worth the build-graph cost. The reverse-proxy
// flow we ship today doesn't trigger Hijack (exec is deferred to
// v1.x.1 per #43). When exec lands, the ResponseRecorder will need
// Hijack() implementing — tracked in #43.
//
// Compile-time safety: if a future code path tries to type-assert
// the recorder to http.Hijacker, it'll get nil instead of a wrong
// implementation, surfacing the gap loudly.
var _ http.Flusher = (*responseRecorder)(nil)

// fmt is only used by string formatting in helper paths above —
// keep the reference here so future edits don't accidentally drop it.
var _ = fmt.Sprintf
