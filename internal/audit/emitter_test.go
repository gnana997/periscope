package audit

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordingSink struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordingSink) Record(_ context.Context, evt Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, evt)
}

func TestEmitter_FansOutToAllSinks(t *testing.T) {
	a, b := &recordingSink{}, &recordingSink{}
	e := New(a, b)
	e.Record(context.Background(), Event{Verb: VerbDelete, Outcome: OutcomeSuccess})

	if len(a.events) != 1 || len(b.events) != 1 {
		t.Fatalf("want both sinks to record once, got a=%d b=%d", len(a.events), len(b.events))
	}
}

func TestEmitter_StampsTimestampWhenZero(t *testing.T) {
	s := &recordingSink{}
	e := New(s)
	before := time.Now().UTC()
	e.Record(context.Background(), Event{Verb: VerbCreate, Outcome: OutcomeSuccess})
	after := time.Now().UTC()

	if len(s.events) != 1 {
		t.Fatalf("want 1 event, got %d", len(s.events))
	}
	ts := s.events[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v not in [%v, %v]", ts, before, after)
	}
}

func TestEmitter_PreservesExplicitTimestamp(t *testing.T) {
	s := &recordingSink{}
	e := New(s)
	when := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	e.Record(context.Background(), Event{
		Timestamp: when,
		Verb:      VerbCreate,
		Outcome:   OutcomeSuccess,
	})

	if !s.events[0].Timestamp.Equal(when) {
		t.Errorf("timestamp: got %v, want %v", s.events[0].Timestamp, when)
	}
}

func TestEmitter_FillsRequestContextFields(t *testing.T) {
	s := &recordingSink{}
	e := New(s)

	ctx, _ := WithRequestContext(context.Background(), RequestContext{
		RequestID: "req-42",
		Route:     "/api/x",
		Actor:     Actor{Sub: "u@x", Email: "u@x", Groups: []string{"sre"}},
	})

	e.Record(ctx, Event{Verb: VerbDelete, Outcome: OutcomeDenied})

	got := s.events[0]
	if got.RequestID != "req-42" {
		t.Errorf("RequestID: got %q", got.RequestID)
	}
	if got.Route != "/api/x" {
		t.Errorf("Route: got %q", got.Route)
	}
	if got.Actor.Sub != "u@x" || got.Actor.Email != "u@x" {
		t.Errorf("Actor not pulled from request context: %+v", got.Actor)
	}
}

func TestEmitter_HandlerActorWinsOverRequestContext(t *testing.T) {
	// When the handler explicitly sets Actor on the event (because
	// it has a Provider in hand), the Emitter should not overwrite
	// it with whatever the auth middleware planted.
	s := &recordingSink{}
	e := New(s)

	ctx, _ := WithRequestContext(context.Background(), RequestContext{
		Actor: Actor{Sub: "session-sub"},
	})
	e.Record(ctx, Event{
		Verb:    VerbCreate,
		Outcome: OutcomeSuccess,
		Actor:   Actor{Sub: "explicit-sub"},
	})

	if s.events[0].Actor.Sub != "explicit-sub" {
		t.Errorf("explicit actor was overwritten: got %q", s.events[0].Actor.Sub)
	}
}

func TestEmitter_NilIsNoOp(t *testing.T) {
	var e *Emitter
	// Must not panic.
	e.Record(context.Background(), Event{Verb: VerbCreate, Outcome: OutcomeSuccess})
}

func TestEmitter_EmptySinksIsNoOp(t *testing.T) {
	e := New()
	e.Record(context.Background(), Event{Verb: VerbCreate, Outcome: OutcomeSuccess})
}
