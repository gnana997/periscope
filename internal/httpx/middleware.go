// Package httpx contains Periscope-specific HTTP middleware that complements
// the chi standard middleware stack. It is stdlib-only (slog) per the
// architectural ground rules.
package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// AuditBegin is a stub for the audit-begin middleware that will record the
// start of every privileged operation in PR1. In PR0 it is intentionally a
// pass-through so the middleware mount points already exist when PR1 lands.
//
// It must never carry credentials. Per the ground rules, the credential
// Provider is passed as an explicit function argument via credentials.Wrap,
// not stuffed into context values.
func AuditBegin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// RequestID returns chi's RequestID middleware. Re-exported here so
// cmd/periscope/main.go imports a single httpx package for middleware
// composition rather than two.
func RequestID(next http.Handler) http.Handler {
	return middleware.RequestID(next)
}

// RealIP returns chi's RealIP middleware. Same rationale as RequestID.
func RealIP(next http.Handler) http.Handler {
	return middleware.RealIP(next)
}

// Recoverer returns chi's Recoverer middleware. Same rationale as RequestID.
func Recoverer(next http.Handler) http.Handler {
	return middleware.Recoverer(next)
}
