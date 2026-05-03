package main

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

// fleetCache is a tiny per-entry TTL cache used by the /api/fleet
// handler. The cache key is (actor, cluster, impersonation-hash) so
// users at different tiers (e.g., admin vs. triage on the same actor —
// not currently possible but defensive against future tier-elevation
// paths) never share entries.
//
// Backed by a sync.Map; entries are expired lazily on Get. There is
// no explicit eviction loop — cardinality is bounded by
// (actors * clusters * tiers) which stays small for v1's deployment
// shape (single-replica, in-memory session store, tens of clusters).
type fleetCache struct {
	ttl time.Duration
	m   sync.Map // key string → fleetCacheEntry
}

type fleetCacheEntry struct {
	value   FleetClusterEntry
	expires time.Time
}

func newFleetCache(ttl time.Duration) *fleetCache {
	return &fleetCache{ttl: ttl}
}

// Get returns the cached entry for (actor, cluster, groups) if present
// and not expired.
func (c *fleetCache) Get(actor, cluster string, groups []string) (FleetClusterEntry, bool) {
	k := fleetCacheKey(actor, cluster, groups)
	v, ok := c.m.Load(k)
	if !ok {
		return FleetClusterEntry{}, false
	}
	e := v.(fleetCacheEntry)
	if time.Now().After(e.expires) {
		c.m.Delete(k)
		return FleetClusterEntry{}, false
	}
	return e.value, true
}

// Put stores an entry with the cache's TTL.
func (c *fleetCache) Put(actor, cluster string, groups []string, val FleetClusterEntry) {
	k := fleetCacheKey(actor, cluster, groups)
	c.m.Store(k, fleetCacheEntry{
		value:   val,
		expires: time.Now().Add(c.ttl),
	})
}

// fleetCacheKey hashes the impersonation groups into the key so
// changes to a user's effective tier invalidate the entry without
// any explicit eviction step.
func fleetCacheKey(actor, cluster string, groups []string) string {
	g := append([]string(nil), groups...)
	sort.Strings(g)
	h := sha256.Sum256([]byte(strings.Join(g, "\x1f")))
	return actor + "|" + cluster + "|" + hex.EncodeToString(h[:8])
}
