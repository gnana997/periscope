// Loopback CONNECT proxy for exec on agent-managed clusters.
//
// Why this file exists:
//   client-go's remotecommand executors — both the WebSocket variant
//   (NewWebSocketExecutor) and the SPDY one (NewSPDYExecutor) — build
//   their own dialers internally and ignore rest.Config.Transport.
//   Specifically:
//
//     - WebSocket: k8s.io/client-go/transport/websocket builds a
//       gorilla/websocket Dialer with no NetDialContext hook
//       (transport/websocket/roundtripper.go).
//     - SPDY: k8s.io/streaming/pkg/httpstream/spdy builds a
//       SpdyRoundTripper that uses *net.Dialer for TCP
//       (httpstream/spdy/roundtripper.go).
//
//   The only ext-config knob those dialers honour is rest.Config.Proxy,
//   which both consult to decide whether to send a CONNECT through a
//   forward proxy before the real dial. So we bind a tiny loopback
//   proxy here, set cfg.Proxy on agent-backed clusters to point at it,
//   and the executors' CONNECT requests get translated into per-cluster
//   tunnel dials.
//
//   Pod GET / list / watch and every other plain-HTTP request keep
//   flowing through cfg.Transport directly — the proxy is only
//   consulted by the upgrade dialers that bypass Transport.
//
// Lifecycle:
//   - cmd/periscope calls StartAgentExecProxy(rootCtx) once at startup.
//   - The proxy listens on 127.0.0.1:0 (kernel picks port).
//   - buildAgentRestConfig reads agentExecProxyURL() and stuffs it
//     into cfg.Proxy when the cluster backend is agent.
//   - StartAgentExecProxy returns a stop function that closes the
//     listener AND drains in-flight CONNECT-tunneled connections.
//     Calling stop() before the rest of the tunnel infra goes away
//     prevents a race between in-flight pipeBidi io.Copy writes and
//     the tunnel server's per-connection close handling (the upstream
//     rancher/remotedialer library does not internally synchronise
//     close-vs-write on its connections; tracked separately).

package k8s

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// agentExecProxyURL is the loopback URL of the running proxy; nil
// before StartAgentExecProxy is called. Reads via the function below
// so callers don't peek at the var directly.
var (
	agentExecProxyMu  sync.RWMutex
	agentExecProxyVal *url.URL
)

func setAgentExecProxyURL(u *url.URL) {
	agentExecProxyMu.Lock()
	defer agentExecProxyMu.Unlock()
	agentExecProxyVal = u
}

// agentExecProxyURL returns the live loopback URL or nil if the proxy
// hasn't been started. buildAgentRestConfig consults it; tests stub
// via TestSetAgentExecProxyURL.
func agentExecProxyURL() *url.URL {
	agentExecProxyMu.RLock()
	defer agentExecProxyMu.RUnlock()
	return agentExecProxyVal
}

// TestSetAgentExecProxyURL is a test-only entry point for substituting
// the proxy URL without spinning up a real listener. Production callers
// should use StartAgentExecProxy.
func TestSetAgentExecProxyURL(u *url.URL) {
	setAgentExecProxyURL(u)
}

// StartAgentExecProxy binds a loopback HTTP CONNECT proxy. Returns
// once the listener is up so callers can immediately rely on
// agentExecProxyURL() returning a non-nil URL. The returned stop
// function closes the listener and waits for in-flight CONNECT
// connections to finish before returning — call it as part of
// shutdown so the proxy's tunneled writes don't race the tunnel
// server's session cleanup.
//
// Idempotent in the sense that a second call replaces the URL (last
// writer wins), but production should only call once at startup.
func StartAgentExecProxy(ctx context.Context) (stop func(), err error) {
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("agent exec proxy: listen: %w", err)
	}
	u := &url.URL{Scheme: "http", Host: ln.Addr().String()}
	setAgentExecProxyURL(u)
	slog.Info("agent.exec_proxy_listening", "addr", u.Host)

	var inflight sync.WaitGroup

	// Tear-down on context cancel: closing the listener kicks Accept
	// out of its blocking read with a "use of closed network
	// connection" error, which the accept loop converts into a clean
	// stop.
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	go acceptLoop(ctx, ln, &inflight)

	stop = func() {
		_ = ln.Close()
		// Wait for any in-flight CONNECT-tunneled connections to
		// finish their io.Copy + Close cycle before we return.
		// Required so callers can sequence "stop the proxy, then tear
		// down the tunnel" without the proxy's still-finishing
		// remotedialer Writes racing the tunnel server's connection
		// close-out.
		inflight.Wait()
	}
	return stop, nil
}

func acceptLoop(ctx context.Context, ln net.Listener, inflight *sync.WaitGroup) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				slog.Info("agent.exec_proxy_stopped")
				return
			}
			slog.Warn("agent.exec_proxy_accept_error", "err", err)
			continue
		}
		inflight.Add(1)
		go func() {
			defer inflight.Done()
			handleAgentProxyConn(ctx, conn)
		}()
	}
}

func handleAgentProxyConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	// HTTP CONNECT is small and cheap to parse; no need for a
	// full http.Server here. Reading the request via http.ReadRequest
	// gives us standard Method/Host parsing without rolling our own
	// CRLF state machine.
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		// Bad/incomplete request; drop silently — could be a port
		// scanner, a stray client, or our own client racing close.
		return
	}
	if req.Method != http.MethodConnect {
		respondProxyError(conn, http.StatusMethodNotAllowed,
			"only CONNECT is supported by this proxy")
		return
	}

	cluster, ok := clusterNameFromCONNECTHost(req.Host)
	if !ok {
		respondProxyError(conn, http.StatusBadRequest,
			fmt.Sprintf("CONNECT host %q must be apiserver.<cluster>.tunnel[:port]", req.Host))
		return
	}

	dial, err := currentAgentLookup()(cluster)
	if err != nil {
		respondProxyError(conn, http.StatusBadGateway,
			fmt.Sprintf("no tunnel for cluster %q: %v", cluster, err))
		return
	}

	// The agent's LocalDial ignores the addr (it always routes to the
	// agent's local SA-injecting reverse proxy on 127.0.0.1:7443) so
	// passing req.Host through is symbolic. We pass it anyway so the
	// agent log line shows the requested target.
	upstream, err := dial(ctx, "tcp", req.Host)
	if err != nil {
		respondProxyError(conn, http.StatusBadGateway,
			fmt.Sprintf("tunnel dial: %v", err))
		return
	}
	defer func() { _ = upstream.Close() }()

	// CONNECT contract: 200 then bidirectional bytes. No body, no
	// Content-Length needed.
	if _, werr := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); werr != nil {
		return
	}

	// If the bufio reader buffered any bytes past the CONNECT request
	// (e.g. the client pipelined the WS upgrade), flush them upstream
	// before starting the bidirectional pipe — otherwise they'd be
	// stranded in the bufio buffer and the upgrade would hang.
	if buffered := br.Buffered(); buffered > 0 {
		drained := make([]byte, buffered)
		if n, _ := io.ReadFull(br, drained); n > 0 {
			if _, werr := upstream.Write(drained[:n]); werr != nil {
				return
			}
		}
	}

	pipeBidi(conn, upstream)
}

// pipeBidi copies bytes in both directions until either side closes.
// On either copy returning, both ends are nudged shut so the other
// goroutine unblocks promptly — a half-open conn is never useful here.
func pipeBidi(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	// sync.Once guards SetReadDeadline so the second pipe-goroutine
	// to finish doesn't race the first on the underlying conn's
	// state. Some net.Conn implementations (notably remotedialer's)
	// write to internal fields inside SetReadDeadline without their
	// own mutex; concurrent calls trip -race even though the writes
	// are functionally idempotent. One call is all we need anyway —
	// once both reads are unblocked, the second stop() would be a
	// no-op.
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			// SetReadDeadline in the past unblocks any in-flight Read
			// with an i/o timeout, letting the partner goroutine exit.
			// Close() alone races with the io.Copy loop on some
			// net.Conn impls.
			_ = a.SetReadDeadline(time.Unix(1, 0))
			_ = b.SetReadDeadline(time.Unix(1, 0))
		})
	}

	go func() {
		defer wg.Done()
		_, _ = io.Copy(a, b)
		stop()
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(b, a)
		stop()
	}()
	wg.Wait()
}

// clusterNameFromCONNECTHost extracts the cluster name from the
// CONNECT request's host:port target. We use the existing rest.Config.
// Host sentinel format: "apiserver.<cluster>.tunnel" (see
// agentHostSentinel). The CONNECT target adds a port (gorilla/websocket
// supplies :80 for ws://, :443 for wss://) — strip and parse.
//
// Returns false on any shape we don't recognise so the proxy can
// return a clear 400 instead of forwarding to a wrong cluster.
func clusterNameFromCONNECTHost(hostport string) (string, bool) {
	host := hostport
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	const prefix = "apiserver."
	const suffix = ".tunnel"
	if !strings.HasPrefix(host, prefix) || !strings.HasSuffix(host, suffix) {
		return "", false
	}
	name := host[len(prefix) : len(host)-len(suffix)]
	if name == "" {
		return "", false
	}
	return name, true
}

func respondProxyError(c net.Conn, status int, msg string) {
	body := msg + "\n"
	_, _ = fmt.Fprintf(c,
		"HTTP/1.1 %d %s\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, http.StatusText(status), len(body), body)
}
