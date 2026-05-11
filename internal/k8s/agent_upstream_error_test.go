package k8s

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

// stubRT is a minimal http.RoundTripper that returns the configured
// response/error verbatim. Used by the wrapAgentUpstream tests so we
// don't have to bring up a real server.
type stubRT struct {
	resp *http.Response
	err  error
}

func (s *stubRT) RoundTrip(_ *http.Request) (*http.Response, error) {
	return s.resp, s.err
}

func mkResponse(status int, contentType, body string) *http.Response {
	h := make(http.Header)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestWrapAgentUpstream_HappyPathPasses2xx(t *testing.T) {
	upstream := &stubRT{resp: mkResponse(http.StatusOK, "application/json", `{"kind":"PodList"}`)}
	rt := wrapAgentUpstream(upstream)
	resp, err := rt.RoundTrip(must(http.NewRequest("GET", "http://x/", nil)))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "PodList") {
		t.Fatalf("body = %q (lost during pass-through)", body)
	}
}

func TestWrapAgentUpstream_RecognisesEnvelope(t *testing.T) {
	wireBody := `{
		"code":"E_AGENT_UPSTREAM",
		"category":"network",
		"cluster":"pre-prod",
		"message":"agent could not reach the cluster's apiserver",
		"detail":"dial tcp 10.100.0.1:443: connect: connection refused",
		"trace_id":"req-abc"
	}`
	upstream := &stubRT{resp: mkResponse(http.StatusBadGateway, "application/json", wireBody)}
	rt := wrapAgentUpstream(upstream)

	resp, err := rt.RoundTrip(must(http.NewRequest("GET", "http://x/", nil)))
	if err == nil {
		t.Fatalf("expected typed error, got resp=%v", resp)
	}
	aue, ok := AsAgentUpstreamError(err)
	if !ok {
		t.Fatalf("err = %v (%T), expected *AgentUpstreamError", err, err)
	}
	if aue.Category != "network" {
		t.Errorf("category = %q, want network", aue.Category)
	}
	if aue.Cluster != "pre-prod" {
		t.Errorf("cluster = %q, want pre-prod", aue.Cluster)
	}
	if aue.TraceID != "req-abc" {
		t.Errorf("trace_id = %q, want req-abc", aue.TraceID)
	}
	if aue.HTTPStatus != http.StatusBadGateway {
		t.Errorf("http status = %d, want 502", aue.HTTPStatus)
	}
}

func TestWrapAgentUpstream_PassesThroughGeneric5xx(t *testing.T) {
	// 502 with non-matching body — wrapper must NOT swallow it,
	// so the caller's normal status handling continues to work.
	upstream := &stubRT{resp: mkResponse(http.StatusBadGateway, "text/plain", "nginx 502 from somewhere upstream")}
	rt := wrapAgentUpstream(upstream)

	resp, err := rt.RoundTrip(must(http.NewRequest("GET", "http://x/", nil)))
	if err != nil {
		t.Fatalf("err = %v, want pass-through", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "nginx") {
		t.Errorf("body lost during non-matching pass-through: %q", body)
	}
}

func TestWrapAgentUpstream_PassesThroughTransportError(t *testing.T) {
	upstream := &stubRT{err: errors.New("dial tcp: i/o timeout")}
	rt := wrapAgentUpstream(upstream)

	_, err := rt.RoundTrip(must(http.NewRequest("GET", "http://x/", nil)))
	if err == nil || !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("err = %v, expected pass-through of upstream transport error", err)
	}
	if _, ok := AsAgentUpstreamError(err); ok {
		t.Errorf("transport-level errors should not be promoted to *AgentUpstreamError")
	}
}

func TestWrapAgentUpstream_IgnoresEnvelopeWithUnknownCode(t *testing.T) {
	// Different `code` — must not match. Defends against future agent
	// versions emitting different envelope codes that the central
	// server doesn't yet understand.
	upstream := &stubRT{resp: mkResponse(http.StatusBadGateway, "application/json",
		`{"code":"E_SOMETHING_ELSE","category":"network"}`)}
	rt := wrapAgentUpstream(upstream)

	resp, err := rt.RoundTrip(must(http.NewRequest("GET", "http://x/", nil)))
	if err != nil {
		t.Fatalf("err = %v, expected pass-through for unknown code", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
}

func must(req *http.Request, err error) *http.Request {
	if err != nil {
		panic(err)
	}
	return req
}

// TestParseAgentUpstreamBody covers the standalone parser used by the
// CONNECT-proxy-side peeker (when we add bytes-level interception in a
// future iteration) and asserts conservative behavior on
// non-application/json or non-matching bodies.
func TestParseAgentUpstreamBody(t *testing.T) {
	t.Run("matches", func(t *testing.T) {
		resp := mkResponse(http.StatusBadGateway, "application/json",
			`{"code":"E_AGENT_UPSTREAM","category":"tls","cluster":"prod","message":"x","detail":"y","trace_id":"abc"}`)
		got := parseAgentUpstreamBody(resp)
		if got == nil {
			t.Fatal("parseAgentUpstreamBody returned nil for a matching body")
		}
		if got.Category != "tls" || got.Cluster != "prod" || got.TraceID != "abc" {
			t.Errorf("fields not parsed: %+v", got)
		}
		if got.HTTPStatus != http.StatusBadGateway {
			t.Errorf("HTTPStatus = %d, want 502", got.HTTPStatus)
		}
	})
	t.Run("ignores text/plain", func(t *testing.T) {
		resp := mkResponse(http.StatusBadGateway, "text/plain", "agent → apiserver: refused")
		if got := parseAgentUpstreamBody(resp); got != nil {
			t.Errorf("parseAgentUpstreamBody returned %+v for text/plain body", got)
		}
	})
	t.Run("ignores foreign code", func(t *testing.T) {
		resp := mkResponse(http.StatusBadGateway, "application/json",
			`{"code":"E_OTHER","category":"network"}`)
		if got := parseAgentUpstreamBody(resp); got != nil {
			t.Errorf("parseAgentUpstreamBody returned %+v for foreign code", got)
		}
	})
	t.Run("ignores nil", func(t *testing.T) {
		if got := parseAgentUpstreamBody(nil); got != nil {
			t.Errorf("parseAgentUpstreamBody(nil) = %+v", got)
		}
	})
}

// TestRespondProxyError_WireFormat locks in the central-server CONNECT
// proxy wire format. wrapAgentUpstream depends on the JSON envelope
// being parseable end-to-end so the SPA shows the same banner whether
// the failure was on the agent side or in our own loopback proxy.
func TestRespondProxyError_WireFormat(t *testing.T) {
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close() }()
	go func() {
		respondProxyError(c2, http.StatusBadGateway, "pre-prod", &agentUpstreamWire{
			Code:     AgentUpstreamErrorCode,
			Category: "network",
			Message:  "could not dial agent tunnel",
			Detail:   "connection reset by peer",
		})
		_ = c2.Close()
	}()

	resp, err := http.ReadResponse(bufio.NewReader(c1), nil)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	aue := parseAgentUpstreamBody(resp)
	if aue == nil {
		t.Fatal("body did not round-trip through parseAgentUpstreamBody")
	}
	if aue.Category != "network" || aue.Cluster != "pre-prod" {
		t.Errorf("round-tripped fields: %+v", aue)
	}
}
