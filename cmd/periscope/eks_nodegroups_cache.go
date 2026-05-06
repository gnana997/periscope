package main

// eks_nodegroups_cache.go — bounded TTL cache for the managed node
// group endpoints.
//
// Two divergences from eksInsightsCache, both deliberate:
//
//   1. Shorter TTL (5 minutes vs 1 hour). Node groups change when an
//      operator scales / upgrades / rotates them, which can happen
//      multiple times per day during an upgrade window. The Insights
//      cache mirrored AWS's daily refresh cadence; node groups don't
//      have that excuse.
//
//   2. Same cluster-keyed shape as the insights cache (no actor in
//      the key). DescribeNodegroup is not RBAC-filtered at our
//      boundary; per-actor keying would inflate cardinality without
//      a security benefit, exactly the same reasoning as insights.
//
// One cache shared across the list and detail endpoints. The list
// entry is keyed "list" and the detail entries are keyed by
// nodegroup name — both within a per-cluster namespace.

import (
	"sort"
	"sync"
	"time"
)

const eksNodegroupsCacheMaxEntries = 256

type eksNodegroupsCacheValue struct {
	List   *NodegroupsListResponse
	Detail *NodegroupDetail
}

type eksNodegroupsCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	m   map[string]eksNodegroupsCacheEntry
}

type eksNodegroupsCacheEntry struct {
	value   eksNodegroupsCacheValue
	expires time.Time
}

func newEKSNodegroupsCache(ttl time.Duration) *eksNodegroupsCache {
	return &eksNodegroupsCache{
		ttl: ttl,
		max: eksNodegroupsCacheMaxEntries,
		m:   make(map[string]eksNodegroupsCacheEntry),
	}
}

func (c *eksNodegroupsCache) GetList(cluster string) (*NodegroupsListResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[ngListKey(cluster)]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, ngListKey(cluster))
		return nil, false
	}
	return e.value.List, e.value.List != nil
}

func (c *eksNodegroupsCache) PutList(cluster string, val NodegroupsListResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[ngListKey(cluster)] = eksNodegroupsCacheEntry{
		value:   eksNodegroupsCacheValue{List: &val},
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

func (c *eksNodegroupsCache) GetDetail(cluster, name string) (*NodegroupDetail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[ngDetailKey(cluster, name)]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, ngDetailKey(cluster, name))
		return nil, false
	}
	return e.value.Detail, e.value.Detail != nil
}

func (c *eksNodegroupsCache) PutDetail(cluster, name string, val NodegroupDetail) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[ngDetailKey(cluster, name)] = eksNodegroupsCacheEntry{
		value:   eksNodegroupsCacheValue{Detail: &val},
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

// InvalidateCluster drops every entry for the given cluster. Not yet
// wired to a callsite — reserved for a future refresh button on the
// UI ("force re-fetch from AWS"). Cheap to add now since iterating
// the map under lock is O(n) and n ≤ max.
func (c *eksNodegroupsCache) InvalidateCluster(cluster string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := cluster + "|"
	for k := range c.m {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(c.m, k)
		}
	}
}

func (c *eksNodegroupsCache) evictLocked() {
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

func (c *eksNodegroupsCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func ngListKey(cluster string) string         { return cluster + "|list" }
func ngDetailKey(cluster, name string) string { return cluster + "|d|" + name }
