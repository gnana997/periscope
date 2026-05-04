// Package tunnel implements the agent-backend transport layer for
// Periscope. It wraps rancher/remotedialer so the central server can
// hold long-lived WebSocket tunnels to per-cluster agents and route
// apiserver-bound HTTP/SSE/WebSocket traffic through them.
//
// Two halves:
//
//   - Server: accepts WebSocket upgrade requests at the agent
//     registration endpoint, validates the agent's identity (the auth
//     hook is pluggable so #42b can layer mTLS on top), and tracks
//     connected sessions in a name → session map. Handlers reach into
//     the map via LookupSession or the convenience RoundTripper to
//     route a request to a specific cluster.
//
//   - Client: used by cmd/periscope-agent. Dials out to the central
//     server, presents auth credentials, and serves whatever bytes
//     the server sends back by dialing the local apiserver. Keepalive
//     and jittered reconnect are built in.
//
// What is intentionally NOT in this package:
//
//   - mTLS client cert minting / validation. That's #42b — the
//     registration handler that signs certs and validates them on
//     reconnect lives in cmd/periscope/agent_handler.go. This package
//     accepts a pluggable Authorizer so the auth handler can compose
//     the validation in.
//   - The BackendAgent registry hookup. #42d wires
//     internal/k8s/client.go::buildRestConfig to call
//     server.RoundTripper(name) for agent-backed clusters.
//   - Agent-side cert lifecycle, healthz, metrics. The Client here is
//     the bare transport; cmd/periscope-agent layers operational
//     concerns on top.
//
// The whole point of the wrapping is that consumers never see
// remotedialer types. If we ever swap transports (gRPC bidi, raw TCP,
// SSH), the public API of this package stays the same.
package tunnel
