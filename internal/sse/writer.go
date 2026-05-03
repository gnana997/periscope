// Package sse provides a single-goroutine writer for Server-Sent Events
// over an http.ResponseWriter.
//
// SSE handlers in periscope share a fixed shape: set the standard event
// stream headers, send a 200, flush, then run a select loop that
// multiplexes a heartbeat ticker against a domain event source. This
// package factors out the boilerplate so handlers focus on their
// domain logic.
//
// Concurrency: a Writer is owned by a single goroutine. http.ResponseWriter
// is not goroutine-safe and Writer makes no attempt to make it so.
package sse

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DefaultHeartbeatInterval is the cadence Writer uses for its built-in
// keepalive ticker. 15s is well under the AWS ALB default idle timeout
// of 60s and matches the existing periscope SSE handlers.
const DefaultHeartbeatInterval = 15 * time.Second

// ErrStreamingUnsupported is returned by Open when the underlying
// http.ResponseWriter does not implement http.Flusher.
var ErrStreamingUnsupported = errors.New("sse: streaming unsupported (no http.Flusher)")

// Writer is a single-goroutine SSE writer.
//
// Construct with Open, defer Close, then drive the response from a
// select loop:
//
//	sw, err := sse.Open(w)
//	if err != nil {
//	    http.Error(w, "streaming unsupported", http.StatusInternalServerError)
//	    return
//	}
//	defer sw.Close()
//
//	for {
//	    select {
//	    case <-r.Context().Done():
//	        return
//	    case <-sw.HeartbeatC():
//	        _ = sw.Ping()
//	    case ev := <-events:
//	        _ = sw.Event("message", "", ev)
//	    }
//	}
type Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
	ticker  *time.Ticker
}

// Open writes the standard SSE response headers, sends a 200 status,
// flushes so clients see the connection as established, and starts an
// internal heartbeat ticker firing at DefaultHeartbeatInterval.
//
// Returns ErrStreamingUnsupported if w does not implement http.Flusher.
// On success, the caller must call Close (typically via defer) to stop
// the ticker.
func Open(w http.ResponseWriter) (*Writer, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, ErrStreamingUnsupported
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	return &Writer{
		w:       w,
		flusher: flusher,
		ticker:  time.NewTicker(DefaultHeartbeatInterval),
	}, nil
}

// HeartbeatC returns the channel that fires on each heartbeat tick.
// The caller selects on it in the main loop and calls Ping on tick.
func (sw *Writer) HeartbeatC() <-chan time.Time {
	return sw.ticker.C
}

// Ping writes a `: ping` SSE comment and flushes. Comments are ignored
// by EventSource clients but reset proxy idle timers along the path.
func (sw *Writer) Ping() error {
	if _, err := fmt.Fprint(sw.w, ": ping\n\n"); err != nil {
		return err
	}
	sw.flusher.Flush()
	return nil
}

// Event writes a single SSE event with the given event type, optional
// id, and JSON-marshalled data, then flushes.
//
// An empty eventType emits a default event (no "event:" line); an empty
// id omits the "id:" line. data is marshalled with encoding/json.
func (sw *Writer) Event(eventType, id string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("sse: marshal data: %w", err)
	}
	return sw.EventRaw(eventType, id, payload)
}

// EventRaw is Event but takes a pre-marshalled payload. Use it on hot
// paths where the caller already produced the bytes and a second
// allocation is wasteful, or when the payload's JSON shape must be
// preserved exactly.
//
// data is treated as opaque. Callers must ensure the payload contains
// no embedded newlines, since SSE uses "\n\n" as the event terminator.
func (sw *Writer) EventRaw(eventType, id string, data []byte) error {
	if eventType != "" {
		if _, err := fmt.Fprintf(sw.w, "event: %s\n", eventType); err != nil {
			return err
		}
	}
	if id != "" {
		if _, err := fmt.Fprintf(sw.w, "id: %s\n", id); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(sw.w, "data: %s\n\n", data); err != nil {
		return err
	}
	sw.flusher.Flush()
	return nil
}

// Close stops the heartbeat ticker. Idempotent.
func (sw *Writer) Close() {
	if sw.ticker != nil {
		sw.ticker.Stop()
		sw.ticker = nil
	}
}
