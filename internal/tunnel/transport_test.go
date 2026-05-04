package tunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestEndToEndRoundtrip proves the foundational claim:
//
//	browser -> Server.RoundTripper(name) -> tunnel -> Client.LocalDial -> apiserver
//
// works end-to-end on a single machine with no real K8s involved.
//
// Topology:
//   - apiServer: stdlib httptest.Server returning a fixed JSON
//     payload — stands in for the cluster's real apiserver.
//   - tunnelServer: our tunnel.Server hosted by another httptest.Server.
//   - agentClient: our tunnel.Client running in a goroutine, dialing
//     out to tunnelServer and configured with apiServer as its
//     LocalDial target.
//   - caller: an http.Client whose Transport is the RoundTripper we
//     get back from tunnelServer.DialerFor("test-cluster").
//
// A successful roundtrip means: caller fires GET to ANY host, request
// flows through tunnelServer -> tunnel -> agent -> apiServer, response
// flows back. We assert that the body the caller sees equals what the
// fake apiserver wrote.
//
// If this test breaks, no other test in the package matters until it's
// fixed — it's load-bearing for the entire #42 epic.
func TestEndToEndRoundtrip(t *testing.T) {
	const (
		clusterName  = "test-cluster"
		expectedBody = `{"kind":"PodList","items":[{"metadata":{"name":"alice"}}]}`
	)

	// 1. Fake apiserver — what the agent will dial locally.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the impersonation header survived the trip — if the
		// tunnel ever drops headers in transit we want to know.
		if got := r.Header.Get("Impersonate-User"); got != "alice@example.com" {
			t.Errorf("apiserver: Impersonate-User = %q, want alice@example.com", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, expectedBody)
	}))
	defer apiServer.Close()

	apiHost := mustHostPort(t, apiServer.URL)

	// 2. Tunnel server.
	tServer := NewServer(ServerOptions{Authorizer: nameFromHeader})

	// Host the tunnel server on a real listener so the agent can WS-
	// upgrade against it. httptest.Server gives us a free port.
	tHTTP := httptest.NewServer(http.HandlerFunc(tServer.Connect))
	defer tHTTP.Close()

	// Build the ws:// URL the agent dials.
	wsURL := strings.Replace(tHTTP.URL, "http://", "ws://", 1)

	// 3. Agent — dials the tunnel server, advertises name via header,
	//    fulfils local dials by hitting our fake apiserver.
	agentCtx, agentCancel := context.WithCancel(context.Background())
	defer agentCancel()

	headers := http.Header{}
	headers.Set("X-Agent-Name", clusterName)

	client, err := NewClient(ClientOptions{
		ServerURL:      wsURL,
		ClientName:     clusterName,
		LocalDial:      dialFixedHost(apiHost),
		Headers:        headers,
		InitialBackoff: 50 * time.Millisecond,
		MaxBackoff:     200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// Run agent in a goroutine; main goroutine waits for the session
	// to register before sending the test request.
	agentDone := make(chan error, 1)
	go func() { agentDone <- client.Run(agentCtx) }()

	// Wait up to 5s for the session to appear server-side.
	deadline := time.Now().Add(5 * time.Second)
	for !tServer.LookupSession(clusterName) {
		if time.Now().After(deadline) {
			t.Fatalf("session never registered for %q", clusterName)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 4. Caller — http.Client whose transport routes via the tunnel.
	dialer, err := tServer.DialerFor(clusterName)
	if err != nil {
		t.Fatalf("DialerFor: %v", err)
	}
	rt := NewRoundTripper(dialer, RoundTripperOptions{})
	httpClient := &http.Client{Transport: rt, Timeout: 5 * time.Second}

	// The host in the URL doesn't matter — the agent ignores it and
	// dials apiHost. Use the realistic-looking sentinel that #42d
	// will codify.
	req, _ := http.NewRequest("GET",
		"http://apiserver."+clusterName+".tunnel/api/v1/pods", nil)
	req.Header.Set("Impersonate-User", "alice@example.com")

	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != expectedBody {
		t.Fatalf("body = %q, want %q", body, expectedBody)
	}

	// 5. Disconnect — cancel agent ctx, verify session goes away.
	agentCancel()
	select {
	case err := <-agentDone:
		if err != nil {
			t.Fatalf("agent.Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent.Run did not return after ctx cancel")
	}

	// Server's watchDisconnect polls on a 2s tick; give it enough
	// time to notice the closed session.
	deadline = time.Now().Add(5 * time.Second)
	for tServer.LookupSession(clusterName) {
		if time.Now().After(deadline) {
			t.Fatalf("session %q lingered after agent stopped", clusterName)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// New DialerFor calls should now fail cleanly.
	if _, err := tServer.DialerFor(clusterName); err == nil {
		t.Fatal("DialerFor after disconnect: err = nil, want ErrNoSession")
	}
}

// helpers --------------------------------------------------------------

// nameFromHeader is the test authorizer: trust whatever the agent
// claims in X-Agent-Name. Production replaces this with the mTLS
// validator landing in #42b.
func nameFromHeader(r *http.Request) (string, bool, error) {
	name := r.Header.Get("X-Agent-Name")
	if name == "" {
		return "", false, nil
	}
	return name, true, nil
}

// dialFixedHost returns a LocalDialer that ignores the requested
// address and always dials the given host:port. Stands in for the
// agent's "always dial the local apiserver" behavior.
func dialFixedHost(hostPort string) LocalDialer {
	return func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, hostPort)
	}
}

func mustHostPort(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u.Host
}
