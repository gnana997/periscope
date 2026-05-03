package main

// helm_cache.go — TTL cache for the /helm/releases list endpoint.
//
// Mirrors fleet_cache.go's shape: per (actor, cluster, impersonation
// hash). 30s TTL — long enough to absorb the SPA's tab-switch
// re-fetches without re-listing the storage Secrets every time, short
// enough that a freshly-deployed release shows up within a minute.
//
// Detail / history / diff are NOT cached server-side: per-revision
// blobs are immutable, so the browser's HTTP cache (Cache-Control
// max-age=60) plus TanStack Query staleness covers them without
// duplicating state.

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

type helmListCache struct {
	ttl time.Duration
	m   sync.Map // key string → helmListCacheEntry
}

type helmListCacheEntry struct {
	value   HelmReleasesResponse
	expires time.Time
}

func newHelmListCache(ttl time.Duration) *helmListCache {
	return &helmListCache{ttl: ttl}
}

func (c *helmListCache) Get(actor, cluster string, groups []string) (HelmReleasesResponse, bool) {
	k := helmListCacheKey(actor, cluster, groups)
	v, ok := c.m.Load(k)
	if !ok {
		return HelmReleasesResponse{}, false
	}
	e := v.(helmListCacheEntry)
	if time.Now().After(e.expires) {
		c.m.Delete(k)
		return HelmReleasesResponse{}, false
	}
	return e.value, true
}

func (c *helmListCache) Put(actor, cluster string, groups []string, val HelmReleasesResponse) {
	k := helmListCacheKey(actor, cluster, groups)
	c.m.Store(k, helmListCacheEntry{
		value:   val,
		expires: time.Now().Add(c.ttl),
	})
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
