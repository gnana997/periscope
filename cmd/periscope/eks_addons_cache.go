package main

// eks_addons_cache.go — bounded TTL cache for the
// /eks/addons endpoints. Mirrors eks_insights_cache.go almost
// verbatim — same cluster-keyed namespace, same list/detail
// discriminator, same evict-when-over-cap policy.
//
// Why 1h TTL: add-ons change on operator action (an explicit
// UpdateAddon / DeleteAddon API call), and AWS doesn't surface a
// fast-moving "addon installed at" timestamp. Polling more
// aggressively just burns AWS API budget without changing what the
// operator sees. Matches the Upgrade Insights cadence so a fleet
// view that opens both tabs hits AWS once per cluster per hour.
//
// One cache shared across the list and detail endpoints. The list
// entry is keyed "list" and the detail entries are keyed by addon
// name — both within a per-cluster namespace.

import (
	"sort"
	"sync"
	"time"
)

const eksAddonsCacheMaxEntries = 256

type eksAddonsCacheValue struct {
	List   *AddonsListResponse
	Detail *AddonDetail
}

type eksAddonsCacheEntry struct {
	value   eksAddonsCacheValue
	expires time.Time
}

type eksAddonsCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	m   map[string]eksAddonsCacheEntry
}

func newEKSAddonsCache(ttl time.Duration) *eksAddonsCache {
	return &eksAddonsCache{
		ttl: ttl,
		max: eksAddonsCacheMaxEntries,
		m:   make(map[string]eksAddonsCacheEntry),
	}
}

func (c *eksAddonsCache) GetList(cluster string) (*AddonsListResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[addonsListKey(cluster)]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, addonsListKey(cluster))
		return nil, false
	}
	return e.value.List, e.value.List != nil
}

func (c *eksAddonsCache) PutList(cluster string, val AddonsListResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[addonsListKey(cluster)] = eksAddonsCacheEntry{
		value:   eksAddonsCacheValue{List: &val},
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

func (c *eksAddonsCache) GetDetail(cluster, name string) (*AddonDetail, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[addonsDetailKey(cluster, name)]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, addonsDetailKey(cluster, name))
		return nil, false
	}
	return e.value.Detail, e.value.Detail != nil
}

func (c *eksAddonsCache) PutDetail(cluster, name string, val AddonDetail) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[addonsDetailKey(cluster, name)] = eksAddonsCacheEntry{
		value:   eksAddonsCacheValue{Detail: &val},
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

func (c *eksAddonsCache) evictLocked() {
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

func (c *eksAddonsCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func addonsListKey(cluster string) string         { return cluster + "|list" }
func addonsDetailKey(cluster, name string) string { return cluster + "|d|" + name }
