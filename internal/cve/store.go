// Package cve owns the local in-memory CVE cache that the API layer
// (#165) and SPA read from. See issue #164 + epic #163 for the
// architecture: per-cluster store, lazy hydrate on first activation,
// delta updates via a long-lived pod informer, 6h TTL refresh, 24h
// eviction of zero-ref entries.
package cve

import (
	"context"
	"sync"
	"time"

	"github.com/gnana997/periscope/internal/awsinspector"
)

// OwnerKind labels how an EC2 instance was provisioned. Surfaced on
// the Nodes / NodeGroups / Karpenter dashboards so operators can join
// a CVE row to "the thing I'd patch".
type OwnerKind string

const (
	OwnerManagedNodegroup OwnerKind = "managed-nodegroup"
	OwnerKarpenter        OwnerKind = "karpenter-nodeclaim"
	OwnerUnmanaged        OwnerKind = "unmanaged"
)

// Finding is the package-local alias of the awsinspector DTO. Re-
// exported so consumers (#165 API layer) don't need to import the SDK
// wrapper directly.
type Finding = awsinspector.Finding

// DigestEntry is the per-image-digest cache row. PodRefs is the
// number of currently-running pods using this digest; the eviction
// sweeper drops the entry when it reaches zero AND the entry is
// older than maxAge.
type DigestEntry struct {
	Digest      string
	Findings    []Finding
	LastFetched time.Time
	PodRefs     int
}

// InstanceEntry is the per-EC2-instance cache row. NodeRefs is the
// number of K8s Nodes pointing at this instance (typically 1). The
// owner fields are populated by the owner resolver at hydrate time.
type InstanceEntry struct {
	InstanceID  string
	Findings    []Finding
	LastFetched time.Time
	NodeRefs    int
	OwnerKind   OwnerKind
	AMI         string
	OwnerName   string
}

// Store is the per-cluster in-memory CVE cache. All access is
// guarded by mu; the read path (API layer) takes RLock, the write
// path (hydrate / refresh / watch hook) takes Lock.
//
// hydrated and hydrating implement first-read blocking: the very
// first read after activation blocks on hydrating until the cold
// hydrate completes, so the UI never paints an empty list on a
// cluster that simply hasn't been scanned yet.
//
// disabled is set when the account's IAM lacks inspector2:* OR
// Inspector v2 is genuinely off; the API layer reads it to render
// the empty-state hint instead of a generic "no findings".
type Store struct {
	mu sync.RWMutex

	digests   map[string]*DigestEntry
	instances map[string]*InstanceEntry

	hydrated    bool
	lastHydrate time.Time
	hydrating chan struct{} // closed when hydrate completes
	disabled  bool
}

// NewStore returns an empty store ready to receive a Hydrate call.
// hydrating is open until MarkHydrated or MarkDisabled is called.
func NewStore() *Store {
	return &Store{
		digests:   map[string]*DigestEntry{},
		instances: map[string]*InstanceEntry{},
		hydrating: make(chan struct{}),
	}
}

// WaitHydrated blocks until the store's first hydrate completes (or
// the context is cancelled). After return, calls to GetDigest /
// GetInstance reflect the hydrated state. Manager.EnsureHydrated
// uses this to make the first read of a cluster synchronously wait
// for the cold-path scan to finish.
func (s *Store) WaitHydrated(ctx context.Context) error {
	s.mu.RLock()
	ch := s.hydrating
	hydrated := s.hydrated
	s.mu.RUnlock()
	if hydrated {
		return nil
	}
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// MarkHydrated flips the store to ready state and unblocks waiters.
// Idempotent so a re-hydrate (e.g. cluster reconnect) doesn't panic
// on a double-close.
func (s *Store) MarkHydrated(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hydrated {
		return
	}
	s.lastHydrate = now
	s.hydrated = true
	close(s.hydrating)
}

// MarkDisabled flips the store to "Inspector v2 not enabled" and
// unblocks waiters with disabled=true. Same idempotency guarantee as
// MarkHydrated.
func (s *Store) MarkDisabled(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hydrated {
		return
	}
	s.disabled = true
	s.lastHydrate = now
	s.hydrated = true
	close(s.hydrating)
}

// Disabled reports whether Inspector v2 is unavailable on this account.
func (s *Store) Disabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.disabled
}

// Hydrated reports whether the first hydrate has finished. False
// during the brief cold-path window before MarkHydrated/MarkDisabled.
func (s *Store) Hydrated() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hydrated
}

// UpsertDigest stores findings for a digest, refreshing LastFetched
// to now. Pod-ref count is preserved across upserts so a refresh
// triggered by TTL doesn't wipe the watch hook's ref tracking.
func (s *Store) UpsertDigest(digest string, findings []Finding, now time.Time) {
	if digest == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.digests[digest]
	if e == nil {
		e = &DigestEntry{Digest: digest}
		s.digests[digest] = e
	}
	e.Findings = findings
	e.LastFetched = now
}

// UpsertInstance stores findings + owner for an instance. NodeRefs
// is preserved across upserts.
func (s *Store) UpsertInstance(instanceID string, findings []Finding, ownerKind OwnerKind, ownerName string, ami string, now time.Time) {
	if instanceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.instances[instanceID]
	if e == nil {
		e = &InstanceEntry{InstanceID: instanceID}
		s.instances[instanceID] = e
	}
	e.Findings = findings
	e.LastFetched = now
	e.OwnerKind = ownerKind
	e.OwnerName = ownerName
	e.AMI = ami
}

// GetDigest returns the entry for a digest, or nil if absent. The
// returned value is a defensive copy of the metadata fields and a
// shared slice of findings — callers MUST treat Findings as
// read-only (the slice header is shared with the store).
func (s *Store) GetDigest(digest string) *DigestEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.digests[digest]
	if !ok {
		return nil
	}
	c := *e
	return &c
}

// GetInstance returns the entry for an instance ID, or nil if absent.
// Same read-only semantics as GetDigest.
func (s *Store) GetInstance(instanceID string) *InstanceEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.instances[instanceID]
	if !ok {
		return nil
	}
	c := *e
	return &c
}

// IncDigestRef bumps the pod-ref count for a digest, creating the
// entry with an empty Findings slice if it does not yet exist. The
// empty entry is fine: the next Refresh or TTL pass will populate it
// with real data.
func (s *Store) IncDigestRef(digest string) {
	if digest == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.digests[digest]
	if e == nil {
		e = &DigestEntry{Digest: digest}
		s.digests[digest] = e
	}
	e.PodRefs++
}

// DecDigestRef decrements the pod-ref count for a digest. Floors at
// zero so an over-decrement (rare, but possible if a watch event is
// dropped) does not flip to a negative ref count and confuse the
// eviction sweeper.
func (s *Store) DecDigestRef(digest string) {
	if digest == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.digests[digest]; ok && e.PodRefs > 0 {
		e.PodRefs--
	}
}

// IncInstanceRef / DecInstanceRef are the instance-side equivalents.
// Used by the (future #166) node informer or by hydrate itself when
// it counts known nodes per instance.
func (s *Store) IncInstanceRef(instanceID string) {
	if instanceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.instances[instanceID]
	if e == nil {
		e = &InstanceEntry{InstanceID: instanceID}
		s.instances[instanceID] = e
	}
	e.NodeRefs++
}

func (s *Store) DecInstanceRef(instanceID string) {
	if instanceID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.instances[instanceID]; ok && e.NodeRefs > 0 {
		e.NodeRefs--
	}
}

// IterStale returns the digest and instance keys whose LastFetched is
// older than (now - ttl). Returned slices are stable copies safe to
// iterate after the store mutex is released.
func (s *Store) IterStale(now time.Time, ttl time.Duration) (digests []string, instances []string) {
	cutoff := now.Add(-ttl)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for k, e := range s.digests {
		if e.LastFetched.Before(cutoff) {
			digests = append(digests, k)
		}
	}
	for k, e := range s.instances {
		if e.LastFetched.Before(cutoff) {
			instances = append(instances, k)
		}
	}
	return digests, instances
}

// IterEvictable returns the digest and instance keys whose ref count
// has dropped to zero AND whose LastFetched is older than (now -
// maxAge). Both predicates must match — a zero-ref but recently-
// fetched entry stays cached for `maxAge` so a pod that briefly
// scales to zero replicas doesn't lose its CVE data.
func (s *Store) IterEvictable(now time.Time, maxAge time.Duration) (digests []string, instances []string) {
	cutoff := now.Add(-maxAge)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for k, e := range s.digests {
		if e.PodRefs == 0 && e.LastFetched.Before(cutoff) {
			digests = append(digests, k)
		}
	}
	for k, e := range s.instances {
		if e.NodeRefs == 0 && e.LastFetched.Before(cutoff) {
			instances = append(instances, k)
		}
	}
	return digests, instances
}

// EvictDigest removes a digest entry. Used by the eviction sweeper.
func (s *Store) EvictDigest(digest string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.digests, digest)
}

// EvictInstance removes an instance entry.
func (s *Store) EvictInstance(instanceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.instances, instanceID)
}

// Snapshot returns a shallow copy of all current entries. Used by
// the API layer (#165) to render list views and by tests to assert
// store state. The returned maps are independent of the store, but
// the *Entry pointers reference store-owned memory — treat as
// read-only.
func (s *Store) Snapshot() (digests map[string]*DigestEntry, instances map[string]*InstanceEntry) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	digests = make(map[string]*DigestEntry, len(s.digests))
	instances = make(map[string]*InstanceEntry, len(s.instances))
	for k, v := range s.digests {
		c := *v
		digests[k] = &c
	}
	for k, v := range s.instances {
		c := *v
		instances[k] = &c
	}
	return digests, instances
}

// LastHydrate returns the time of the most recent MarkHydrated /
// MarkDisabled call. Zero before the first hydrate completes.
// Surfaced on the `/cve/status` endpoint and used as the ETag base
// for read endpoints.
func (s *Store) LastHydrate() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastHydrate
}

// EntryCounts returns the current number of digest + instance cache
// entries. Surfaced on `/cve/status` and used as the ETag basis for
// read endpoints.
func (s *Store) EntryCounts() (digests, instances int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.digests), len(s.instances)
}
