// Per-connection idle-timeout wrapper for hijacked WebSocket / SPDY
// connections in the agent's reverse proxy.
//
// Why this exists:
//   The server's session.Run already enforces an exec idle timeout
//   (PERISCOPE_EXEC_IDLE_SECONDS, default 600s). On a normal close,
//   the chain is:
//
//     server session.Run hits idle deadline
//       → server closes the WS to the browser
//       → client-go executor closes the loopback conn
//       → loopback proxy.pipeBidi sees EOF
//       → tunneled conn closes
//       → agent's accepted conn closes
//
//   But if the server crashes / partitions / drops the link mid-
//   session, that close cascade never runs and the agent holds the
//   hijacked TCP conn open forever — until the OS or kernel TCP
//   keep-alive eventually reaps it (minutes to hours depending on
//   sysctl).
//
//   This wrapper gives the agent an INDEPENDENT idle-timeout so a
//   stuck WS / SPDY stream gets reaped on the agent side regardless
//   of upstream-server liveness. Activity = any successful Read on
//   the conn (which includes WS data frames, control frames, ping
//   responses, SPDY streams). After idleTimeout of zero successful
//   Reads, the next Read errors with i/o timeout and the proxy's
//   pipe goroutines unwind.
//
//   http.Server's own IdleTimeout doesn't help here — once Hijack()
//   runs, the Server gives up control and IdleTimeout no longer
//   applies. We have to install the deadline on the conn itself.

package main

import (
	"log/slog"
	"net"
	"time"
)

// idleConn wraps a net.Conn with a read deadline that resets on every
// successful Read. Idle for longer than `timeout` → next Read returns
// a net.Error with Timeout()=true.
//
// Writes don't reset the deadline — the deadline is about RECEIVING
// activity. A peer that's writing but never reading is unusual but
// shouldn't be killed by an idle reader timeout.
type idleConn struct {
	net.Conn
	timeout time.Duration
	logCtx  string // pod path or similar identifier; only used at the timeout-fired log line
}

// newIdleConn returns the wrapped conn with the initial deadline set.
// timeout <= 0 returns the underlying conn unwrapped (no-op).
func newIdleConn(c net.Conn, timeout time.Duration, logCtx string) net.Conn {
	if timeout <= 0 {
		return c
	}
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	return &idleConn{Conn: c, timeout: timeout, logCtx: logCtx}
}

// Read resets the deadline on success. On timeout, logs once at INFO so
// operators can correlate "exec session reaped after Xs idle" lines
// with their request_id traces.
func (c *idleConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err == nil {
		_ = c.SetReadDeadline(time.Now().Add(c.timeout))
		return n, nil
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		slog.Info("agent.exec_idle_timeout",
			"timeout_seconds", int(c.timeout.Seconds()),
			"context", c.logCtx,
		)
	}
	return n, err
}
