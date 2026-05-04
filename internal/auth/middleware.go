package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/authz"
)

// SessionFromContext returns the user's Identity attached by Middleware
// or DevMiddleware. Convenience wrapper around credentials.SessionFromContext
// that returns the auth-package shape.
func SessionFromContext(ctx context.Context) (Identity, bool) {
	s := credentials.SessionFromContext(ctx)
	if s.Subject == "" || s.Subject == "anonymous" {
		return Identity{}, false
	}
	return Identity{
		Subject: s.Subject,
		Email:   s.Email,
		Groups:  s.Groups,
	}, true
}

func plant(ctx context.Context, id Identity) context.Context {
	return credentials.WithSession(ctx, credentials.Session{
		Subject: id.Subject,
		Email:   id.Email,
		Groups:  append([]string(nil), id.Groups...),
	})
}

// publicPaths are bypassed by auth middleware — they implement auth
// itself or are health-only. Logout endpoints are public so that
// expired sessions can still clear their cookies cleanly.
func isPublic(path string) bool {
	switch path {
	case "/healthz",
		"/api/auth/config",
		"/api/auth/login",
		"/api/auth/callback",
		"/api/auth/loggedout",
		"/api/auth/logout",
		"/api/auth/logout/everywhere":
		return true
	}
	return false
}

// Middleware returns the chi middleware that:
//  1. reads the session cookie,
//  2. looks up the Session record,
//  3. enforces idle / absolute timeouts,
//  4. silently refreshes the access token if within 60s of expiry,
//  5. attaches the Identity to the request context.
//
// Public paths (login, callback, healthz, logout) bypass the check.
// Other paths with no/invalid/expired session get 401.
func Middleware(client *OIDCClient, store SessionStore, cfg Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			c, err := r.Cookie(cfg.Session.CookieName)
			if err != nil || c.Value == "" {
				unauthorized(w, r)
				return
			}
			s, ok := store.Get(c.Value)
			if !ok {
				unauthorized(w, r)
				return
			}
			now := time.Now()
			if now.After(s.AbsoluteExpiry) {
				_ = store.Delete(s.ID)
				slog.InfoContext(r.Context(), "auth.session_expired",
					"subject", s.Subject, "kind", "absolute")
				unauthorized(w, r)
				return
			}
			if cfg.Session.IdleTimeout > 0 && now.Sub(s.LastActivity) > cfg.Session.IdleTimeout {
				_ = store.Delete(s.ID)
				slog.InfoContext(r.Context(), "auth.session_expired",
					"subject", s.Subject, "kind", "idle")
				unauthorized(w, r)
				return
			}

			// Refresh access token if near expiry. Skip when there's
			// no refresh token (e.g. operator chose not to request
			// the offline_access scope).
			if client != nil && s.RefreshToken != "" && time.Until(s.AccessExpiry) < 60*time.Second {
				refreshed, err := client.Refresh(r.Context(), s)
				if err != nil {
					_ = store.Delete(s.ID)
					slog.WarnContext(r.Context(), "auth.session_expired",
						"subject", s.Subject, "kind", "refresh_failed", "err", err)
					unauthorized(w, r)
					return
				}
				s = refreshed
			}

			s.LastActivity = now
			_ = store.Update(s)

			next.ServeHTTP(w, r.WithContext(plant(r.Context(), s.ToIdentity())))
		})
	}
}

// DevMiddleware injects a fixed dev session on every non-public
// request. Used when ModeDev is active: no cookie work, no OIDC, just
// a stable Identity downstream so the credentials Provider sees a real
// actor.
func DevMiddleware(cfg Config) func(http.Handler) http.Handler {
	id := Identity{
		Subject: cfg.Dev.Subject,
		Email:   cfg.Dev.Email,
		Groups:  append([]string(nil), cfg.Dev.Groups...),
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isPublic(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(plant(r.Context(), id)))
		})
	}
}

// unauthorized writes the standard 401 for API/XHR clients but
// redirects browser navigations to the login endpoint so users
// get the OIDC flow instead of a plain-text dead-end. The SPA
// fetches with `Accept: application/json`, so checking for
// text/html in Accept reliably distinguishes the two.
func unauthorized(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && acceptsHTML(r) {
		http.Redirect(w, r, "/api/auth/login", http.StatusFound)
		return
	}
	w.Header().Set("WWW-Authenticate", `Cookie realm="periscope"`)
	http.Error(w, "unauthenticated", http.StatusUnauthorized)
}

// acceptsHTML returns true when the client signaled a preference
// for an HTML response — i.e. a browser top-level navigation.
// Tolerant of the q-value form (e.g. `text/html;q=0.9`).
func acceptsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if accept == "" {
		return false
	}
	for _, part := range strings.Split(accept, ",") {
		media := strings.TrimSpace(part)
		if i := strings.Index(media, ";"); i >= 0 {
			media = media[:i]
		}
		if strings.EqualFold(strings.TrimSpace(media), "text/html") {
			return true
		}
	}
	return false
}
// SessionValid reports whether the request's session cookie still
// resolves to a non-expired, non-idle session. Side-effect-free: does
// not update LastActivity, refresh tokens, or hit the OIDC provider.
//
// Returns true unconditionally in non-OIDC modes (dev) — there is no
// session record to expire.
//
// Used by long-lived handlers (e.g. SSE watch streams) to detect
// expiry mid-stream so they can emit a graceful close event instead
// of waiting for the next user-initiated request to fail. Other
// concurrent requests through Middleware continue to bump LastActivity
// normally, so an active user's stream stays alive even if the stream
// itself isn't ticking activity.
func SessionValid(r *http.Request, store SessionStore, cfg Config) bool {
	if cfg.Mode != ModeOIDC {
		return true
	}
	c, err := r.Cookie(cfg.Session.CookieName)
	if err != nil || c.Value == "" {
		return false
	}
	s, ok := store.Get(c.Value)
	if !ok {
		return false
	}
	now := time.Now()
	if now.After(s.AbsoluteExpiry) {
		return false
	}
	if cfg.Session.IdleTimeout > 0 && now.Sub(s.LastActivity) > cfg.Session.IdleTimeout {
		return false
	}
	return true
}

// AcceptHTML reports whether the request looks like a browser
// navigation. Useful for handlers that want to redirect humans to
// /auth/login but return 401 to fetches.
func AcceptHTML(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// RegisterRoutes mounts every /api/auth/* endpoint on the chi router.
// Caller is responsible for mounting the auth middleware before
// registering app routes; auth's own routes bypass the middleware via
// isPublic().
//
// In dev mode, the okta-only routes (login, callback, logout/everywhere)
// short-circuit with a 200 / redirect so the SPA can call them
// uniformly without checking mode.
func RegisterRoutes(r chi.Router, client *OIDCClient, store SessionStore, cfg Config, resolver *authz.Resolver, auditEnabled bool) {
	r.Get("/api/auth/config", ConfigHandler(cfg))
	r.Get("/api/auth/login", LoginHandler(client, cfg))
	r.Get("/api/auth/callback", CallbackHandler(client, store, cfg))
	r.Get("/api/auth/loggedout", LoggedOutHandler())
	r.Get("/api/auth/logout", LogoutHandler(store, cfg))
	r.Get("/api/auth/logout/everywhere", LogoutEverywhereHandler(client, store, cfg))
	r.Get("/api/auth/whoami", WhoamiHandler(store, cfg, resolver, auditEnabled))
}
