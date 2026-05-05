package main

// helm_chart_cache.go — bounded TTL caches for the chart-fetch
// endpoints (issue #73). Two separate cache instances so the
// "list versions for a ref" and "fetch values for (ref, version)"
// operations evict and TTL independently:
//
//   chartVersionsCache  — keyed by ref           (5 min TTL, 256 cap)
//   chartValuesCache    — keyed by (ref, version) (15 min TTL, 256 cap)
//
// Different TTLs because different invariants:
//   - Versions list moves when the registry gains a new release.
//     5min keeps an operator who just pushed v1.2.3 from waiting too
//     long when they hit the version picker.
//   - A specific (ref, version) is immutable — once resolved, it
//     can't change. 15min is just to keep memory bounded; we'd
//     happily cache it forever if RAM were free.
//
// Chart fetches don't impersonate (refs are public in v1.1), so the
// cache keys don't need actor / cluster scoping. That simplifies the
// keying vs helmListCache.

import (
	"sort"
	"sync"
	"time"

	"github.com/gnana997/periscope/internal/k8s"
)

const (
	chartVersionsCacheTTL = 5 * time.Minute
	chartValuesCacheTTL   = 15 * time.Minute
	chartCacheMaxEntries  = 256
)

type chartVersionsCacheEntry struct {
	value   k8s.ChartVersionsResult
	expires time.Time
}

type chartValuesCacheEntry struct {
	value   k8s.ChartFetchResult
	expires time.Time
}

type chartVersionsCache struct {
	mu sync.Mutex
	m  map[string]chartVersionsCacheEntry
}

type chartValuesCache struct {
	mu sync.Mutex
	m  map[string]chartValuesCacheEntry
}

func newChartVersionsCache() *chartVersionsCache {
	return &chartVersionsCache{m: make(map[string]chartVersionsCacheEntry)}
}

func newChartValuesCache() *chartValuesCache {
	return &chartValuesCache{m: make(map[string]chartValuesCacheEntry)}
}

// Get returns the cached value if present and unexpired. Caller is
// responsible for the cache key — for versions it's the ref; for
// values it's "ref|chartName|version" via chartValuesKey.
func (c *chartVersionsCache) Get(key string) (k8s.ChartVersionsResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(c.m, key)
		}
		return k8s.ChartVersionsResult{}, false
	}
	return e.value, true
}

func (c *chartVersionsCache) Put(key string, val k8s.ChartVersionsResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = chartVersionsCacheEntry{value: val, expires: time.Now().Add(chartVersionsCacheTTL)}
	if len(c.m) > chartCacheMaxEntries {
		c.evictLocked()
	}
}

func (c *chartVersionsCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

func (c *chartVersionsCache) evictLocked() {
	now := time.Now()
	for k, e := range c.m {
		if now.After(e.expires) {
			delete(c.m, k)
		}
	}
	if len(c.m) <= chartCacheMaxEntries {
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
	target := chartCacheMaxEntries * 9 / 10
	for i := 0; i < len(all)-target; i++ {
		delete(c.m, all[i].key)
	}
}

func (c *chartValuesCache) Get(key string) (k8s.ChartFetchResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.expires) {
		if ok {
			delete(c.m, key)
		}
		return k8s.ChartFetchResult{}, false
	}
	return e.value, true
}

func (c *chartValuesCache) Put(key string, val k8s.ChartFetchResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[key] = chartValuesCacheEntry{value: val, expires: time.Now().Add(chartValuesCacheTTL)}
	if len(c.m) > chartCacheMaxEntries {
		c.evictValuesLocked()
	}
}

func (c *chartValuesCache) Invalidate(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}

func (c *chartValuesCache) evictValuesLocked() {
	now := time.Now()
	for k, e := range c.m {
		if now.After(e.expires) {
			delete(c.m, k)
		}
	}
	if len(c.m) <= chartCacheMaxEntries {
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
	target := chartCacheMaxEntries * 9 / 10
	for i := 0; i < len(all)-target; i++ {
		delete(c.m, all[i].key)
	}
}

// chartValuesKey hashes (ref, chartName, version) into a stable key.
// chartName is empty for OCI (the ref encodes the chart name); for
// HTTP repos it's the index entry name. The triple uniquely
// identifies a chart artifact.
func chartValuesKey(ref, chartName, version string) string {
	return ref + "|" + chartName + "|" + version
}
