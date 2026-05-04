package tunnel

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rancher/remotedialer"
)

// Authorizer validates an inbound agent connection and returns the
// cluster name the agent claims. The bool reports whether the request
// is allowed; err is logged but not exposed to the agent (beyond an
// HTTP 401 status).
//
// #42b's mTLS handler implements this by reading the verified peer
// certificate from r.TLS.PeerCertificates and matching the CN against
// the registered cluster set. For local dev / testing we accept a
// trivial header-based authorizer so the in-memory tests don't need a
// CA chain.
type Authorizer func(r *http.Request) (clusterName string, ok bool, err error)

// SessionEvent is emitted on connect / disconnect so the registry,
// fleet view, and metrics can react.
type SessionEvent struct {
	ClusterName string
	Connected   bool
	At          time.Time
}

// Observer is the optional callback fired on session changes. Called
// synchronously from the connection goroutine — keep work bounded.
type Observer func(SessionEvent)

// ServerOptions configure the tunnel server. Zero values are sensible.
type ServerOptions struct {
	// Authorizer validates agent connections. Required.
	Authorizer Authorizer

	// Observer is called on connect / disconnect. Optional.
	Observer Observer

	// PeerID, if non-empty, identifies this server replica when peer
	// routing is added in v1.5. For v1.x.0 single-replica we leave it
	// empty and remotedialer treats sessions as local-only.
	PeerID string
}

// Server is the central side of the agent tunnel. One Server holds N
// agent sessions, each keyed by cluster name (the value the
// Authorizer returned for that connection).
//
// The remotedialer Server underneath does the WebSocket framing and
// session multiplexing; we wrap it so callers see only this package's
// types and so we can swap transports later without touching handler
// code.
type Server struct {
	rd       *remotedialer.Server
	observer Observer
	authz    Authorizer

	// Connected-name tracking. remotedialer exposes HasSession but
	// not "list of connected clients" — we keep our own set so the
	// fleet view and metrics can enumerate.
	mu        sync.RWMutex
	connected map[string]time.Time
}

// NewServer constructs a tunnel server. The returned value is safe
// for concurrent use.
func NewServer(opts ServerOptions) *Server {
	if opts.Authorizer == nil {
		// A nil Authorizer would auth-allow everything — refuse at
		// construction so production never ships in this state.
		panic("tunnel: Authorizer is required")
	}
	s := &Server{
		observer:  opts.Observer,
		authz:     opts.Authorizer,
		connected: make(map[string]time.Time),
	}
	s.rd = remotedialer.New(s.authorize, s.errorWriter)
	return s
}

// Connect is the http.Handler for the agent's WebSocket upgrade
// endpoint. Mount it at e.g. /api/agents/connect; the registration
// handler in #42b wraps this with mTLS validation.
func (s *Server) Connect(w http.ResponseWriter, r *http.Request) {
	// remotedialer.Server.ServeHTTP does the upgrade + session loop,
	// invoking our authorize hook to extract the client key. The call
	// blocks for the duration of the WebSocket session, returning
	// when the agent disconnects.
	s.rd.ServeHTTP(w, r)
}

// LookupSession returns true iff a session is currently connected for
// the given cluster name. Cheap; called per incoming user request.
func (s *Server) LookupSession(name string) bool {
	return s.rd.HasSession(name)
}

// Connected returns the set of currently-connected cluster names.
// Snapshot — safe to iterate after return. Used by the fleet view and
// the prometheus collector.
func (s *Server) Connected() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.connected))
	for name := range s.connected {
		out = append(out, name)
	}
	return out
}

// ConnectedAt returns the time the named agent connected, or zero
// time if not connected. Used by the fleet view's "connected since"
// indicator.
func (s *Server) ConnectedAt(name string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected[name]
}

// DialerFor returns a tunnel-bound DialContext for the named agent.
// The returned function opens a TCP connection through the tunnel; it
// is intended to be plugged into http.Transport.DialContext (see
// transport.go).
//
// Returns ErrNoSession if no agent is currently connected under that
// name.
func (s *Server) DialerFor(name string) (DialFunc, error) {
	if !s.rd.HasSession(name) {
		return nil, fmt.Errorf("%w: %s", ErrNoSession, name)
	}
	// remotedialer's Dialer has signature
	//   func(ctx, network, address) (net.Conn, error)
	// — we expose our own typed alias so consumers don't need to
	// import remotedialer to get a DialFunc.
	dial := s.rd.Dialer(name)
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Check liveness once more inside the closure: the agent may
		// have disconnected between LookupSession and Dial.
		if !s.rd.HasSession(name) {
			return nil, fmt.Errorf("%w: %s", ErrAgentDisconnected, name)
		}
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, fmt.Errorf("tunnel dial %s -> %s: %w", name, addr, err)
		}
		return conn, nil
	}, nil
}

// authorize is the bridge between remotedialer's authorizer signature
// and our typed Authorizer. Also tracks connect/disconnect for the
// observer + connected map.
func (s *Server) authorize(r *http.Request) (clientKey string, authed bool, err error) {
	name, ok, err := s.authz(r)
	if err != nil {
		slog.WarnContext(r.Context(), "tunnel.authorize_failed",
			"remote_addr", r.RemoteAddr, "err", err)
		return "", false, err
	}
	if !ok {
		slog.WarnContext(r.Context(), "tunnel.authorize_denied",
			"remote_addr", r.RemoteAddr, "claimed_name", name)
		return "", false, ErrUnauthorized
	}
	// remotedialer doesn't give us connect/disconnect callbacks at
	// session granularity — the session lifetime equals the duration
	// of authorize -> ServeHTTP -> WebSocket close. Track here.
	s.markConnected(r.Context(), name)
	go s.watchDisconnect(name)
	return name, true, nil
}

// errorWriter is remotedialer's hook for writing errors back to the
// agent during the session loop. We translate to the standard
// http.Error shape so agents see structured responses.
func (s *Server) errorWriter(rw http.ResponseWriter, req *http.Request, code int, err error) {
	slog.WarnContext(req.Context(), "tunnel.session_error",
		"code", code, "err", err)
	http.Error(rw, err.Error(), code)
}

func (s *Server) markConnected(ctx context.Context, name string) {
	now := time.Now().UTC()
	s.mu.Lock()
	s.connected[name] = now
	s.mu.Unlock()
	slog.InfoContext(ctx, "tunnel.agent_connected", "cluster", name, "at", now.Format(time.RFC3339))
	if s.observer != nil {
		s.observer(SessionEvent{ClusterName: name, Connected: true, At: now})
	}
}

func (s *Server) markDisconnected(name string) {
	now := time.Now().UTC()
	s.mu.Lock()
	delete(s.connected, name)
	s.mu.Unlock()
	slog.Info("tunnel.agent_disconnected", "cluster", name, "at", now.Format(time.RFC3339))
	if s.observer != nil {
		s.observer(SessionEvent{ClusterName: name, Connected: false, At: now})
	}
}

// watchDisconnect polls remotedialer.HasSession until the named
// session is gone, then fires the disconnect observer. This is the
// least-bad option — remotedialer doesn't expose a disconnect
// channel at the public API level, so we poll on a 2s tick. Cheap
// (just a map lookup under a RWMutex) and the latency on the fleet
// view's "disconnected" indicator being 0–2s late is fine.
func (s *Server) watchDisconnect(name string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if !s.rd.HasSession(name) {
			s.markDisconnected(name)
			return
		}
	}
}
