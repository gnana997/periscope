package main

// eks_addon_versions_cache.go — TTL cache for DescribeAddonVersions
// catalog lookups, keyed by (addonName, k8sVersion).
//
// Different shape from the per-cluster eksAddonsCache: keys here are
// NOT cluster-scoped because the answer doesn't depend on which
// cluster asked. AWS publishes the (addonName, k8sVersion) → version
// list as a global catalog. A fleet-wide view of N clusters all
// running coredns on 1.30 hits AWS once per (addon, k8sVer) every 6h,
// not N times per cluster per cache-flush.
//
// 6h TTL: the issue specifies this number. AWS publishes new add-on
// versions roughly weekly; 6h is well inside any operator's "I want
// to know now" window while making the AWS API budget for catalog
// queries effectively free.
//
// Sticky-error semantics: a forbidden / throttled response is cached
// for the same TTL alongside successes. This prevents a misconfigured
// IAM policy or AWS-side rate limit from triggering a retry storm
// across every list/detail invocation. Mirrors amiCatalogCache's
// cached-error contract.

import (
	"sort"
	"sync"
	"time"
)

const addonVersionsCacheMaxEntries = 256

// addonVersionsCacheValue is the catalog payload for one
// (addonName, k8sVersion) tuple. nil = "AWS returned no versions"
// (degenerate but legal); the caller treats nil-with-no-error as
// "no update available".
type addonVersionsCacheValue struct {
	// Versions is the full ordered list returned by
	// DescribeAddonVersions for this (addonName, k8sVersion). Order is
	// AWS's; we preserve it so the SPA's "version history" tab matches
	// the Console.
	Versions []AddonVersionEntry
	// Latest is the highest-version entry whose Compatibilities
	// include the queried k8sVersion. May be empty if AWS returned no
	// versions or none claim compat with the version we asked about.
	Latest string
	// DefaultVersion is the AWS-marked default for this k8sVer. The
	// SPA uses it as a hint but does not display it differently from
	// other versions today.
	DefaultVersion string
}

type addonVersionsCacheEntry struct {
	value   *addonVersionsCacheValue // nil = "lookup returned no answer"
	err     error                    // sticky error so a 6h-misconfigured-IAM doesn't burn quota
	expires time.Time
}

type addonVersionsCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	m   map[string]addonVersionsCacheEntry
}

func newAddonVersionsCache(ttl time.Duration) *addonVersionsCache {
	return &addonVersionsCache{
		ttl: ttl,
		max: addonVersionsCacheMaxEntries,
		m:   make(map[string]addonVersionsCacheEntry),
	}
}

// Get returns the cached lookup, if any. Three return shapes:
//
//	ok=false          : cache miss; caller does the AWS call
//	ok=true, err==nil : cached success — value points to the catalog
//	                    entry (nil if AWS returned no versions)
//	ok=true, err!=nil : cached failure; do not retry
func (c *addonVersionsCache) Get(addonName, k8sVersion string) (*addonVersionsCacheValue, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[addonVersionsKey(addonName, k8sVersion)]
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, addonVersionsKey(addonName, k8sVersion))
		return nil, nil, false
	}
	return e.value, e.err, true
}

func (c *addonVersionsCache) Put(addonName, k8sVersion string, val *addonVersionsCacheValue, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[addonVersionsKey(addonName, k8sVersion)] = addonVersionsCacheEntry{
		value:   val,
		err:     err,
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

func (c *addonVersionsCache) evictLocked() {
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

func (c *addonVersionsCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func addonVersionsKey(addonName, k8sVersion string) string {
	return addonName + "|" + k8sVersion
}
