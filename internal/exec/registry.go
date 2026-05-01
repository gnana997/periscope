// Package exec wires browser WebSocket connections to k8s.ExecPod streams
// and tracks active sessions. The PR1 surface is intentionally narrow: a
// session registry sufficient for audit and a session orchestrator that
// runs one exec stream end-to-end. Idle handling, heartbeats, and caps
// land in PR3/PR4.
package exec

import (
	"sync"
	"time"
)

// Session is the registered, in-memory record for one active exec session.
// It is informational only — cancelling a session is done via the
// per-session context owned by the orchestrator, not via this struct.
type Session struct {
	ID        string    // UUIDv4
	Actor     string    // Provider.Actor() at session start
	Cluster   string
	Namespace string
	Pod       string
	Container string
	StartedAt time.Time
}

// Registry is a process-wide in-memory map of active sessions. Concurrent-safe.
//
// PR1 uses this for audit and future "list active sessions" UX. Per-user
// caps and admin "kill session" are PR4.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]Session
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{sessions: make(map[string]Session)}
}

// Add records a session. Returns false if the ID is already present (caller
// should regenerate).
func (r *Registry) Add(s Session) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[s.ID]; exists {
		return false
	}
	r.sessions[s.ID] = s
	return true
}

// Remove deletes a session. Safe to call on an unknown ID.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

// List returns a snapshot of all active sessions. Order is not guaranteed.
func (r *Registry) List() []Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Session, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}

// CountForActor returns the number of active sessions owned by the given
// actor. PR4 uses this to enforce per-user caps; PR1 keeps it for the
// future call site.
func (r *Registry) CountForActor(actor string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sessions {
		if s.Actor == actor {
			n++
		}
	}
	return n
}
