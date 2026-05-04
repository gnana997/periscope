package tunnel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// HTTP handlers for the agent registration flow. Two endpoints:
//
//   POST /api/agents/tokens   — admin-only; mints a bootstrap token
//                                bound to a cluster name.
//   POST /api/agents/register  — unauthenticated (the bootstrap token
//                                IS the auth); validates the token,
//                                signs the agent's CSR, returns the
//                                client cert + the CA bundle.
//
// Mounting + auth wiring (admin-only on /tokens, unauth on /register)
// lives in cmd/periscope/main.go. This file just owns the shape of
// the request/response and the handler bodies.
//
// Registration sequence:
//   1. Operator (admin tier in the SPA, or a future periscopectl):
//      POST /api/agents/tokens  body: {"cluster": "prod-eu"}
//      → 200 {"token": "...", "cluster": "prod-eu", "expiresAt": "..."}
//   2. Operator runs `helm install periscope-agent ... --set
//      registrationToken=<token>` on the managed cluster.
//   3. Agent boots, generates ECDSA P-256 keypair locally, builds a
//      CSR with CN=<cluster>, POST /api/agents/register
//      body: {"token": "...", "cluster": "prod-eu", "csr": "<b64-DER>"}
//      → 200 {"cert": "<PEM>", "caBundle": "<PEM>"}
//   4. Agent stores the cert + CA in a local Secret, then dials
//      wss://periscope.example.com/api/agents/connect with the cert
//      as its mTLS client cert.

// MintTokenRequest is the body shape for POST /api/agents/tokens.
type MintTokenRequest struct {
	Cluster string `json:"cluster"`
}

// RegisterRequest is the body shape for POST /api/agents/register.
type RegisterRequest struct {
	Token   string `json:"token"`
	Cluster string `json:"cluster"`
	// CSR is the base64-encoded DER form (no PEM wrapping). Agents
	// that prefer to send PEM should base64-encode the DER bytes
	// inside the PEM block; we don't accept full PEM here to keep
	// the parser surface small.
	CSR string `json:"csr"`
}

// RegisterResponse is what the agent receives on success.
type RegisterResponse struct {
	// Cert is the PEM-encoded signed client cert.
	Cert string `json:"cert"`
	// CABundle is the PEM-encoded server CA cert. The agent uses
	// this to validate the central server's TLS cert on every
	// reconnect.
	CABundle string `json:"caBundle"`
	// ExpiresAt is when the client cert expires (informational; the
	// agent should re-register at 2/3 of this lifetime).
	ExpiresAt time.Time `json:"expiresAt"`
}

// MintTokenHandler returns an http.HandlerFunc that mints a bootstrap
// token. The caller (cmd/periscope) wraps this with admin-only
// middleware before mounting.
func MintTokenHandler(store *TokenStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req MintTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}
		req.Cluster = strings.TrimSpace(req.Cluster)
		if !validClusterName(req.Cluster) {
			http.Error(w, "cluster: must be 1..63 chars, alphanumeric + dashes",
				http.StatusBadRequest)
			return
		}

		issuance, err := store.MintToken(req.Cluster)
		if err != nil {
			slog.ErrorContext(r.Context(), "tunnel.mint_token_failed",
				"cluster", req.Cluster, "err", err)
			http.Error(w, "mint failed", http.StatusInternalServerError)
			return
		}
		slog.InfoContext(r.Context(), "tunnel.token_minted",
			"cluster", req.Cluster, "expires_at", issuance.ExpiresAt.Format(time.RFC3339))

		writeJSON(w, http.StatusOK, issuance)
	}
}

// RegisterHandler returns an http.HandlerFunc that performs the
// registration step: validates the bootstrap token, signs the agent's
// CSR, returns the client cert + CA bundle.
//
// Mounted UNAUTHENTICATED — the bootstrap token IS the proof of
// authorization. The handler is rate-limit friendly (cmd/periscope can
// add chi middleware) and logs every attempt with outcome for forensics.
//
// validityOverride lets cmd/periscope inject a different per-cert
// lifetime via config; zero means use the default (90 days).
func RegisterHandler(store *TokenStore, ca *CA, validityOverride time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("decode: %v", err), http.StatusBadRequest)
			return
		}
		if req.Token == "" || req.Cluster == "" || req.CSR == "" {
			http.Error(w, "token, cluster, csr are all required",
				http.StatusBadRequest)
			return
		}

		csrDER, err := base64.StdEncoding.DecodeString(req.CSR)
		if err != nil {
			http.Error(w, fmt.Sprintf("csr base64 decode: %v", err),
				http.StatusBadRequest)
			return
		}

		// Redeem the token first — failing here means we never sign,
		// so no leaked cert if the CSR is bogus.
		cluster, err := store.RedeemToken(req.Token, req.Cluster)
		if err != nil {
			slog.WarnContext(r.Context(), "tunnel.register_token_rejected",
				"cluster", req.Cluster, "err", err.Error())
			http.Error(w, registerErrMessage(err), registerErrStatus(err))
			return
		}

		validity := validityOverride
		if validity == 0 {
			validity = CertValidity{}.withDefaults().Client
		}
		certPEM, err := ca.SignClient(csrDER, cluster, validity)
		if err != nil {
			slog.ErrorContext(r.Context(), "tunnel.register_sign_failed",
				"cluster", cluster, "err", err)
			http.Error(w, "sign failed", http.StatusInternalServerError)
			return
		}

		expiresAt := time.Now().UTC().Add(validity)
		slog.InfoContext(r.Context(), "tunnel.agent_registered",
			"cluster", cluster, "cert_expires_at", expiresAt.Format(time.RFC3339))

		writeJSON(w, http.StatusOK, RegisterResponse{
			Cert:      string(certPEM),
			CABundle:  string(ca.CertPEM()),
			ExpiresAt: expiresAt,
		})
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// registerErrStatus maps token-redeem errors to HTTP status codes.
// All token failures collapse to 401: an attacker probing the
// endpoint shouldn't be able to distinguish "valid format but
// expired" from "garbage" — that leaks more than it helps.
func registerErrStatus(err error) int {
	switch {
	case errors.Is(err, ErrTokenInvalid),
		errors.Is(err, ErrTokenConsumed),
		errors.Is(err, ErrTokenExpired),
		errors.Is(err, ErrTokenClusterMismatch):
		return http.StatusUnauthorized
	}
	return http.StatusInternalServerError
}

func registerErrMessage(err error) string {
	// Same "single message" treatment as the status mapping — we
	// log the real reason, the agent gets a uniform string.
	return "registration rejected"
}

// validClusterName enforces a small DNS-1123-ish name shape so
// CN-as-cluster-name doesn't surprise the rest of the registry.
func validClusterName(name string) bool {
	if l := len(name); l == 0 || l > 63 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
			// no leading/trailing dash, no consecutive dashes
			if i == 0 || i == len(name)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
