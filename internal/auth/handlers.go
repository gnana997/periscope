package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"github.com/gnana997/periscope/internal/authz"
)

// loginCookieName is the short-lived cookie that carries state + PKCE
// verifier across the redirect to OIDC and back. Cleared on /callback.
const loginCookieName = "periscope_login"

// LoginHandler kicks off the OIDC flow: generate state + PKCE
// verifier, stash them in a short-lived cookie, redirect to OIDC.
//
// In dev mode this endpoint is unreachable — the dev middleware
// auto-creates a session before the SPA ever asks to log in.
func LoginHandler(client *OIDCClient, cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if client == nil {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		state := NewState()
		verifier := NewPKCEVerifier()

		// Encode state + verifier into a single value so they
		// share a cookie. State is short, verifier is the secret.
		val := state + ":" + verifier

		http.SetCookie(w, &http.Cookie{
			Name:     loginCookieName,
			Value:    val,
			Path:     "/",
			HttpOnly: true,
			Secure:   isHTTPS(r),
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(10 * time.Minute),
		})
		http.Redirect(w, r, client.AuthCodeURL(state, verifier), http.StatusFound)
	}
}

// CallbackHandler completes the OIDC flow and, on success, sets the
// session cookie and 302s back to the SPA root.
func CallbackHandler(client *OIDCClient, store SessionStore, cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if client == nil {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		c, err := r.Cookie(loginCookieName)
		if err != nil {
			slog.WarnContext(r.Context(), "auth.login_failed", "reason", "no_login_cookie")
			http.Error(w, "login session expired — try again", http.StatusBadRequest)
			return
		}
		// One-shot cookie: clear regardless of outcome.
		clearLoginCookie(w, r)

		state, verifier, ok := strings.Cut(c.Value, ":")
		if !ok {
			slog.WarnContext(r.Context(), "auth.login_failed", "reason", "bad_login_cookie")
			http.Error(w, "login attempt invalid — try again", http.StatusBadRequest)
			return
		}

		q := r.URL.Query()
		if q.Get("state") != state {
			slog.WarnContext(r.Context(), "auth.login_failed", "reason", "state_mismatch")
			http.Error(w, "login attempt invalid — try again", http.StatusBadRequest)
			return
		}
		if errCode := q.Get("error"); errCode != "" {
			slog.WarnContext(r.Context(), "auth.login_failed",
				"reason", "okta_error",
				"okta_error", errCode,
				"okta_desc", q.Get("error_description"))
			http.Error(w, "okta declined the login: "+errCode, http.StatusUnauthorized)
			return
		}

		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}

		sessID := NewSessionID()
		s, err := client.Exchange(r.Context(), code, verifier, sessID, cfg.Session.AbsoluteTimeout)
		if err != nil {
			slog.ErrorContext(r.Context(), "auth.login_failed",
				"reason", "code_exchange_failed", "err", err)
			http.Error(w, "couldn't complete login", http.StatusBadGateway)
			return
		}

		if !groupsAllowed(s.Groups, cfg.Authorization.AllowedGroups) {
			slog.WarnContext(r.Context(), "auth.login_failed",
				"reason", "not_in_allowed_groups",
				"subject", s.Subject,
				"email", s.Email,
				"groups", s.Groups)
			http.Error(w, "your account is not in any group that has Periscope access. contact your admin.", http.StatusForbidden)
			return
		}

		if err := store.Create(s); err != nil {
			slog.ErrorContext(r.Context(), "auth.session_create_failed", "err", err)
			http.Error(w, "session error", http.StatusInternalServerError)
			return
		}

		setSessionCookie(w, r, cfg.Session, s.ID, s.AbsoluteExpiry)
		slog.InfoContext(r.Context(), "auth.login",
			"subject", s.Subject, "email", s.Email, "groups", s.Groups)

		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// LogoutHandler clears the session cookie and (if a session exists)
// removes the server-side record. Local logout — OIDC session is left
// alone. Returns 204.
func LogoutHandler(store SessionStore, cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(cfg.Session.CookieName); err == nil {
			if s, ok := store.Get(c.Value); ok {
				_ = store.Delete(c.Value)
				slog.InfoContext(r.Context(), "auth.logout",
					"subject", s.Subject, "kind", "local")
			}
		}
		clearSessionCookie(w, r, cfg.Session)
		http.Redirect(w, r, "/", http.StatusFound)
	}
}

// LogoutEverywhereHandler clears the local session AND redirects the
// browser through OIDC's end_session_endpoint to terminate the IdP
// session as well.
func LogoutEverywhereHandler(client *OIDCClient, store SessionStore, cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idTokenHint := ""
		if c, err := r.Cookie(cfg.Session.CookieName); err == nil {
			if s, ok := store.Get(c.Value); ok {
				idTokenHint = s.IDToken
				_ = store.Delete(c.Value)
				slog.InfoContext(r.Context(), "auth.logout",
					"subject", s.Subject, "kind", "everywhere")
			}
		}
		clearSessionCookie(w, r, cfg.Session)

		if client == nil {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		logoutURL := client.LogoutURL(url.QueryEscape(idTokenHint))
		if logoutURL == "" {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		http.Redirect(w, r, logoutURL, http.StatusFound)
	}
}

// LoggedOutHandler is the static landing page OIDC redirects back to
// after end_session. Returns minimal HTML; the SPA handles richer UI
// when the user navigates to /.
func LoggedOutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!doctype html><meta charset=utf-8><title>Signed out</title><p>You have been signed out. <a href="/">Return to Periscope</a>.</p>`))
	}
}

// WhoamiHandler returns {subject, email, groups, mode, expiresAt} for
// the current session. Used by the SPA's <AuthProvider> on first load.
func WhoamiHandler(store SessionStore, cfg Config, resolver *authz.Resolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, ok := SessionFromContext(r.Context())
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		var expiresAt int64
		if c, err := r.Cookie(cfg.Session.CookieName); err == nil {
			if rec, ok := store.Get(c.Value); ok {
				expiresAt = rec.AbsoluteExpiry.Unix()
			}
		}
		var tier string
		var authzMode string
		if resolver != nil {
			authzMode = string(resolver.Mode())
			tier = resolver.ResolvedTier(authz.Identity{Subject: s.Subject, Groups: s.Groups})
		}
		body := struct {
			Subject    string   `json:"subject"`
			Email      string   `json:"email"`
			Groups     []string `json:"groups"`
			Mode       string   `json:"mode"`
			AuthzMode  string   `json:"authzMode"`
			Tier       string   `json:"tier,omitempty"`
			ExpiresAt  int64    `json:"expiresAt"`
		}{
			Subject:    s.Subject,
			Email:      s.Email,
			Groups:     s.Groups,
			Mode:       string(cfg.Mode),
			AuthzMode:  authzMode,
			Tier:       tier,
			ExpiresAt:  expiresAt,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}

// --- cookie helpers ---

func setSessionCookie(w http.ResponseWriter, r *http.Request, cfg SessionConfig, value string, exp time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     cfg.CookieName,
		Value:    value,
		Path:     "/",
		Domain:   cfg.CookieDomain,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  exp,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request, cfg SessionConfig) {
	http.SetCookie(w, &http.Cookie{
		Name:     cfg.CookieName,
		Value:    "",
		Path:     "/",
		Domain:   cfg.CookieDomain,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

func clearLoginCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     loginCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// isHTTPS reports whether the request appears to be served over HTTPS.
// We honor X-Forwarded-Proto from the trusted reverse proxy chain
// (chi.RealIP runs upstream) so cookies don't break behind TLS-
// terminating ingress.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// groupsAllowed returns true if user has any group in allowed (or if
// allowed is empty — operator hasn't gated yet).
func groupsAllowed(user, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	have := make(map[string]struct{}, len(user))
	for _, g := range user {
		have[g] = struct{}{}
	}
	for _, g := range allowed {
		if _, ok := have[g]; ok {
			return true
		}
	}
	return false
}

// ConfigHandler exposes a small, public configuration slice the SPA
// needs to render the LoginScreen before a session exists. Returns:
//
//	{ "authMode": "dev"|"oidc",
//	  "providerName": "Auth0" }            // when authMode = oidc
//
// Public on purpose: pre-auth pages call this without a cookie.
// Carries no sensitive data — issuer name is already in the redirect
// URL the user is about to be sent through.
func ConfigHandler(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body := struct {
			AuthMode     string `json:"authMode"`
			ProviderName string `json:"providerName,omitempty"`
		}{
			AuthMode: string(cfg.Mode),
		}
		if cfg.Mode == ModeOIDC {
			body.ProviderName = ProviderLabel(cfg.OIDC)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		_ = json.NewEncoder(w).Encode(body)
	}
}
