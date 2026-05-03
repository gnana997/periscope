package main

// helm_cache.go — bounded TTL cache for the /helm/releases list endpoint.
//
// Mirrors fleetCache's purpose (per-actor / per-cluster memoization)
// but adds a hard size cap — without it, sync.Map cardinality grows
// unbounded as new actor hashes accumulate over time. With the cap,
// the worst-case memory is bounded by helmListCacheMaxEntries.
//
// Eviction policy on Put when over cap:
//
//   1. Sweep all expired entries (cheap; entries past TTL are dead anyway).
//   2. If still over cap, evict the entries with the oldest expiration.
//      LRU would be more accurate but requires per-Get bookkeeping under
//      lock; the oldest-expiry approach is a good approximation when TTL
//      is uniform (which it is here).
//
// Detail / history / diff are NOT cached server-side: per-revision blobs
// are immutable, so the browser's HTTP cache (Cache-Control max-age=60)
// plus TanStack Query staleness covers them without duplicating state.

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

// helmListCacheMaxEntries caps cache memory. With ~256 bytes per
// HelmReleasesResponse summary slot × ~10 releases per actor average,
// 1024 entries ≈ 2.5 MiB worst case. Tune via the env var if needed.
const helmListCacheMaxEntries = 1024

type helmListCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	m   map[string]helmListCacheEntry
}

type helmListCacheEntry struct {
	value   HelmReleasesResponse
	expires time.Time
}

func newHelmListCache(ttl time.Duration) *helmListCache {
	return &helmListCache{
		ttl: ttl,
		max: helmListCacheMaxEntries,
		m:   make(map[string]helmListCacheEntry),
	}
}

func (c *helmListCache) Get(actor, cluster string, groups []string) (HelmReleasesResponse, bool) {
	k := helmListCacheKey(actor, cluster, groups)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok {
		return HelmReleasesResponse{}, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, k)
		return HelmReleasesResponse{}, false
	}
	return e.value, true
}

func (c *helmListCache) Put(actor, cluster string, groups []string, val HelmReleasesResponse) {
	k := helmListCacheKey(actor, cluster, groups)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = helmListCacheEntry{
		value:   val,
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

// evictLocked is called with c.mu held when the map is over cap. Sweeps
// expired entries first; if still over cap, evicts the entries with the
// oldest expiration until back at 90% of cap.
func (c *helmListCache) evictLocked() {
	now := time.Now()
	for k, e := range c.m {
		if now.After(e.expires) {
			delete(c.m, k)
		}
	}
	if len(c.m) <= c.max {
		return
	}
	// Still over cap — sort by expiry and trim oldest. We aim for 90% of
	// max so the next Put doesn't immediately re-trigger eviction.
	type kv struct {
		key string
		exp time.Time
	}
	all := make([]kv, 0, len(c.m))
	for k, e := range c.m {
		all = append(all, kv{key: k, exp: e.expires})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].exp.Before(all[j].exp) })
	target := c.max * 9 / 10
	for i := 0; i < len(all)-target; i++ {
		delete(c.m, all[i].key)
	}
}

// Len returns the current entry count. Used by tests; not part of the
// hot path.
func (c *helmListCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// helmListCacheKey hashes the impersonation groups into the key so
// changes to a user's effective tier invalidate the entry without
// any explicit eviction step. Same shape as fleetCacheKey.
func helmListCacheKey(actor, cluster string, groups []string) string {
	g := append([]string(nil), groups...)
	sort.Strings(g)
	h := sha256.Sum256([]byte(strings.Join(g, "\x1f")))
	return actor + "|" + cluster + "|" + hex.EncodeToString(h[:8])
}
