package identity

import (
	"sort"
	"sync"
	"time"
)

// Store is the per-cluster in-memory snapshot of the SA↔Role index.
// Concurrent reads (Snapshot) and a single writer (Replace) are
// safe. Invalidate marks the snapshot stale so the next manager
// Ensure rebuilds; it does not blank entries — readers continue to
// see the last successful snapshot until the rebuild completes,
// avoiding empty-state flicker during refresh.
type Store struct {
	mu          sync.RWMutex
	entries     map[SAKey]SARoleIndexEntry
	lastBuilt   time.Time
	hasSnapshot bool
}

// NewStore returns an empty Store. Until Replace is called once,
// HasSnapshot reports false and Snapshot returns nil.
func NewStore() *Store {
	return &Store{entries: map[SAKey]SARoleIndexEntry{}}
}

// Replace atomically swaps the current snapshot for the new one and
// updates lastBuilt to now. Callers pass the Manager's clock-derived
// time so tests can drive TTL semantics without sleeping.
func (s *Store) Replace(entries []SARoleIndexEntry, now time.Time) {
	m := make(map[SAKey]SARoleIndexEntry, len(entries))
	for _, e := range entries {
		m[SAKey{Namespace: e.Namespace, Name: e.SAName}] = e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = m
	s.lastBuilt = now
	s.hasSnapshot = true
}

// Snapshot returns the current entries sorted by (namespace, saName).
// Returns nil before the first Replace.
func (s *Store) Snapshot() []SARoleIndexEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.hasSnapshot {
		return nil
	}
	out := make([]SARoleIndexEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].SAName < out[j].SAName
	})
	return out
}

// Age returns the elapsed time since the last successful Replace.
// Returns a very large value before the first build so Manager.Ensure
// treats the store as stale on cold start. The clock-now parameter
// keeps Age driven by the Manager's clock for testability.
func (s *Store) Age(now time.Time) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.hasSnapshot {
		return time.Duration(1<<62) // "infinite" — never <ttl
	}
	return now.Sub(s.lastBuilt)
}

// HasSnapshot reports whether any successful Replace has occurred.
// Handlers use this to decide between "still warming up — serve 503
// with Retry-After" and "soft-fail with an empty page".
func (s *Store) HasSnapshot() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hasSnapshot
}

// Invalidate marks the current snapshot stale by zeroing lastBuilt.
// The next Manager.Ensure observing Age >= ttl rebuilds. The args
// are kept (vs a no-arg Invalidate) so the watch hook is explicit
// about what changed; a future partial-rebuild optimization can use
// them without changing callers.
func (s *Store) Invalidate(ns, sa string) {
	_ = ns
	_ = sa
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastBuilt = time.Time{}
}
