// Typed error surfacing for agent → apiserver transport failures.
//
// The agent's reverse proxy (cmd/periscope-agent/proxy.go) emits a
// JSON envelope on every transport-layer failure between itself and
// the local apiserver:
//
//	{
//	  "code":     "E_AGENT_UPSTREAM",
//	  "category": "network|tls|timeout|unknown",
//	  "cluster":  "pre-prod",
//	  "message":  "agent could not reach the cluster's apiserver",
//	  "detail":   "dial tcp 10.100.0.1:443: connect: connection refused",
//	  "trace_id": "<X-Request-Id>"
//	}
//
// This file defines the typed error the central server hands up to its
// HTTP handlers (cmd/periscope/errors.go) and the exec session
// (internal/exec/session.go) when it sees that envelope. The typed
// error preserves every field so downstream code can render
// category-specific UX without re-parsing JSON.

package k8s

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gnana997/periscope/internal/agentupstream"
)

// AgentUpstreamErrorCode aliases agentupstream.CodeAgentUpstream so
// existing call sites that depend on the typed error continue to
// compile. New code should reference agentupstream.CodeAgentUpstream
// (and the no-tunnel variant agentupstream.CodeNoAgent) directly.
const AgentUpstreamErrorCode = agentupstream.CodeAgentUpstream

// AgentUpstreamError is the typed error produced by wrapAgentUpstream
// (and by the exec CONNECT proxy interceptor) when the agent reverse
// proxy reports a transport-layer failure. Field semantics match the
// JSON envelope.
//
// The field names are exposed so the session layer can copy them
// straight into the WS error control frame; the SPA already speaks
// the same vocabulary.
type AgentUpstreamError struct {
	// Code is always "E_AGENT_UPSTREAM" today; carried explicitly so
	// future agent versions can introduce sub-codes (e.g.
	// E_AGENT_NO_TUNNEL) on the same envelope without changing the
	// type.
	Code string

	// Category is one of: network, tls, timeout, unknown. The SPA
	// keys the banner copy off this string.
	Category string

	// Cluster is the agent-reported cluster name (PERISCOPE_CLUSTER_NAME
	// on the agent side). May be empty when produced by paths that don't
	// know the cluster (e.g. the central-server CONNECT proxy when the
	// tunnel itself is missing — that path fills it in from the URL).
	Cluster string

	// Message is the friendly one-liner the SPA renders when no
	// category-specific copy is wired up yet.
	Message string

	// Detail is the raw underlying error string from the agent's
	// reverse proxy. Useful for the "info" expander and operator logs.
	Detail string

	// TraceID is the X-Request-Id the agent saw on the inbound
	// request, or a fallback hex token the agent minted when no
	// header was present. The same id appears in the agent's slog
	// access-log line so operators can pivot.
	TraceID string

	// HTTPStatus is the status the agent returned alongside the JSON
	// body (502 for net/tls/unknown, 504 for timeout). Surfaced so
	// non-exec handlers can pass it through to the SPA's HTTP error
	// envelope unchanged.
	HTTPStatus int
}

func (e *AgentUpstreamError) Error() string {
	if e == nil {
		return "agent upstream error"
	}
	parts := []string{e.Code}
	if e.Category != "" {
		parts = append(parts, "category="+e.Category)
	}
	if e.Cluster != "" {
		parts = append(parts, "cluster="+e.Cluster)
	}
	if e.TraceID != "" {
		parts = append(parts, "trace_id="+e.TraceID)
	}
	if e.Detail != "" {
		parts = append(parts, "detail="+e.Detail)
	} else if e.Message != "" {
		parts = append(parts, "message="+e.Message)
	}
	return strings.Join(parts, " ")
}

// AsAgentUpstreamError returns the typed error if err wraps one,
// matching the errors.As idiom. Convenience for handlers that don't
// want to declare a *AgentUpstreamError local variable just to call
// errors.As.
func AsAgentUpstreamError(err error) (*AgentUpstreamError, bool) {
	var aue *AgentUpstreamError
	if errors.As(err, &aue) {
		return aue, true
	}
	return nil, false
}

// agentUpstreamWire is the on-wire JSON shape the agent emits, retained
// here as a package-local alias for the shared agentupstream.Envelope
// so existing test code and the CONNECT proxy in agent_exec_proxy.go
// continue to compile without importing the shared package directly.
// New code should reference agentupstream.Envelope.
type agentUpstreamWire = agentupstream.Envelope

// parseAgentUpstreamBody attempts to read an agent JSON envelope from
// resp.Body. Returns the typed error on success; nil otherwise. The
// caller is responsible for any further response handling — this
// function does NOT close the body, only drain what it parsed.
//
// Conservative: only recognises bodies whose `code` is in
// agentupstream.RecognizedCodes (today: E_AGENT_UPSTREAM from the
// agent's reverse proxy + E_NO_AGENT from the central server's
// loopback CONNECT proxy). A 502 from a generic upstream (e.g. an
// ingress controller in front of a future deployment shape) is left
// alone so callers can decide what to do.
func parseAgentUpstreamBody(resp *http.Response) *AgentUpstreamError {
	if resp == nil || resp.Body == nil {
		return nil
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		return nil
	}
	// Cap the read so a hostile upstream can't blow up memory.
	const maxBody = 16 * 1024
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil || len(raw) == 0 {
		return nil
	}
	var w agentUpstreamWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil
	}
	if !agentupstream.IsRecognized(w.Code) {
		return nil
	}
	return &AgentUpstreamError{
		Code:       w.Code,
		Category:   w.Category,
		Cluster:    w.Cluster,
		Message:    w.Message,
		Detail:     w.Detail,
		TraceID:    w.TraceID,
		HTTPStatus: resp.StatusCode,
	}
}

// wrapAgentUpstream decorates rt so that responses carrying the
// agent's structured error envelope are surfaced as a typed
// *AgentUpstreamError instead of a generic 502 with an opaque body.
//
// On envelope match: drains+closes the body, returns (nil, *AgentUpstreamError).
// On any other response: passes resp through unchanged so the caller's
// own status/body handling continues to work.
//
// Used for non-exec API traffic (Pod GET/list/watch, logs, apply, etc.)
// flowing through tunnel.NewRoundTripper. The exec path takes a
// different intercept point because client-go's executors bypass
// rest.Config.Transport — see internal/k8s/agent_exec_proxy.go.
func wrapAgentUpstream(rt http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := rt.RoundTrip(req)
		if err != nil || resp == nil {
			return resp, err
		}
		// Only inspect non-2xx responses — happy-path traffic must not
		// pay JSON parse cost.
		if resp.StatusCode < 400 {
			return resp, nil
		}
		// Buffer the body so we can both classify and (on miss) hand
		// it back to the caller untouched. Cap protects us from
		// pathological bodies. Note the cap is silent: if a
		// (hypothetical) upstream sent > maxBody of valid JSON, the
		// LimitReader truncates, json.Unmarshal fails, and we fall
		// through to the non-match branch — caller receives the
		// truncated body. Acceptable because in practice
		// E_AGENT_UPSTREAM envelopes are < 1 KB; raise maxBody if
		// that ever stops being true.
		const maxBody = 16 * 1024
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read agent response: %w", readErr)
		}
		// Restore the body for the non-match path.
		resp.Body = io.NopCloser(bytes.NewReader(body))

		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			return resp, nil
		}
		var w agentUpstreamWire
		if err := json.Unmarshal(body, &w); err != nil || !agentupstream.IsRecognized(w.Code) {
			return resp, nil
		}
		// Match — close the body and return the typed error. The
		// caller never sees the raw response in this branch.
		_ = resp.Body.Close()
		return nil, &AgentUpstreamError{
			Code:       w.Code,
			Category:   w.Category,
			Cluster:    w.Cluster,
			Message:    w.Message,
			Detail:     w.Detail,
			TraceID:    w.TraceID,
			HTTPStatus: resp.StatusCode,
		}
	})
}
