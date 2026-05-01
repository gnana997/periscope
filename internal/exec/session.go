package exec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"k8s.io/client-go/tools/remotecommand"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// Session orchestration for one exec stream.
//
// Wire format on the browser-facing WebSocket (see RFC 0001 §6):
//
//	binary frames  →  stdin (in)  /  merged stdout+stderr (out)
//	text frames    →  JSON control messages
//
// Control messages:
//
//	in : {"type":"resize","cols":N,"rows":N}, {"type":"close"}
//	out: {"type":"hello",...}, {"type":"closed",...}, {"type":"error",...},
//	     {"type":"idle_warn","secondsRemaining":N}
//
// PR3 adds heartbeat (ws.Ping every cfg.HeartbeatInterval), idle timer
// (cfg.IdleTimeout with cfg.IdleWarnLead grace), and a heuristic that
// upgrades a fast 127-exit with no stdin/stdout to E_NO_SHELL so the UI
// can show a friendlier error than "container_exit / 127".

// Params is the subset of inputs needed to start a Session.
type Params struct {
	SessionID string
	Actor     string
	Cluster   clusters.Cluster
	Namespace string
	Pod       string
	Container string   // empty → resolved by k8s.ExecPod
	Command   []string // empty → DefaultShellCommand
	TTY       bool
}

// Stats captures byte-level metrics for the audit end record. Counts are
// best-effort: they reflect what we forwarded between the WebSocket and the
// exec stream, not what the container actually consumed or emitted.
type Stats struct {
	BytesIn  int64
	BytesOut int64
}

// pingTimeout bounds a single heartbeat ping. The library waits for a pong
// via the read loop; this is the upper limit before we declare the peer
// gone and tear the session down.
const pingTimeout = 10 * time.Second

// Run executes a single exec session over the supplied WebSocket. It
// blocks until the stream ends and returns the final result, byte
// counters, and any execution error for the caller's audit record.
//
// The caller is responsible for:
//   - registering/de-registering the session in a Registry,
//   - emitting audit start/end records,
//   - closing the WebSocket if Run returns an error before doing so itself.
func Run(ctx context.Context, ws *websocket.Conn, p credentials.Provider, params Params, cfg Config) (k8s.ExecResult, Stats, error) {
	// Tie the session to a cancellable context. Any path that wants to end
	// the session — client {type:close}, idle timeout, heartbeat fail,
	// stream EOF, error — cancels here.
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	// Buffered so a slow apiserver writer doesn't block control-frame
	// processing. Resize events are infrequent; capacity 8 is generous.
	resizeCh := make(chan remotecommand.TerminalSize, 8)

	// Tracks bytes flowing through the session for the audit end record.
	var bytesIn, bytesOut atomic.Int64

	// lastActivity is the unix-nano timestamp of the most recent stdin
	// byte received OR stdout byte sent. Heartbeat ping/pong does NOT
	// reset it (RFC 0001 §7 — "a session with no terminal output is not
	// active just because it's holding a connection").
	startedAt := time.Now()
	var lastActivity atomic.Int64
	lastActivity.Store(startedAt.UnixNano())

	// closeOverride lets internal goroutines (heartbeat, idle) tag the
	// reason a session was torn down. Read by closedFrame at the end so
	// the audit/UI distinguishes "client closed" from "server idle close"
	// from "transport ping timeout".
	var closeOverride atomic.Pointer[string]
	setOverride := func(reason string) {
		r := reason
		closeOverride.CompareAndSwap(nil, &r)
	}

	// Reader goroutine: browser → server.
	// Binary frames feed stdin; text frames are JSON control messages.
	go func() {
		defer stdinW.Close()
		defer close(resizeCh)
		for {
			mt, data, err := ws.Read(sessionCtx)
			if err != nil {
				return
			}
			switch mt {
			case websocket.MessageBinary:
				if _, werr := stdinW.Write(data); werr != nil {
					return
				}
				bytesIn.Add(int64(len(data)))
				lastActivity.Store(time.Now().UnixNano())
			case websocket.MessageText:
				handleControl(sessionCtx, data, resizeCh, cancel)
			}
		}
	}()

	// Writer goroutine: server → browser.
	// Forwards stdout (which already merges stderr) as binary frames.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		buf := make([]byte, 32*1024)
		for {
			n, err := stdoutR.Read(buf)
			if n > 0 {
				if werr := ws.Write(sessionCtx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
				bytesOut.Add(int64(n))
				lastActivity.Store(time.Now().UnixNano())
			}
			if err != nil {
				return
			}
		}
	}()

	// Heartbeat goroutine: ws.Ping at cfg.HeartbeatInterval. coder/websocket
	// serializes writes internally, so this is safe to run alongside the
	// stdout writer. A failed ping marks the peer as dead.
	go func() {
		ticker := time.NewTicker(cfg.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(sessionCtx, pingTimeout)
				err := ws.Ping(pingCtx)
				pingCancel()
				if err != nil {
					// Ping returns an error if context expired (timeout)
					// or the WS is already closing. Either way, kill the
					// session.
					setOverride("heartbeat_timeout")
					cancel()
					return
				}
			}
		}
	}()

	// Idle goroutine: ticks every 5s. Sends idle_warn at T-IdleWarnLead and
	// closes at T. Activity (stdin or stdout bytes) resets both.
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		warned := false
		for {
			select {
			case <-sessionCtx.Done():
				return
			case <-ticker.C:
				elapsed := time.Duration(time.Now().UnixNano() - lastActivity.Load())
				if elapsed >= cfg.IdleTimeout {
					setOverride("idle")
					_ = writeControl(sessionCtx, ws, controlFrame{
						Type:   "closed",
						Reason: "idle",
					})
					cancel()
					return
				}
				warnAt := cfg.IdleTimeout - cfg.IdleWarnLead
				if !warned && elapsed >= warnAt {
					_ = writeControl(sessionCtx, ws, controlFrame{
						Type:             "idle_warn",
						SecondsRemaining: int(cfg.IdleWarnLead.Seconds()),
					})
					warned = true
				} else if warned && elapsed < warnAt {
					// User typed during the grace window — reset so a
					// future quiet period re-warns.
					warned = false
				}
			}
		}
	}()

	// Send the hello frame. Resolution of the actual container/command
	// happens inside ExecPod; we send what we know so the client can
	// render "Connecting to <container>" before the first prompt arrives.
	helloContainer := params.Container
	if helloContainer == "" {
		// Best-effort lookup — failure here is non-fatal because ExecPod
		// will resolve again.
		if c, err := k8s.ResolveExecTarget(sessionCtx, p, params.Cluster, params.Namespace, params.Pod, ""); err == nil {
			helloContainer = c
		}
	}
	_ = writeControl(sessionCtx, ws, controlFrame{
		Type:      "hello",
		SessionID: params.SessionID,
		Container: helloContainer,
	})

	// Run the exec stream. Stdout and Stderr point at the same pipe so the
	// browser sees a single merged stream (RFC §6).
	result, execErr := k8s.ExecPod(sessionCtx, p, k8s.ExecPodArgs{
		Cluster:      params.Cluster,
		Namespace:    params.Namespace,
		Pod:          params.Pod,
		Container:    params.Container,
		Command:      params.Command,
		TTY:          params.TTY,
		Stdin:        stdinR,
		Stdout:       stdoutW,
		Stderr:       stdoutW,
		TerminalSize: resizeCh,
		SessionID:    params.SessionID,
	})

	// Closing the writer end of stdout signals the writer goroutine to
	// drain and exit.
	_ = stdoutW.Close()
	<-writerDone

	stats := Stats{BytesIn: bytesIn.Load(), BytesOut: bytesOut.Load()}
	elapsed := time.Since(startedAt)
	override := closeOverride.Load()

	// E_NO_SHELL heuristic: a 127-exit with zero traffic in under 1.5s
	// almost certainly means the container has no shell on PATH. Surface
	// it as a friendlier error frame so the UI can render a helpful
	// message instead of a confusing "exit 127".
	if override == nil && execErr == nil && shouldFlagNoShell(result, stats, elapsed) {
		_ = writeControl(sessionCtx, ws, controlFrame{
			Type:      "error",
			Code:      "E_NO_SHELL",
			Message:   "container has no shell on PATH",
			Retryable: false,
		})
		// Tag the audit record so close_reason joins to "no_shell".
		setOverride("no_shell")
		override = closeOverride.Load()
		// Convert the result so the audit reason is consistent.
		result.Reason = "no_shell"
	} else {
		// Default close path: announce the close reason to the client.
		_ = writeControl(sessionCtx, ws, closedFrame(result, execErr, override))
	}

	// If we tagged an override, surface it through the result so the
	// audit end record records the right reason.
	if override != nil && *override != "" {
		result.Reason = *override
	}

	return result, stats, execErr
}

// shouldFlagNoShell returns true when the heuristic for E_NO_SHELL fires:
// the container exited 127 in under 1.5 seconds with no bytes in either
// direction. The conservative thresholds avoid mislabeling real shells
// that fail fast for unrelated reasons.
func shouldFlagNoShell(r k8s.ExecResult, s Stats, elapsed time.Duration) bool {
	return r.ExitCode == 127 &&
		r.Reason == "container_exit" &&
		s.BytesIn == 0 &&
		s.BytesOut == 0 &&
		elapsed < 1500*time.Millisecond
}

// controlFrame is the JSON envelope for browser-facing text-frame messages.
// Fields use omitempty so each message type only carries its own fields.
type controlFrame struct {
	Type             string `json:"type"`
	SessionID        string `json:"sessionId,omitempty"`
	Container        string `json:"container,omitempty"`
	Reason           string `json:"reason,omitempty"`
	ExitCode         int    `json:"exitCode,omitempty"`
	Code             string `json:"code,omitempty"`
	Message          string `json:"message,omitempty"`
	Retryable        bool   `json:"retryable,omitempty"`
	SecondsRemaining int    `json:"secondsRemaining,omitempty"`
	Cols             int    `json:"cols,omitempty"`
	Rows             int    `json:"rows,omitempty"`
}

// inboundControl mirrors controlFrame but is parsed by the reader goroutine.
// We split the types so the producer side can stay strict about omitempty
// without surprising the consumer.
type inboundControl struct {
	Type string `json:"type"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

func handleControl(ctx context.Context, data []byte, resize chan<- remotecommand.TerminalSize, cancel context.CancelFunc) {
	var msg inboundControl
	if err := json.Unmarshal(data, &msg); err != nil {
		// Bad JSON from a (hostile or buggy) client is not fatal — drop it.
		return
	}
	switch msg.Type {
	case "resize":
		if msg.Cols <= 0 || msg.Rows <= 0 {
			return
		}
		select {
		case resize <- remotecommand.TerminalSize{Width: uint16(msg.Cols), Height: uint16(msg.Rows)}:
		case <-ctx.Done():
		default:
			// Channel full — drop. Resizes are idempotent; the next one
			// will catch up.
		}
	case "close":
		cancel()
	}
}

func writeControl(ctx context.Context, ws *websocket.Conn, frame controlFrame) error {
	wctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return ws.Write(wctx, websocket.MessageText, b)
}

// closedFrame builds the final {type:closed,...} message for the client.
// override (if non-nil) wins over both the result reason and any error
// classification — used when an internal goroutine (heartbeat/idle) tore
// the session down for a reason the apiserver wouldn't know about.
func closedFrame(r k8s.ExecResult, err error, override *string) controlFrame {
	frame := controlFrame{Type: "closed", ExitCode: r.ExitCode}
	if override != nil && *override != "" {
		frame.Reason = *override
		return frame
	}
	if err == nil {
		frame.Reason = r.Reason
		if frame.Reason == "" {
			frame.Reason = "completed"
		}
		return frame
	}
	switch {
	case errors.Is(err, context.Canceled):
		frame.Reason = "client"
	case errors.Is(err, context.DeadlineExceeded):
		frame.Reason = "deadline"
	default:
		frame.Reason = "server_error"
	}
	return frame
}
