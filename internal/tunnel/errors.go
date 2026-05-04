package tunnel

import "errors"

// Sentinel errors callers can branch on. Wrapped with %w when more
// context is needed; use errors.Is.
var (
	// ErrNoSession means the named agent is not currently connected.
	// Handlers translate this to HTTP 503 (the agent's cluster is
	// transiently unreachable, retry is reasonable).
	ErrNoSession = errors.New("tunnel: agent not connected")

	// ErrAgentDisconnected means the session existed when the request
	// started but the underlying tunnel dropped before the dial
	// completed. Distinct from ErrNoSession so observability can
	// separate "never connected" from "flapping mid-flight."
	ErrAgentDisconnected = errors.New("tunnel: agent disconnected mid-request")

	// ErrUnauthorized is returned by the Server's auth hook when an
	// agent fails its identity check (bad mTLS cert, expired cert,
	// cluster name not in registry). The HTTP layer surfaces this as
	// 401 to the agent so it can log and back off.
	ErrUnauthorized = errors.New("tunnel: agent unauthorized")
)
