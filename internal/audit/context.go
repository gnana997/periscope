package audit

import "context"

// RequestContext is the per-request audit slice planted by
// httpx.AuditBegin and enriched by the auth layer once the Session
// is known. Handlers read it via RequestContextFrom; the Emitter
// reads it automatically when handlers leave the corresponding
// fields zero on the Event.
//
// We hold a pointer in context so the auth middleware can patch
// Actor onto the same RequestContext that AuditBegin planted,
// without rebuilding the context value (which would be invisible to
// AuditBegin's downstream chain).
type RequestContext struct {
	RequestID string
	Route     string
	Actor     Actor
}

type ctxKey int

const requestContextKey ctxKey = 1

// WithRequestContext plants a fresh RequestContext on ctx and
// returns the new context plus a pointer to the stored value so a
// later middleware can patch it in place. Patching in place is
// deliberate: the middleware that knows the Actor (auth) runs
// after AuditBegin, but downstream handlers that read the context
// resolve it through the same key — they should see the patched
// Actor without anyone having to re-call WithValue.
func WithRequestContext(ctx context.Context, rc RequestContext) (context.Context, *RequestContext) {
	stored := rc
	return context.WithValue(ctx, requestContextKey, &stored), &stored
}

// RequestContextFrom returns the RequestContext attached by
// WithRequestContext, or the zero value if none is present. A zero
// value is safe to read — RequestID/Route/Actor.Sub are all empty
// strings, which sinks treat as "field absent."
func RequestContextFrom(ctx context.Context) RequestContext {
	if v, ok := ctx.Value(requestContextKey).(*RequestContext); ok && v != nil {
		return *v
	}
	return RequestContext{}
}

// PatchActor updates the Actor on the RequestContext stored in ctx.
// No-op if no RequestContext is present (e.g. internal callers that
// bypassed httpx.AuditBegin). Used by the auth middleware once the
// Session has been resolved from the cookie.
//
// IMPORTANT — single-writer per request. PatchActor MUST be called
// from the request goroutine only, never from a handler-spawned
// goroutine. The function mutates a *RequestContext pointer shared
// with concurrent readers in Emitter.Record; a write from a background
// goroutine would race (the race detector catches this the first
// time you run the test suite with -race).
//
// Today the auth middleware satisfies this constraint: it runs
// synchronously before any handler. If you add a future emit site
// that runs in a background goroutine spawned from a handler
// (e.g. async log streaming, batched writes), snapshot the Actor at
// goroutine launch time and pass it explicitly rather than re-reading
// from ctx.
func PatchActor(ctx context.Context, a Actor) {
	if v, ok := ctx.Value(requestContextKey).(*RequestContext); ok && v != nil {
		v.Actor = a
	}
}
