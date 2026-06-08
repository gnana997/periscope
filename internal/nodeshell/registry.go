package nodeshell

import (
	"sync"
	"time"
)

// SessionRecord is the slim accounting view of a live node-shell
// session, used for concurrency caps and the cap-reached dialog.
type SessionRecord struct {
	ID        string
	Actor     string
	Cluster   string
	Node      string
	StartedAt time.Time
}

// Registry tracks live node-shell sessions for per-user and per-cluster
// concurrency caps. In-memory and per-process, matching the rest of the
// single-replica v1 surface.
type Registry struct {
	mu   sync.Mutex
	byID map[string]SessionRecord
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry { return &Registry{byID: map[string]SessionRecord{}} }

// Add records a session. Returns false on id collision (the caller
// should treat that as an internal error).
func (r *Registry) Add(rec SessionRecord) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byID[rec.ID]; ok {
		return false
	}
	r.byID[rec.ID] = rec
	return true
}

// Remove drops a session (safe to call for an unknown id).
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
}

// CountForActor returns how many live sessions an actor holds.
func (r *Registry) CountForActor(actor string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.byID {
		if s.Actor == actor {
			n++
		}
	}
	return n
}

// CountForCluster returns how many live sessions target a cluster.
func (r *Registry) CountForCluster(cluster string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.byID {
		if s.Cluster == cluster {
			n++
		}
	}
	return n
}

// SnapshotForActor returns the actor's live sessions for the
// cap-reached dialog.
func (r *Registry) SnapshotForActor(actor string) []SessionRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []SessionRecord
	for _, s := range r.byID {
		if s.Actor == actor {
			out = append(out, s)
		}
	}
	return out
}
