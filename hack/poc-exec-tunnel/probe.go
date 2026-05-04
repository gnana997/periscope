// probe.go — RFC 0004 Tier 2 end-to-end exec probe.
//
// Connects to the periscope SPA's exec WebSocket endpoint, runs a
// stdin echo round-trip via the agent tunnel, and exits 0 iff the
// expected bytes round-trip cleanly. Run from the host side of the
// kind harness; run.sh starts a kubectl port-forward to the periscope
// pod's :8080 before invoking this binary.
//
// Wire format (RFC 0001 6, see also internal/exec/session.go):
//
//	binary frames  →  stdin (in) / merged stdout+stderr (out)
//	text frames    →  JSON control: {type:hello}, {type:closed}, {type:resize}, {type:close}, ...
//
// We send a stdin token, look for it on stdout, then send a {type:close}
// and assert the {type:closed} frame reports exit 0.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
)

func main() {
	server := flag.String("server", "http://127.0.0.1:8080", "periscope SPA URL")
	cluster := flag.String("cluster", "kind-periscope-poc", "cluster name")
	namespace := flag.String("namespace", "default", "pod namespace")
	pod := flag.String("pod", "", "pod name (required)")
	cookie := flag.String("cookie", "dev", "periscope_session cookie value")
	timeout := flag.Duration("timeout", 30*time.Second, "overall probe timeout")
	flag.Parse()

	if *pod == "" {
		fmt.Fprintln(os.Stderr, "probe: --pod is required")
		os.Exit(2)
	}

	if err := run(*server, *cluster, *namespace, *pod, *cookie, *timeout); err != nil {
		fmt.Fprintf(os.Stderr, "probe: FAIL: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("probe: PASS")
}

func run(server, cluster, namespace, pod, cookie string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	wsURL := strings.Replace(server, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL = fmt.Sprintf("%s/api/clusters/%s/pods/%s/%s/exec?tty=true",
		wsURL, cluster, namespace, pod)

	header := http.Header{}
	header.Set("Cookie", "periscope_session="+cookie)
	header.Set("Origin", server)

	fmt.Printf("probe: dialing %s\n", wsURL)
	conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("ws dial: %w (status %d: %s)",
				err, resp.StatusCode, string(body))
		}
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.CloseNow()

	// Step 1: read the hello frame so we know the apiserver-side exec
	// stream is up. session.go writes this BEFORE forwarding any user
	// bytes.
	if err := readHello(ctx, conn); err != nil {
		return fmt.Errorf("hello: %w", err)
	}
	fmt.Println("probe: hello received")

	// Step 2: send a stdin token, expect it on stdout. The token is
	// long enough not to collide with any shell prompt prefix.
	const token = "PERISCOPE-POC-OK-d4c3b2a1"
	stdin := []byte("echo " + token + "\n")
	if err := conn.Write(ctx, websocket.MessageBinary, stdin); err != nil {
		return fmt.Errorf("write stdin: %w", err)
	}
	fmt.Printf("probe: stdin sent (%d bytes)\n", len(stdin))

	if err := readUntilStdoutContains(ctx, conn, token, 10*time.Second); err != nil {
		return fmt.Errorf("await stdout: %w", err)
	}
	fmt.Println("probe: token observed on stdout")

	// Step 3: half-close stdin via {type:close} → expect {type:closed}.
	closeMsg, _ := json.Marshal(map[string]string{"type": "close"})
	if err := conn.Write(ctx, websocket.MessageText, closeMsg); err != nil {
		return fmt.Errorf("write close: %w", err)
	}
	fmt.Println("probe: close sent")

	if err := readClosedFrame(ctx, conn, 5*time.Second); err != nil {
		return fmt.Errorf("await closed: %w", err)
	}
	fmt.Println("probe: closed frame received cleanly")

	return nil
}

func readHello(ctx context.Context, conn *websocket.Conn) error {
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		mt, data, err := conn.Read(rctx)
		if err != nil {
			return err
		}
		if mt == websocket.MessageText {
			var msg struct {
				Type string `json:"type"`
			}
			if jerr := json.Unmarshal(data, &msg); jerr != nil {
				return fmt.Errorf("decode control: %w (raw=%q)", jerr, data)
			}
			switch msg.Type {
			case "hello":
				return nil
			case "error":
				return fmt.Errorf("server returned error frame: %s", string(data))
			default:
				// Some other control frame; keep reading.
				continue
			}
		}
		// Binary frames before hello are unexpected but harmless;
		// keep waiting.
	}
}

func readUntilStdoutContains(ctx context.Context, conn *websocket.Conn, want string, deadline time.Duration) error {
	rctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	var buf strings.Builder
	for {
		mt, data, err := conn.Read(rctx)
		if err != nil {
			return fmt.Errorf("read: %w (got so far: %q)", err, buf.String())
		}
		if mt == websocket.MessageBinary {
			buf.Write(data)
			if strings.Contains(buf.String(), want) {
				return nil
			}
		}
		// Ignore control frames at this stage.
	}
}

func readClosedFrame(ctx context.Context, conn *websocket.Conn, deadline time.Duration) error {
	rctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	for {
		mt, data, err := conn.Read(rctx)
		if err != nil {
			// Server may close the WS as part of the closed handshake;
			// that's fine if we've already seen {type:closed}.
			if errors.Is(err, io.EOF) || websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return nil
			}
			return err
		}
		if mt == websocket.MessageText {
			var msg struct {
				Type     string `json:"type"`
				ExitCode *int   `json:"exitCode,omitempty"`
				Reason   string `json:"reason,omitempty"`
			}
			if jerr := json.Unmarshal(data, &msg); jerr != nil {
				return fmt.Errorf("decode control: %w (raw=%q)", jerr, data)
			}
			if msg.Type == "closed" {
				if msg.ExitCode != nil && *msg.ExitCode != 0 {
					return fmt.Errorf("non-zero exit code %d (reason=%q)",
						*msg.ExitCode, msg.Reason)
				}
				return nil
			}
			if msg.Type == "error" {
				return fmt.Errorf("server error frame: %s", string(data))
			}
		}
	}
}
