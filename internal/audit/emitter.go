package audit

import (
	"context"
	"time"
)

// Emitter is the entry point handlers call to record an audit
// event. It fans out to every configured Sink.
//
// Sinks are held as a slice rather than a single field because the
// roadmap is "stdout today, stdout + SQLite tomorrow, maybe a SIEM
// shipper later" — designing for multiple sinks from day one means
// adding the SQLite sink in PR2 is a one-line append in main().
//
// The Emitter is stateless beyond its sink slice, so it is safe to
// share across goroutines without further synchronization.
type Emitter struct {
	sinks []Sink
}

// New constructs an Emitter wrapping the given sinks. Passing zero
// sinks is allowed and yields a no-op emitter — useful for tests
// that don't care about audit output.
func New(sinks ...Sink) *Emitter {
	return &Emitter{sinks: sinks}
}

// Record stamps the timestamp if unset, snapshots any per-request
// audit context (request_id, route, actor) onto the event so
// handlers don't need to thread it manually, then fans out to every
// sink.
//
// A nil Emitter is treated as a no-op so handlers reached during
// startup or in tests where audit isn't wired don't have to
// guard their call sites.
func (e *Emitter) Record(ctx context.Context, evt Event) {
	if e == nil {
		return
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	rc := RequestContextFrom(ctx)
	if evt.RequestID == "" {
		evt.RequestID = rc.RequestID
	}
	if evt.Route == "" {
		evt.Route = rc.Route
	}
	// Per-request Actor takes precedence over Session-derived data
	// only when the handler hasn't already filled it in. Handlers
	// that have a Provider in hand (post-credentials.Wrap) will
	// usually populate Actor explicitly with the OIDC sub; the
	// fallback is for handlers that emit before Provider resolution.
	if evt.Actor.Sub == "" {
		evt.Actor = rc.Actor
	}
	for _, s := range e.sinks {
		s.Record(ctx, evt)
	}
}
