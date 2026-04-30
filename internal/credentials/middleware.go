package credentials

import (
	"log/slog"
	"net/http"
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
		session := sessionFromRequest(r)
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

// sessionFromRequest extracts the authenticated Session from the request.
// v1 stub: returns a fixed local-dev session. Replace when Okta OIDC
// middleware lands.
func sessionFromRequest(_ *http.Request) Session {
	return Session{Subject: "dev@local"}
}
