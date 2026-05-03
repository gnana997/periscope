package main

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/authz"
	"github.com/gnana997/periscope/internal/credentials"
)

// auditQueryHandler serves GET /api/audit. Authz policy:
//
//   - Tier mode + tier == admin: sees all rows; honors any filters
//     the client passed.
//   - Anything else (non-admin tier, shared mode, raw mode): the
//     server *forces* the actor filter to the caller's own subject
//     regardless of what the client asked. Means an SRE can
//     self-audit but cannot peek at what colleagues did.
//
// The endpoint is registered only when the audit Reader is wired
// (i.e. when SQLite was successfully opened at boot). When SQLite is
// disabled the route returns 404, which is the right shape for
// "feature off."
func auditQueryHandler(reader audit.Reader, resolver *authz.Resolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := credentials.SessionFromContext(r.Context())
		if s.Subject == "" || s.Subject == "anonymous" {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}

		args, err := parseAuditQuery(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// canSeeAll resolves via Resolver.IsAuditAdmin — combines the explicit
		// AuditAdminGroups override with mode-specific fallbacks. See
		// internal/authz/mode.go::IsAuditAdmin for the full resolution order.
		canSeeAll := resolver != nil && resolver.IsAuditAdmin(authz.Identity{
			Subject: s.Subject, Groups: s.Groups,
		})
		if !canSeeAll {
			args.Actor = s.Subject
			w.Header().Set("X-Audit-Scope", "self")
		} else {
			w.Header().Set("X-Audit-Scope", "all")
		}

		result, err := reader.Query(r.Context(), args)
		if err != nil {
			slog.ErrorContext(r.Context(), "audit query failed",
				"err", err, "actor", s.Subject)
			http.Error(w, "operation failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// parseAuditQuery translates URL query params into audit.QueryArgs.
// All fields are optional; bad time / int values are surfaced as 400
// so callers don't silently get unintended results.
func parseAuditQuery(r *http.Request) (audit.QueryArgs, error) {
	q := r.URL.Query()
	args := audit.QueryArgs{
		Actor:        q.Get("actor"),
		Verb:         audit.Verb(q.Get("verb")),
		Outcome:      audit.Outcome(q.Get("outcome")),
		Cluster:      q.Get("cluster"),
		Namespace:    q.Get("namespace"),
		ResourceName: q.Get("name"),
		RequestID:    q.Get("request_id"),
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return args, parseErr("from", err)
		}
		args.From = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return args, parseErr("to", err)
		}
		args.To = t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return args, parseErr("limit", err)
		}
		args.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return args, parseErr("offset", err)
		}
		args.Offset = n
	}
	return args, nil
}

func parseErr(field string, err error) error {
	if err == nil {
		return &auditQueryParseError{field: field}
	}
	return &auditQueryParseError{field: field, cause: err}
}

type auditQueryParseError struct {
	field string
	cause error
}

func (e *auditQueryParseError) Error() string {
	if e.cause != nil {
		return "invalid " + e.field + ": " + e.cause.Error()
	}
	return "invalid " + e.field
}
