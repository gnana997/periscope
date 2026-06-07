package clustershell

import (
	"sync"
	"time"
)

// SessionRecord is the informational handle stored in the Registry
// for an active cluster-shell session. Concurrency is owned by the
// per-session context, not by this struct — entries only enable cap
// enforcement, listing, and the "list active sessions" UX hook in
// the cap-exceeded response body.
type SessionRecord struct {
	ID        string // UUIDv4
	Actor     string // Provider.Actor() at session start
	Cluster   string
	Tier      string
	Mode      Mode
	StartedAt time.Time
}

// Registry is a process-wide in-memory map of active cluster-shell
// sessions, structurally identical to internal/exec/registry.go.
// Concurrent-safe. v1.2 keeps state in-memory and per-replica; HA
// support (shared store across replicas) lands when the dashboard
// itself goes multi-replica.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]SessionRecord
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{sessions: make(map[string]SessionRecord)}
}

// Add records a session. Returns false if the ID is already present;
// the caller should regenerate the session UUID and retry.
func (r *Registry) Add(s SessionRecord) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[s.ID]; exists {
		return false
	}
	r.sessions[s.ID] = s
	return true
}

// Remove deletes a session record. Safe on unknown IDs (no-op).
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

// List returns a snapshot of all active sessions. Order is not guaranteed.
func (r *Registry) List() []SessionRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SessionRecord, 0, len(r.sessions))
	for _, s := range r.sessions {
		out = append(out, s)
	}
	return out
}

// CountForActor returns the number of active sessions owned by the
// supplied actor. Used by the handler to enforce per-user caps
// before WS upgrade.
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

// CountForCluster returns the number of active sessions targeting
// the supplied cluster across all actors. Used by the handler to
// enforce per-cluster total caps.
func (r *Registry) CountForCluster(cluster string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sessions {
		if s.Cluster == cluster {
			n++
		}
	}
	return n
}

// SnapshotForActor returns a copy of the sessions belonging to the
// supplied actor. Populates the E_CAP_USER response body so the SPA
// can offer "close one to free a slot" without a separate fetch.
func (r *Registry) SnapshotForActor(actor string) []SessionRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]SessionRecord, 0)
	for _, s := range r.sessions {
		if s.Actor == actor {
			out = append(out, s)
		}
	}
	return out
}
