package main

// eks_insights_cache.go — bounded TTL cache for the
// /eks/upgrade-insights endpoints.
//
// Two important divergences from helmListCache (the closest
// existing analogue):
//
//   1. Cluster-keyed only, NOT actor-keyed. EKS Upgrade Insights
//      come from AWS at the cluster boundary; they are not
//      RBAC-filtered against the requesting user the way the helm
//      release list is. Per-actor keying would inflate cache
//      cardinality (1 entry per (user, cluster) pair instead of
//      per cluster) for no benefit, and would prevent shared cache
//      hits across users looking at the same cluster — which is
//      the common case for upgrade reviews.
//
//   2. Long TTL (1h). AWS itself only refreshes insights once per
//      day, so polling more aggressively just burns money on AWS
//      API calls without changing what the operator sees. The TTL
//      can still be busted by a process restart (the cache lives in
//      memory only).
//
// One cache shared across the list and detail endpoints. The list
// entry is keyed "list" and the detail entries are keyed by
// insightId — both within a per-cluster namespace.

import (
	"sort"
	"sync"
	"time"
)

// eksInsightsCacheMaxEntries caps the cache. With a few dozen
// insights per cluster × a list entry × ~50 clusters in a fleet,
// 256 entries is generous; the sweep keeps memory bounded under
// pathological registries too.
const eksInsightsCacheMaxEntries = 256

// eksInsightsCacheValue is the discriminated union the cache holds.
// Exactly one of List or Detail is non-nil for any given entry; the
// other is the zero value. Using pointers avoids copying the larger
// detail blob on Get.
type eksInsightsCacheValue struct {
	List   *UpgradeInsightsListResponse
	Detail *UpgradeInsightDetail
}

type eksInsightsCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	m   map[string]eksInsightsCacheEntry
}

type eksInsightsCacheEntry struct {
	value   eksInsightsCacheValue
	expires time.Time
}

func newEKSInsightsCache(ttl time.Duration) *eksInsightsCache {
	return &eksInsightsCache{
		ttl: ttl,
		max: eksInsightsCacheMaxEntries,
		m:   make(map[string]eksInsightsCacheEntry),
	}
}

// GetList returns the cached list response for cluster, if any.
// The returned pointer points into the cache; callers must treat
// the value as read-only.
func (c *eksInsightsCache) GetList(cluster string) (*UpgradeInsightsListResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[listKey(cluster)]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, listKey(cluster))
		return nil, false
	}
	return e.value.List, e.value.List != nil
}

// PutList stores the list response under (cluster, "list").
func (c *eksInsightsCache) PutList(cluster string, val UpgradeInsightsListResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[listKey(cluster)] = eksInsightsCacheEntry{
		value:   eksInsightsCacheValue{List: &val},
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

// GetDetail returns the cached detail blob for (cluster, insightId).
func (c *eksInsightsCache) GetDetail(cluster, insightID string) (*UpgradeInsightDetail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[detailKey(cluster, insightID)]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, detailKey(cluster, insightID))
		return nil, false
	}
	return e.value.Detail, e.value.Detail != nil
}

// PutDetail stores the detail blob under (cluster, insightId).
func (c *eksInsightsCache) PutDetail(cluster, insightID string, val UpgradeInsightDetail) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[detailKey(cluster, insightID)] = eksInsightsCacheEntry{
		value:   eksInsightsCacheValue{Detail: &val},
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

// evictLocked is called with c.mu held when the map is over cap.
// Sweeps expired entries first; if still over cap, trims the
// oldest-expiry entries down to 90% of cap. Mirrors helmListCache so
// behavior under memory pressure is consistent across handlers.
func (c *eksInsightsCache) evictLocked() {
	now := time.Now()
	for k, e := range c.m {
		if now.After(e.expires) {
			delete(c.m, k)
		}
	}
	if len(c.m) <= c.max {
		return
	}
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

// Len reports the current entry count. Test-only.
func (c *eksInsightsCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func listKey(cluster string) string         { return cluster + "|list" }
func detailKey(cluster, id string) string   { return cluster + "|d|" + id }
