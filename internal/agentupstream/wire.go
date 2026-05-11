// Package agentupstream defines the JSON envelope the periscope-agent's
// reverse proxy emits when it can't reach the local apiserver, and the
// stable error code on that envelope. Two consumers share these
// definitions:
//
//   - cmd/periscope-agent/proxy.go writes envelopes from its
//     ErrorHandler when the reverse proxy hits a transport failure.
//   - internal/k8s/agent_upstream_error.go parses envelopes off the
//     wire and converts them to a typed *AgentUpstreamError the
//     central server's HTTP handlers + exec session inspect.
//
// Pulled into its own package (rather than duplicating the const +
// struct in both call sites, as an earlier iteration of #68 did) so
// the wire contract has exactly one source of truth. New optional
// fields are safe to add. Renaming or repurposing fields requires a
// coordinated agent + server rollout — bump CodeAgentUpstream first
// only as part of a wire-breaking change.
package agentupstream

// CodeAgentUpstream is the stable code stamped on every envelope the
// agent's reverse proxy writes when it can't reach the local
// apiserver. The server-side parser uses this string (and the small
// set returned by RecognizedCodes) to discriminate Periscope-shaped
// failures from generic 502s emitted by other infra.
const CodeAgentUpstream = "E_AGENT_UPSTREAM"

// CodeNoAgent is emitted by the central server's loopback CONNECT
// proxy when the requested cluster has no registered agent tunnel.
// Recognized by the server-side parser so the SPA renders a friendly
// "agent for this cluster is disconnected" banner instead of an
// opaque 502.
const CodeNoAgent = "E_NO_AGENT"

// RecognizedCodes is the set of `code` field values the server-side
// parser converts into a typed *AgentUpstreamError. Other codes (e.g.
// E_INVALID_CONNECT for malformed CONNECT requests) deliberately stay
// generic — they're programmer errors, not operational ones operators
// can fix without code changes.
func RecognizedCodes() []string {
	return []string{CodeAgentUpstream, CodeNoAgent}
}

// IsRecognized reports whether code names a structured envelope the
// server-side parser should convert into *AgentUpstreamError.
func IsRecognized(code string) bool {
	switch code {
	case CodeAgentUpstream, CodeNoAgent:
		return true
	}
	return false
}

// Envelope is the on-wire JSON shape both the agent's reverse proxy
// (writer) and the central server's RoundTripper interceptor (reader)
// agree on. Field names + JSON tags are part of the wire contract;
// don't rename without a coordinated rollout.
type Envelope struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Category string `json:"category"`
	Cluster  string `json:"cluster,omitempty"`
	Detail   string `json:"detail,omitempty"`
	TraceID  string `json:"trace_id,omitempty"`
}
