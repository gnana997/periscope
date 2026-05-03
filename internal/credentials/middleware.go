package credentials

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/gnana997/periscope/internal/audit"
)

// Handler is the signature for HTTP handlers that need a Provider. The
// Provider is an explicit argument — never read from context.Value —
// because credentials must not be smuggled through context.
type Handler func(http.ResponseWriter, *http.Request, Provider)

// Wrap returns an http.HandlerFunc that resolves a Provider for the
// request via the Factory and invokes the wrapped Handler. Failures to
// build a Provider return 500.
func Wrap(factory Factory, h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := SessionFromContext(r.Context())
		p, err := factory.For(r.Context(), session)
		if err != nil {
			slog.ErrorContext(r.Context(), "credentials.For failed",
				"err", err, "actor", session.Subject)
			http.Error(w, "credentials unavailable", http.StatusInternalServerError)
			return
		}
		h(w, r, p)
	}
}

type ctxKey int

const sessionKey ctxKey = 1

// WithSession plants a Session on the request context. Called by the
// auth middleware once it has resolved the user from the cookie.
//
// The Session here is the *identity* slice — Subject/Email/Groups —
// not credentials. Tokens never reach this layer; the AWS provider is
// resolved separately through the Factory.
//
// As a side-effect, WithSession also patches the audit.RequestContext
// (planted earlier by httpx.AuditBegin) with the resolved Actor so
// audit emissions downstream don't have to re-derive it. The patch
// is in-place on the pointer stored in ctx, so AuditBegin's
// downstream chain sees it.
func WithSession(ctx context.Context, s Session) context.Context {
	audit.PatchActor(ctx, audit.Actor{
		Sub:    s.Subject,
		Email:  s.Email,
		Groups: s.Groups,
	})
	return context.WithValue(ctx, sessionKey, s)
}

// SessionFromContext returns the Session attached by WithSession, or
// the zero value if none is present. A zero Session has Subject ==
// "anonymous" so audit lines never read empty.
func SessionFromContext(ctx context.Context) Session {
	if v, ok := ctx.Value(sessionKey).(Session); ok {
		return v
	}
	return Session{Subject: "anonymous"}
}
