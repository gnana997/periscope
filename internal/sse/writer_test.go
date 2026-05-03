package sse

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// nonFlushingRecorder wraps an httptest.ResponseRecorder but does NOT
// expose Flush, so we can verify Open returns ErrStreamingUnsupported.
type nonFlushingRecorder struct {
	header http.Header
	status int
	body   strings.Builder
}

func (r *nonFlushingRecorder) Header() http.Header {
	if r.header == nil {
		r.header = make(http.Header)
	}
	return r.header
}

func (r *nonFlushingRecorder) Write(b []byte) (int, error) {
	return r.body.Write(b)
}

func (r *nonFlushingRecorder) WriteHeader(status int) {
	r.status = status
}

func TestOpen_SetsStandardHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, err := Open(rec)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sw.Close()

	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache, no-transform" {
		t.Errorf("Cache-Control = %q, want no-cache, no-transform", got)
	}
	if got := rec.Header().Get("Connection"); got != "keep-alive" {
		t.Errorf("Connection = %q, want keep-alive", got)
	}
	if got := rec.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestOpen_NoFlusherReturnsError(t *testing.T) {
	rec := &nonFlushingRecorder{}
	if _, err := Open(rec); err != ErrStreamingUnsupported {
		t.Fatalf("Open with non-flusher = %v, want ErrStreamingUnsupported", err)
	}
}

func TestPing(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, err := Open(rec)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sw.Close()

	if err := sw.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if got := rec.Body.String(); got != ": ping\n\n" {
		t.Errorf("Ping body = %q, want %q", got, ": ping\n\n")
	}
}

func TestEvent_DefaultEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, _ := Open(rec)
	defer sw.Close()

	if err := sw.Event("", "", map[string]string{"l": "hello"}); err != nil {
		t.Fatalf("Event: %v", err)
	}
	want := "data: {\"l\":\"hello\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("default event body = %q, want %q", got, want)
	}
}

func TestEvent_NamedEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, _ := Open(rec)
	defer sw.Close()

	if err := sw.Event("error", "", map[string]string{"message": "boom"}); err != nil {
		t.Fatalf("Event: %v", err)
	}
	want := "event: error\ndata: {\"message\":\"boom\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("named event body = %q, want %q", got, want)
	}
}

func TestEvent_WithID(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, _ := Open(rec)
	defer sw.Close()

	if err := sw.Event("added", "12345", struct{ Name string }{Name: "x"}); err != nil {
		t.Fatalf("Event: %v", err)
	}
	want := "event: added\nid: 12345\ndata: {\"Name\":\"x\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("event with id body = %q, want %q", got, want)
	}
}

func TestEvent_DoneShape(t *testing.T) {
	// Sanity check that the existing handlers' "event: done\ndata: {}\n\n"
	// shape is reproducible via Event("done", "", struct{}{}).
	rec := httptest.NewRecorder()
	sw, _ := Open(rec)
	defer sw.Close()

	if err := sw.Event("done", "", struct{}{}); err != nil {
		t.Fatalf("Event: %v", err)
	}
	want := "event: done\ndata: {}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("done event body = %q, want %q", got, want)
	}
}

func TestEventRaw(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, _ := Open(rec)
	defer sw.Close()

	if err := sw.EventRaw("meta", "", []byte(`{"pods":[]}`)); err != nil {
		t.Fatalf("EventRaw: %v", err)
	}
	want := "event: meta\ndata: {\"pods\":[]}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("raw event body = %q, want %q", got, want)
	}
}

func TestClose_Idempotent(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, _ := Open(rec)
	sw.Close()
	sw.Close() // must not panic
}

func TestHeartbeatC_NotNil(t *testing.T) {
	rec := httptest.NewRecorder()
	sw, _ := Open(rec)
	defer sw.Close()

	if sw.HeartbeatC() == nil {
		t.Fatal("HeartbeatC returned nil")
	}
}
