package exec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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
//   binary frames  →  stdin (in)  /  merged stdout+stderr (out)
//   text frames    →  JSON control messages
//
// PR1 supports these control messages:
//   in : {"type":"resize","cols":N,"rows":N}, {"type":"close"}
//   out: {"type":"hello",...}, {"type":"closed",...}, {"type":"error",...}
//
// Idle/heartbeat/auto-reconnect are PR3.

// Params is the subset of inputs needed to start a Session. They are
// gathered by the HTTP handler and passed straight through.
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

// Run executes a single exec session over the supplied WebSocket. It blocks
// until the stream ends and returns the final result for audit logging.
//
// The caller is responsible for:
//   - registering/de-registering the session in a Registry,
//   - emitting audit start/end records,
//   - closing the WebSocket if Run returns an error before doing so itself.
func Run(ctx context.Context, ws *websocket.Conn, p credentials.Provider, params Params) (k8s.ExecResult, error) {
	// Tie the session to a cancellable context. Any path that wants to end
	// the session — client {type:close}, stream EOF, error — cancels here.
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()

	// Buffered so a slow apiserver writer doesn't block control-frame
	// processing. Resize events are infrequent; capacity 8 is generous.
	resizeCh := make(chan remotecommand.TerminalSize, 8)

	// Tracks bytes flowing through the session for the audit end record.
	var bytesIn, bytesOut atomic.Int64

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
			case websocket.MessageText:
				handleControl(sessionCtx, data, resizeCh, cancel)
			}
		}
	}()

	// Writer goroutine: server → browser.
	// Forwards stdout (which already merges stderr — see ExecPod call below)
	// as binary frames.
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
			}
			if err != nil {
				return
			}
		}
	}()

	// Send the hello frame. Resolution of the actual container/command
	// happens inside ExecPod; we send what we know so the client can render
	// "Connecting to <container>" before the first prompt arrives.
	helloContainer := params.Container
	if helloContainer == "" {
		// Best-effort lookup so the hello message has a populated container
		// field. Failure here is non-fatal — ExecPod will resolve again.
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
	// browser sees a single merged stream (see RFC §6 — "stdout+stderr
	// merged on the server").
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

	// Closing the writer end of the stdout pipe signals the writer
	// goroutine to drain and exit.
	_ = stdoutW.Close()
	<-writerDone

	// Send a final closed frame so the client knows the reason and exit
	// code. Best-effort: the WS may already be gone.
	closed := closedFrame(result, execErr)
	_ = writeControl(sessionCtx, ws, closed)

	// Annotate the audit record's bytes_* fields via slog, and return the
	// result + error for the caller's session_end record.
	slog.InfoContext(ctx, "pod_exec.session.streamed",
		"category", "audit_detail",
		"session_id", params.SessionID,
		"bytes_stdin", bytesIn.Load(),
		"bytes_stdout", bytesOut.Load(),
		"close_reason", closed.Reason,
		"exit_code", closed.ExitCode,
	)

	return result, execErr
}

// controlFrame is the JSON envelope for browser-facing text-frame messages.
// Fields are tagged with `omitempty` so each message type only carries its
// own fields.
type controlFrame struct {
	Type            string `json:"type"`
	SessionID       string `json:"sessionId,omitempty"`
	Container       string `json:"container,omitempty"`
	Reason          string `json:"reason,omitempty"`
	ExitCode        int    `json:"exitCode,omitempty"`
	ExitCodeSet     bool   `json:"-"`
	Code            string `json:"code,omitempty"`
	Message         string `json:"message,omitempty"`
	SecondsRemaining int   `json:"secondsRemaining,omitempty"`
	Cols            int    `json:"cols,omitempty"`
	Rows            int    `json:"rows,omitempty"`
}

// inboundControl mirrors controlFrame but is parsed by reader goroutines.
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

func closedFrame(r k8s.ExecResult, err error) controlFrame {
	frame := controlFrame{Type: "closed", ExitCode: r.ExitCode}
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
