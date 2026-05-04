package tunnel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewServer_PanicsWithoutAuthorizer(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when Authorizer is nil")
		}
	}()
	_ = NewServer(ServerOptions{})
}

func TestServer_ConnectedEmpty(t *testing.T) {
	s := NewServer(ServerOptions{Authorizer: allowAll})
	if got := s.Connected(); len(got) != 0 {
		t.Fatalf("Connected() = %v, want empty", got)
	}
}

func TestServer_LookupSession_NoAgent(t *testing.T) {
	s := NewServer(ServerOptions{Authorizer: allowAll})
	if s.LookupSession("nope") {
		t.Fatal("LookupSession on missing agent returned true")
	}
}

func TestServer_DialerFor_NoAgent_ReturnsErrNoSession(t *testing.T) {
	s := NewServer(ServerOptions{Authorizer: allowAll})
	_, err := s.DialerFor("nope")
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("DialerFor on missing agent: err = %v, want ErrNoSession", err)
	}
}

func TestAuthorizer_DeniedPath(t *testing.T) {
	denied := func(*http.Request) (string, bool, error) { return "agent-1", false, nil }
	s := NewServer(ServerOptions{Authorizer: denied})

	// Drive the WebSocket upgrade through the server's handler. We
	// don't need a real WebSocket library here — sending a non-WS
	// request will fail the upgrade and exercise the auth path.
	req := httptest.NewRequest("GET", "/connect", nil)
	rec := httptest.NewRecorder()
	s.Connect(rec, req)

	// remotedialer rejects the upgrade; what we want to confirm is
	// that the auth callback was even reached and didn't allow the
	// session. The connected map should remain empty.
	if got := s.Connected(); len(got) != 0 {
		t.Fatalf("denied auth still recorded a session: Connected() = %v", got)
	}
}

func TestObserver_FiresOnConnectAndDisconnect(t *testing.T) {
	events := make(chan SessionEvent, 4)
	s := NewServer(ServerOptions{
		Authorizer: allowAll,
		Observer:   func(e SessionEvent) { events <- e },
	})

	// Manually drive markConnected / watchDisconnect equivalents.
	// We can't fake remotedialer.HasSession from outside the
	// package, so we test the observer plumbing directly via the
	// helpers — the integration in transport_test.go covers the
	// real connect path.
	s.markConnected(context.Background(), "test-cluster")

	select {
	case e := <-events:
		if !e.Connected || e.ClusterName != "test-cluster" {
			t.Fatalf("connect event mismatch: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("connect event never fired")
	}

	if !contains(s.Connected(), "test-cluster") {
		t.Fatalf("Connected() missing test-cluster: %v", s.Connected())
	}
	if at := s.ConnectedAt("test-cluster"); at.IsZero() {
		t.Fatal("ConnectedAt returned zero for connected agent")
	}

	s.markDisconnected("test-cluster")
	select {
	case e := <-events:
		if e.Connected || e.ClusterName != "test-cluster" {
			t.Fatalf("disconnect event mismatch: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect event never fired")
	}
	if contains(s.Connected(), "test-cluster") {
		t.Fatalf("Connected() still lists test-cluster after disconnect: %v", s.Connected())
	}
}

func TestServer_Connect_RejectsNonWebSocket(t *testing.T) {
	// remotedialer.ServeHTTP returns 400 on non-WS requests; that's
	// the protective path we want to confirm doesn't accidentally
	// register a session.
	s := NewServer(ServerOptions{Authorizer: allowAll})
	srv := httptest.NewServer(http.HandlerFunc(s.Connect))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("plain GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 200 {
		t.Fatalf("non-WS request got 200; expected an upgrade rejection")
	}
}

// helpers --------------------------------------------------------------

func allowAll(r *http.Request) (string, bool, error) {
	// Tests that need a specific name plant it in the X-Agent-Name
	// header. Default to "default" so tests not exercising naming
	// don't have to bother.
	if name := r.Header.Get("X-Agent-Name"); name != "" {
		return strings.TrimSpace(name), true, nil
	}
	return "default", true, nil
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
