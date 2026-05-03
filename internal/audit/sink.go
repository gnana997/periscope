package audit

import (
	"context"
	"log/slog"
)

// Sink is the contract between the Emitter and a downstream
// destination. Implementations must be safe for concurrent calls
// from multiple request handlers — the Emitter does not serialize.
//
// Record is intentionally infallible. An audit pipeline that drops
// rows on a transient sink error is worse than one that logs the
// error and moves on — the alternative is blocking a privileged
// request on the audit DB being slow. Sinks that need persistence
// guarantees should implement their own buffering.
type Sink interface {
	Record(ctx context.Context, evt Event)
}

// StdoutSink emits the Event as a structured slog record.
//
// Field naming preserves the keys the pre-refactor handlers used
// (`category=audit`, `event=<verb>`, `actor.sub`, `cluster`,
// `session_id`, `bytes_stdin`, etc.) so existing log scrapers and
// SIEM queries continue to match. New fields (`outcome`,
// `request_id`, `route`, `reason`) are additive.
//
// The slog record's own `time` field carries the timestamp; we don't
// emit a separate `ts` key here. Sinks that own their storage column
// (SQLite) read evt.Timestamp directly.
type StdoutSink struct {
	// Logger is the slog logger to write through. If nil,
	// slog.Default() is used at Record time so callers can construct
	// the sink before main() finishes wiring the default logger.
	Logger *slog.Logger
}

// Record emits one slog Info line per event. Optional fields are
// only emitted when non-empty so cluster-scoped or exec events don't
// carry empty `namespace`/`name` keys.
func (s *StdoutSink) Record(ctx context.Context, evt Event) {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}

	attrs := make([]any, 0, 16+len(evt.Extra)*2)
	attrs = append(attrs,
		"category", "audit",
		"event", string(evt.Verb),
		"outcome", string(evt.Outcome),
		"actor.sub", evt.Actor.Sub,
	)
	if evt.Actor.Email != "" {
		attrs = append(attrs, "actor.email", evt.Actor.Email)
	}
	if len(evt.Actor.Groups) > 0 {
		attrs = append(attrs, "actor.groups", evt.Actor.Groups)
	}
	if evt.RequestID != "" {
		attrs = append(attrs, "request_id", evt.RequestID)
	}
	if evt.Route != "" {
		attrs = append(attrs, "route", evt.Route)
	}
	if evt.Cluster != "" {
		attrs = append(attrs, "cluster", evt.Cluster)
	}
	if evt.Resource.Group != "" {
		attrs = append(attrs, "group", evt.Resource.Group)
	}
	if evt.Resource.Version != "" {
		attrs = append(attrs, "version", evt.Resource.Version)
	}
	if evt.Resource.Resource != "" {
		attrs = append(attrs, "resource", evt.Resource.Resource)
	}
	if evt.Resource.Namespace != "" {
		attrs = append(attrs, "namespace", evt.Resource.Namespace)
	}
	if evt.Resource.Name != "" {
		attrs = append(attrs, "name", evt.Resource.Name)
	}
	if evt.Reason != "" {
		attrs = append(attrs, "reason", evt.Reason)
	}
	for k, v := range evt.Extra {
		attrs = append(attrs, k, v)
	}

	logger.InfoContext(ctx, "audit", attrs...)
}
