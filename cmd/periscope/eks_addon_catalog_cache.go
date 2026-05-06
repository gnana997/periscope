package main

// eks_addon_catalog_cache.go — TTL cache for the unfiltered
// DescribeAddonVersions response, keyed by k8sVersion alone.
//
// Different shape from both eksAddonsCache (per-cluster, list/detail
// of installed add-ons) and addonVersionsCache (per-(addonName,
// k8sVersion), filtered catalog of one addon). The catalog answers
// "what could I install on a 1.30 cluster?" — the answer depends only
// on the k8s version, so a fleet of N clusters running 1.30 hits AWS
// once per 6h, not N times.
//
// 6h TTL: matches addonVersionsCache. AWS publishes new addon
// versions roughly weekly; 6h is well inside any operator's "I want
// to know now" window while keeping the AWS API budget effectively
// free. Sticky errors (forbidden/throttled) cached at the same TTL
// to prevent retry storms.
//
// Tiny cap (16): an EKS fleet runs at most ~5 concurrent k8s minor
// versions. 16 leaves room for stragglers without bounding growth.

import (
	"sort"
	"sync"
	"time"
)

const addonCatalogCacheMaxEntries = 16

// addonCatalogCacheValue holds the flattened catalog payload for one
// k8sVer. Layered with per-cluster install state at request time —
// the cache itself is install-agnostic.
type addonCatalogCacheValue struct {
	Entries []CatalogAddon
	// KubernetesVersion is the value AWS keyed against; copied here
	// so callers don't need a parallel map.
	KubernetesVersion string
}

type addonCatalogCacheEntry struct {
	value   *addonCatalogCacheValue
	err     error
	expires time.Time
}

type addonCatalogCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	m   map[string]addonCatalogCacheEntry
}

func newAddonCatalogCache(ttl time.Duration) *addonCatalogCache {
	return &addonCatalogCache{
		ttl: ttl,
		max: addonCatalogCacheMaxEntries,
		m:   make(map[string]addonCatalogCacheEntry),
	}
}

// Get returns the cached catalog, if any.
//
//	ok=false          : cache miss; caller does the AWS call
//	ok=true, err==nil : cached success
//	ok=true, err!=nil : cached failure; do not retry
func (c *addonCatalogCache) Get(k8sVersion string) (*addonCatalogCacheValue, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k8sVersion]
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, k8sVersion)
		return nil, nil, false
	}
	return e.value, e.err, true
}

func (c *addonCatalogCache) Put(k8sVersion string, val *addonCatalogCacheValue, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k8sVersion] = addonCatalogCacheEntry{
		value:   val,
		err:     err,
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

func (c *addonCatalogCache) evictLocked() {
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
	if target < 1 {
		target = 1
	}
	for i := 0; i < len(all)-target; i++ {
		delete(c.m, all[i].key)
	}
}

func (c *addonCatalogCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}
