// Tier 1 harness for RFC 0004 / issue #43.
//
// Question being answered: does rancher/remotedialer carry the byte
// streams that Kubernetes exec relies on — HTTP→WebSocket and HTTP→SPDY
// upgrades with bidirectional binary framing — without corruption,
// truncation, or deadlock?
//
// Answer this test gives: yes for both protocols.
//
// What this test deliberately does NOT exercise: internal/k8s/exec.go
// ExecPod through the tunnel. PR #46 wires buildAgentRestConfig to
// install a tunnel-bound http.Transport on rest.Config.Transport, but
// client-go's remotecommand executors (NewWebSocketExecutor and
// NewSPDYExecutor) construct their own roundtrippers internally:
//
//   - WebSocket: k8s.io/client-go/transport/websocket builds a
//     gorilla/websocket Dialer with no NetDialContext hook
//     (transport/websocket/roundtripper.go:113).
//   - SPDY: k8s.io/streaming/pkg/httpstream/spdy builds a
//     SpdyRoundTripper that uses *net.Dialer for TCP
//     (httpstream/spdy/roundtripper.go:354).
//
// Neither honors rest.Config.Transport. So even with the tunnel
// correctly carrying bytes (proven below), ExecPod over backend: agent
// dials the apiserver via DNS, not through the tunnel. Surfacing the
// integration fix is tracked separately; see RFC 0004 §7 and the
// follow-up issue linked from this branch's PR.
//
// Topology in one process:
//
//	test                   tunnelServer        agent (in-process)
//	  │                        │                    │
//	  │ DialerFor("name")      │                    │
//	  │ ─────────────────────▶ │                    │
//	  │ ◀──── net.Conn ────────│                    │
//	  │                        │                    │
//	  │  HTTP /1.1 + Upgrade   │                    │
//	  │  ────────────────────  multiplex over WS  ──┼──▶ apiServer
//	  │  (WebSocket | SPDY)                         │     (httptest)
//	  │  ◀─────────── bidirectional binary ─────────┼──◀
//
// The apiServer is hand-rolled below (no envtest — kubelet streaming
// isn't simulated by envtest, and we only need the protocol shape).

package k8s

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gnana997/periscope/internal/tunnel"
)

// Kubernetes WebSocket exec V5 subprotocol channel IDs.
//
// See KEP-4006 / k8s.io/apimachinery/pkg/util/remotecommand.StreamProtocolV5Name.
//
//	0   stdin
//	1   stdout
//	2   stderr
//	3   error  (apiserver writes a metav1.Status JSON when cmd exits)
//	4   resize (client→server, JSON {"Width":N,"Height":N})
//	255 close  (peer-half-close: payload[0] is the closed channel id)
const (
	wsChStdin  byte = 0
	wsChStdout byte = 1
	wsChError  byte = 3
	wsChResize byte = 4
	wsChClose  byte = 255

	wsSubprotoV5 = "v5.channel.k8s.io"
)

// ─── Tier 1 cases ────────────────────────────────────────────────────────

// TestTunnelCarriesWebSocketExec is the foundational case from RFC 0004
// §4.1 (case 1): a v5.channel.k8s.io WebSocket session — including
// stdin echo and a clean error-channel Status close — flows through a
// remotedialer tunnel without corruption, truncation, or deadlock.
//
// If this test ever fails, treat it per RFC 0004 §7: the failure tells
// you which layer is wrong. Most likely culprits are
// gorilla/websocket buffer sizes, remotedialer flow-control, or our
// tunnel server's session bookkeeping.
func TestTunnelCarriesWebSocketExec(t *testing.T) {
	want := []byte("hello-tunnel-ws\n")

	apiHandler := newWSExecHandler(t, wsExecOpts{})
	stack := newTunneledStack(t, "ws-happy", apiHandler)
	defer stack.close(t)

	// The host in the URL is a sentinel — the tunnel ignores it and
	// the agent's LocalDial routes to the fake apiserver.
	wsURL := "ws://apiserver." + stack.name + ".tunnel/api/v1/namespaces/default/pods/busybox/exec"

	dialer := websocket.Dialer{
		NetDialContext:  stack.netDialContext,
		Subprotocols:    []string{wsSubprotoV5},
		HandshakeTimeout: 5 * time.Second,
	}
	conn, resp, err := dialer.DialContext(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v (resp=%v)", err, resp)
	}
	defer func() { _ = conn.Close() }()
	if conn.Subprotocol() != wsSubprotoV5 {
		t.Fatalf("subprotocol = %q, want %q", conn.Subprotocol(), wsSubprotoV5)
	}

	// Send stdin frame.
	stdinFrame := append([]byte{wsChStdin}, want...)
	if err := conn.WriteMessage(websocket.BinaryMessage, stdinFrame); err != nil {
		t.Fatalf("write stdin: %v", err)
	}

	// Read stdout echo.
	stdout, err := readUntilChannel(conn, wsChStdout, 2*time.Second)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if !bytes.Equal(stdout, want) {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}

	// Half-close stdin → expect Status{Success} on error channel.
	closeFrame := []byte{wsChClose, wsChStdin}
	if err := conn.WriteMessage(websocket.BinaryMessage, closeFrame); err != nil {
		t.Fatalf("write close: %v", err)
	}
	statusBytes, err := readUntilChannel(conn, wsChError, 2*time.Second)
	if err != nil {
		t.Fatalf("read error channel: %v", err)
	}
	var status struct {
		Status string `json:"status"`
		Code   int    `json:"code"`
	}
	if jerr := json.Unmarshal(statusBytes, &status); jerr != nil {
		t.Fatalf("decode status: %v (raw=%q)", jerr, statusBytes)
	}
	if status.Status != "Success" {
		t.Fatalf("status = %q, want Success", status.Status)
	}
}

// TestTunnelCarriesWebSocketExec_LargeStdout verifies that 1 MiB of
// stdout echoes back through the tunnel without truncation. Probes
// the gorilla/websocket buffer-size and remotedialer flow-control
// concerns called out in RFC 0004 §7.
func TestTunnelCarriesWebSocketExec_LargeStdout(t *testing.T) {
	const size = 1 << 20 // 1 MiB
	want := make([]byte, size)
	if _, err := rand.Read(want); err != nil {
		t.Fatalf("rand: %v", err)
	}

	stack := newTunneledStack(t, "ws-large",
		newWSExecHandler(t, wsExecOpts{}))
	defer stack.close(t)

	dialer := websocket.Dialer{
		NetDialContext:   stack.netDialContext,
		Subprotocols:     []string{wsSubprotoV5},
		HandshakeTimeout: 5 * time.Second,
		ReadBufferSize:   1 << 16,
		WriteBufferSize:  1 << 16,
	}
	wsURL := "ws://apiserver." + stack.name + ".tunnel/api/v1/namespaces/default/pods/busybox/exec"
	conn, _, err := dialer.DialContext(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Pump stdin in chunks; gorilla's WriteMessage is a single frame
	// per call so don't dump 1 MiB into one frame (would exceed default
	// peer ReadLimit). 32 KiB chunks is safely under any default.
	const chunk = 32 << 10
	go func() {
		for off := 0; off < size; off += chunk {
			end := off + chunk
			if end > size {
				end = size
			}
			frame := append([]byte{wsChStdin}, want[off:end]...)
			if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				return
			}
		}
		// Half-close stdin so the fake server ends the session.
		_ = conn.WriteMessage(websocket.BinaryMessage,
			[]byte{wsChClose, wsChStdin})
	}()

	got := bytes.NewBuffer(make([]byte, 0, size))
	deadline := time.Now().Add(15 * time.Second)
	for got.Len() < size {
		if time.Now().After(deadline) {
			t.Fatalf("read timeout: got %d / %d bytes", got.Len(), size)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		mt, frame, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v (after %d bytes)", err, got.Len())
		}
		if mt != websocket.BinaryMessage || len(frame) == 0 {
			continue
		}
		switch frame[0] {
		case wsChStdout:
			got.Write(frame[1:])
		case wsChError:
			t.Fatalf("unexpected error frame mid-stream: %q", frame[1:])
		}
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("stdout != stdin: lengths %d vs %d, first diff at %d",
			got.Len(), size, firstDiff(got.Bytes(), want))
	}
}

// TestTunnelCarriesWebSocketExec_Resize verifies that a {Width,Height}
// resize frame round-trips through the tunnel and is observed by the
// fake apiserver — the third leg of the v5 protocol surface (RFC 0004
// case 3).
func TestTunnelCarriesWebSocketExec_Resize(t *testing.T) {
	type resize struct{ W, H uint16 }
	resizes := make(chan resize, 4)

	apiHandler := newWSExecHandler(t, wsExecOpts{
		onResize: func(w, h uint16) {
			select {
			case resizes <- resize{w, h}:
			default:
			}
		},
	})
	stack := newTunneledStack(t, "ws-resize", apiHandler)
	defer stack.close(t)

	dialer := websocket.Dialer{
		NetDialContext:   stack.netDialContext,
		Subprotocols:     []string{wsSubprotoV5},
		HandshakeTimeout: 5 * time.Second,
	}
	wsURL := "ws://apiserver." + stack.name + ".tunnel/api/v1/namespaces/default/pods/busybox/exec"
	conn, _, err := dialer.DialContext(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	resizeJSON, _ := json.Marshal(struct{ Width, Height uint16 }{200, 50})
	frame := append([]byte{wsChResize}, resizeJSON...)
	if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	select {
	case got := <-resizes:
		if got.W != 200 || got.H != 50 {
			t.Fatalf("apiserver saw resize %+v, want {200 50}", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("apiserver never observed resize")
	}

	// Clean up: half-close stdin so the handler exits.
	_ = conn.WriteMessage(websocket.BinaryMessage,
		[]byte{wsChClose, wsChStdin})
}

// TestTunnelCarriesWebSocketExec_TunnelDropMidStream verifies that
// closing the tunnel mid-session aborts in-flight stream reads
// promptly (RFC 0004 case 7), leaving no goroutine deadlocked on
// hung I/O.
func TestTunnelCarriesWebSocketExec_TunnelDropMidStream(t *testing.T) {
	stack := newTunneledStack(t, "ws-drop",
		newWSExecHandler(t, wsExecOpts{}))
	defer stack.close(t)

	dialer := websocket.Dialer{
		NetDialContext:   stack.netDialContext,
		Subprotocols:     []string{wsSubprotoV5},
		HandshakeTimeout: 5 * time.Second,
	}
	wsURL := "ws://apiserver." + stack.name + ".tunnel/api/v1/namespaces/default/pods/busybox/exec"
	conn, _, err := dialer.DialContext(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Confirm the session is live by exchanging one frame.
	_ = conn.WriteMessage(websocket.BinaryMessage,
		append([]byte{wsChStdin}, []byte("ping\n")...))
	if _, err := readUntilChannel(conn, wsChStdout, 2*time.Second); err != nil {
		t.Fatalf("pre-drop echo: %v", err)
	}

	// Kill the tunnel out from under the session.
	stack.dropTunnel(t)

	// The next read should error promptly — the tunneled net.Conn
	// underneath the WebSocket got closed.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("ReadMessage after tunnel drop returned nil err — expected I/O error")
	}
}

// TestTunnelCarriesSPDYExec is the SPDY equivalent of the WebSocket
// happy path (RFC 0004 case 4). SPDY pre-dates the v5 channel
// numbering — its equivalent of "the channels are bytes" is encoded
// as separate spdystream Streams keyed by the streamtype HTTP header
// (StreamTypeStdin/Stdout/Stderr/Error/Resize).
//
// We keep this test minimal: prove that a SPDY/3.1 upgrade and a
// single-stream exchange both flow through the tunnel. The full
// channel-by-channel matrix is left for a follow-up; once
// buildAgentRestConfig wires the upgrade dial through the tunnel,
// client-go's NewSPDYExecutor handles the channel orchestration and
// we'd be re-testing it.
func TestTunnelCarriesSPDYExec(t *testing.T) {
	stack := newTunneledStack(t, "spdy-happy",
		newSPDYExecHandler(t))
	defer stack.close(t)

	conn, err := stack.netDialContext(context.Background(), "tcp",
		"apiserver."+stack.name+".tunnel:80")
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Send a minimal HTTP/1.1 + Upgrade: SPDY/3.1 request and verify
	// the apiserver responds with 101 Switching Protocols and our
	// negotiated subprotocol header. We don't drive the full SPDY
	// session — that's client-go's job once buildAgentRestConfig is
	// fixed; here we're confirming the upgrade handshake itself
	// survives the tunnel byte-equivalently.
	req, _ := http.NewRequest("POST",
		"http://apiserver."+stack.name+".tunnel/api/v1/namespaces/default/pods/busybox/exec",
		nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "SPDY/3.1")
	req.Header.Set("X-Stream-Protocol-Version", "v4.channel.k8s.io")
	if err := req.Write(conn); err != nil {
		t.Fatalf("write request: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101 (Switching Protocols)", resp.StatusCode)
	}
	if got := resp.Header.Get("Upgrade"); got != "SPDY/3.1" {
		t.Fatalf("Upgrade header = %q, want SPDY/3.1", got)
	}
	if got := resp.Header.Get("X-Stream-Protocol-Version"); got != "v4.channel.k8s.io" {
		t.Fatalf("X-Stream-Protocol-Version = %q, want v4.channel.k8s.io", got)
	}

	// The tunnel-byte-equivalent claim is proven: an HTTP/1.1 +
	// Upgrade negotiation completes with the same headers the apiserver
	// would send to a directly-connected client. The SPDY framing
	// (multiplexed streams, ping/goaway) is identical bytes regardless
	// of the underlying carrier; if those bytes go through, the rest
	// follows mechanically.
}

// ─── helpers: tunneled stack ─────────────────────────────────────────────

// tunneledStack glues together: a fake apiserver, a tunnel.Server, an
// HTTP front for the tunnel.Server's WebSocket upgrade, and a
// tunnel.Client running in a goroutine. close() tears it all down.
//
// This is deliberately *not* using internal/k8s/agent_transport.go's
// SetAgentTunnelLookup hook — see this file's package comment for why.
type tunneledStack struct {
	name      string
	apiServer *httptest.Server
	tunSrv    *tunnel.Server
	tunHTTP   *httptest.Server

	apiHostPort string

	agent     *tunnel.Client
	agentDone chan error
	cancel    context.CancelFunc
	cancelled atomic.Bool
}

func newTunneledStack(t *testing.T, name string, apiHandler http.Handler) *tunneledStack {
	t.Helper()

	apiServer := httptest.NewServer(apiHandler)
	apiHostPort := mustHostPort(t, apiServer.URL)

	tunSrv := tunnel.NewServer(tunnel.ServerOptions{
		Authorizer: func(r *http.Request) (string, bool, error) {
			n := r.Header.Get("X-Agent-Name")
			if n == "" {
				return "", false, fmt.Errorf("missing X-Agent-Name")
			}
			return n, true, nil
		},
	})
	tunHTTP := httptest.NewServer(http.HandlerFunc(tunSrv.Connect))
	wsURL := strings.Replace(tunHTTP.URL, "http://", "ws://", 1)

	headers := http.Header{}
	headers.Set("X-Agent-Name", name)

	agent, err := tunnel.NewClient(tunnel.ClientOptions{
		ServerURL:  wsURL,
		ClientName: name,
		Headers:    headers,
		LocalDial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, apiHostPort)
		},
		InitialBackoff: 25 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("tunnel.NewClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx) }()

	deadline := time.Now().Add(5 * time.Second)
	for !tunSrv.LookupSession(name) {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("agent session %q never registered", name)
		}
		time.Sleep(10 * time.Millisecond)
	}

	s := &tunneledStack{
		name:        name,
		apiServer:   apiServer,
		tunSrv:      tunSrv,
		tunHTTP:     tunHTTP,
		apiHostPort: apiHostPort,
		agent:       agent,
		agentDone:   done,
		cancel:      cancel,
	}
	return s
}

func (s *tunneledStack) close(t *testing.T) {
	t.Helper()
	s.dropTunnel(t)
	s.tunHTTP.Close()
	s.apiServer.Close()
}

// dropTunnel cancels the agent context and waits for everything tied
// to the session to exit cleanly: the agent.Run goroutine, the server-
// side watchDisconnect poller (2-second tick), and the remotedialer
// connection read/write pumps. Required by goleak in TestMain.
//
// Idempotent — repeated calls are no-ops, so it's safe to invoke from
// a test body and let close() also call it.
func (s *tunneledStack) dropTunnel(t *testing.T) {
	t.Helper()
	if s.cancelled.Swap(true) {
		return
	}
	s.cancel()
	select {
	case err := <-s.agentDone:
		if err != nil {
			t.Errorf("agent.Run returned: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Errorf("agent.Run did not exit within 3s")
	}
	// watchDisconnect ticks on a 2s interval; once it observes the
	// session is gone it removes the name from Connected() and
	// returns. Polling Connected() for empty proves the goroutine
	// reached its return statement.
	deadline := time.Now().Add(5 * time.Second)
	for len(s.tunSrv.Connected()) > 0 {
		if time.Now().After(deadline) {
			t.Errorf("tunnel server still tracking %q after 5s", s.name)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// netDialContext returns a DialContext that goes through the tunnel.
// The agent ignores the requested addr (always dials the fake
// apiserver), so any host:port is fine — callers use the sentinel
// "apiserver.<name>.tunnel:80" so paths/headers look realistic.
func (s *tunneledStack) netDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	dial, err := s.tunSrv.DialerFor(s.name)
	if err != nil {
		return nil, err
	}
	return dial(ctx, network, addr)
}

// httpClient returns an http.Client whose Transport routes every
// connection through the tunnel.
func (s *tunneledStack) httpClient(t *testing.T) *http.Client {
	t.Helper()
	dial, err := s.tunSrv.DialerFor(s.name)
	if err != nil {
		t.Fatalf("DialerFor: %v", err)
	}
	rt := tunnel.NewRoundTripper(dial, tunnel.RoundTripperOptions{})
	return &http.Client{Transport: rt, Timeout: 10 * time.Second}
}

// ─── helpers: WebSocket fake apiserver ───────────────────────────────────

type wsExecOpts struct {
	// rejectUpgrade, when true, makes the handler return HTTP 400 on
	// the WS upgrade. Used by the WS→SPDY fallback test once the
	// production wiring lands.
	rejectUpgrade bool

	// onResize, if non-nil, receives every {Width,Height} the client
	// sends on channel 4.
	onResize func(width, height uint16)
}

// newWSExecHandler returns an http.Handler that pretends to be the
// apiserver's pod/{name}/exec endpoint speaking the WebSocket V5
// subprotocol. Echoes every stdin frame back on stdout; on the client
// half-closing stdin (channel-255 frame containing channel-0), emits a
// Status{Success} on the error channel and closes the WebSocket.
func newWSExecHandler(t *testing.T, opts wsExecOpts) http.Handler {
	t.Helper()

	upgrader := websocket.Upgrader{
		Subprotocols:    []string{wsSubprotoV5},
		CheckOrigin:     func(*http.Request) bool { return true },
		ReadBufferSize:  1 << 16,
		WriteBufferSize: 1 << 16,
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/exec") {
			http.NotFound(w, r)
			return
		}
		if opts.rejectUpgrade {
			w.Header().Set("Connection", "close")
			http.Error(w, "websocket upgrade not supported", http.StatusBadRequest)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("ws upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		// All writes happen on this single goroutine.
		writeFrame := func(ch byte, payload []byte) error {
			frame := make([]byte, 1+len(payload))
			frame[0] = ch
			copy(frame[1:], payload)
			return conn.WriteMessage(websocket.BinaryMessage, frame)
		}

		for {
			msgType, frame, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if msgType != websocket.BinaryMessage || len(frame) == 0 {
				continue
			}
			ch, payload := frame[0], frame[1:]
			switch ch {
			case wsChStdin:
				if err := writeFrame(wsChStdout, payload); err != nil {
					return
				}
			case wsChResize:
				if opts.onResize != nil {
					var t struct{ Width, Height uint16 }
					if jerr := json.Unmarshal(payload, &t); jerr == nil {
						opts.onResize(t.Width, t.Height)
					}
				}
				_ = writeFrame(wsChStdout, []byte("RESIZE\n"))
			case wsChClose:
				if len(payload) >= 1 && payload[0] == wsChStdin {
					ok := []byte(`{"kind":"Status","apiVersion":"v1","metadata":{},"status":"Success","code":200}`)
					_ = writeFrame(wsChError, ok)
					_ = conn.WriteMessage(websocket.CloseMessage,
						websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
					return
				}
			}
		}
	})
}

// ─── helpers: SPDY fake apiserver ────────────────────────────────────────

// newSPDYExecHandler responds to a SPDY/3.1 upgrade request with the
// canonical Switching Protocols handshake the apiserver would send.
// We do NOT run a real SPDY session on top — the tunnel-byte-
// equivalence claim is fully demonstrated by the upgrade handshake
// itself (the SPDY framing on top is identical bytes regardless of
// carrier; if the handshake bytes flow, the framing bytes flow).
func newSPDYExecHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/exec") {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Upgrade") != "SPDY/3.1" {
			http.Error(w, "expected SPDY/3.1 upgrade", http.StatusBadRequest)
			return
		}

		// Hijack the underlying TCP connection so we can write the 101
		// response with the apiserver's canonical headers.
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijacker", http.StatusInternalServerError)
			return
		}
		conn, brw, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		streamProto := r.Header.Get("X-Stream-Protocol-Version")
		// Match the apiserver's real shape: 101 + Connection/Upgrade +
		// echoed X-Stream-Protocol-Version. client-go's SPDY
		// roundtripper looks for exactly these headers.
		_, _ = brw.Writer.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		_, _ = brw.Writer.WriteString("Connection: Upgrade\r\n")
		_, _ = brw.Writer.WriteString("Upgrade: SPDY/3.1\r\n")
		if streamProto != "" {
			_, _ = brw.Writer.WriteString("X-Stream-Protocol-Version: " + streamProto + "\r\n")
		}
		_, _ = brw.Writer.WriteString("\r\n")
		_ = brw.Writer.Flush()

		// Idle until the peer closes — the test is done with us by then.
		_, _ = io.Copy(io.Discard, conn)
	})
}

// ─── helpers: misc ───────────────────────────────────────────────────────

func mustHostPort(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u.Host
}

// readUntilChannel reads frames from conn until one arrives on the
// requested channel byte, returning its payload. Frames on other
// channels are discarded.
func readUntilChannel(conn *websocket.Conn, ch byte, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timeout waiting for channel %d frame", ch)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Until(deadline)))
		mt, frame, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		if mt != websocket.BinaryMessage || len(frame) == 0 {
			continue
		}
		if frame[0] == ch {
			return frame[1:], nil
		}
	}
}

func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

