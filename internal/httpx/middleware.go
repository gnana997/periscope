// Package httpx contains Periscope-specific HTTP middleware that complements
// the chi standard middleware stack. It is stdlib-only (slog) per the
// architectural ground rules.
package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/gnana997/periscope/internal/audit"
)

// AuditBegin plants a per-request audit.RequestContext on the
// request context so downstream handlers and the audit.Emitter can
// pick up the request_id without each handler reconstructing it.
//
// Actor is left empty here — auth runs after this middleware. The
// auth middleware patches Actor onto the same RequestContext via
// audit.PatchActor once the Session is resolved from the cookie, so
// handlers that emit audit events later see the populated Actor.
//
// AuditBegin does not emit a row per request. That can be added
// later if useful; for v1 the row-per-action emissions in handlers
// carry enough information.
//
// It must never carry credentials. Per the ground rules, the
// credential Provider is passed as an explicit function argument
// via credentials.Wrap, not stuffed into context values.
func AuditBegin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, _ := audit.WithRequestContext(r.Context(), audit.RequestContext{
			RequestID: middleware.GetReqID(r.Context()),
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequestID generates a per-request ID, stores it on the request context
// (readable via chi's middleware.GetReqID), and echoes it back to the
// caller as the X-Request-Id response header. The header makes the ID
// visible in browser devtools, curl -D, and reverse-proxy logs, which is
// useful when correlating a stuck pod-exec session with backend slog.
func RequestID(next http.Handler) http.Handler {
	return middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.GetReqID(r.Context()); id != "" {
			w.Header().Set("X-Request-Id", id)
		}
		next.ServeHTTP(w, r)
	}))
}

// RealIP returns chi's RealIP middleware. Same rationale as RequestID.
func RealIP(next http.Handler) http.Handler {
	return middleware.RealIP(next)
}

// Recoverer returns chi's Recoverer middleware. Same rationale as RequestID.
func Recoverer(next http.Handler) http.Handler {
	return middleware.Recoverer(next)
}
