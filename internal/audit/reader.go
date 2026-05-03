package audit

import (
	"context"
	"time"
)

// Reader is the read-side counterpart to Sink — anything that can
// surface persisted audit rows back to a handler. Defined here rather
// than coupled to *SQLiteSink so a future Postgres or external
// backend (or a no-op stub for tests) can satisfy the same contract.
type Reader interface {
	Query(ctx context.Context, q QueryArgs) (QueryResult, error)
}

// QueryArgs is the filter shape for /api/audit. Empty fields are
// treated as "no filter on this dimension." All filters compose with
// AND. Time range is inclusive on From, exclusive on To.
//
// All exact-match fields are indexed (or hit indexed prefixes); the
// reader does not allow free-text or LIKE queries in v1 because they
// would degrade past the retention cap.
type QueryArgs struct {
	From time.Time
	To   time.Time

	// Actor restricts to rows where actor_sub equals this value.
	// The HTTP handler enforces this filter for non-admin users
	// regardless of what the client passed.
	Actor string

	Verb         Verb
	Outcome      Outcome
	Cluster      string
	Namespace    string
	ResourceName string
	RequestID    string

	// Limit and Offset drive pagination. Limit is clamped by the
	// reader to a sensible maximum (500) so a client can't ask for
	// 1M rows at once. Zero Limit means "use the reader default."
	Limit  int
	Offset int
}

// QueryResult is the wire shape returned to the SPA. Items are
// ordered newest-first by timestamp, then by id for deterministic
// pagination across rows that share a timestamp.
type QueryResult struct {
	Items  []Row `json:"items"`
	Total  int   `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// Row is one persisted audit event projected back to the SPA.
// JSON tags are explicit so the wire shape doesn't drift if the Go
// struct field names ever change.
type Row struct {
	ID        int64          `json:"id"`
	Timestamp time.Time      `json:"timestamp"`
	RequestID string         `json:"requestId,omitempty"`
	Actor     Actor          `json:"actor"`
	Verb      Verb           `json:"verb"`
	Outcome   Outcome        `json:"outcome"`
	Cluster   string         `json:"cluster,omitempty"`
	Resource  ResourceRef    `json:"resource"`
	Reason    string         `json:"reason,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// MaxQueryLimit caps the per-page row count regardless of what the
// caller asked for. Keeps a runaway client from yanking the entire
// table in one round-trip.
const MaxQueryLimit = 500

// DefaultQueryLimit is what the reader uses when QueryArgs.Limit is
// zero. Big enough for a useful first page, small enough that an
// unfiltered query stays cheap.
const DefaultQueryLimit = 50
