package main

// eks_ami_catalog_cache.go — TTL cache for "latest AMI" lookups.
//
// Different shape from the nodegroup cache: keys are
// (amiType, k8sVersion) tuples (NOT cluster-scoped) because the
// answer doesn't depend on which cluster asked. A fleet-wide view
// of 50 clusters all running AL2023 1.30 nodegroups burns one SSM
// call per (family, k8sVer) every 30 minutes, not 50.
//
// 30min TTL: the issue calls this number out and it's appropriate —
// AWS publishes new AL2023 EKS-optimized AMIs roughly weekly, so
// 30min is well inside any operator's "I want to know now" window
// while still making the AWS API budget negligible.

import (
	"sort"
	"sync"
	"time"

	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

const amiCatalogCacheMaxEntries = 256

type amiCatalogCacheEntry struct {
	value   *LatestAMI // nil = "lookup returned no answer" (negative cache)
	err     error      // sticky error so a 30min misconfigured-IAM doesn't burn quota
	expires time.Time
}

type amiCatalogCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	m   map[string]amiCatalogCacheEntry
}

func newAMICatalogCache(ttl time.Duration) *amiCatalogCache {
	return &amiCatalogCache{
		ttl: ttl,
		max: amiCatalogCacheMaxEntries,
		m:   make(map[string]amiCatalogCacheEntry),
	}
}

// Get returns the cached lookup, if any. Three return shapes:
//
//   ok=false           : cache miss; caller does the AWS call
//   ok=true, err==nil  : cached success — value points to the
//                        catalog entry (nil if the family is
//                        unsupported, see lookupSSM negative case)
//   ok=true, err!=nil  : cached failure; do not retry
func (c *amiCatalogCache) Get(amiType ekstypes.AMITypes, k8sVersion string) (*LatestAMI, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[amiCatalogKey(amiType, k8sVersion)]
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, amiCatalogKey(amiType, k8sVersion))
		return nil, nil, false
	}
	return e.value, e.err, true
}

func (c *amiCatalogCache) Put(amiType ekstypes.AMITypes, k8sVersion string, val *LatestAMI, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[amiCatalogKey(amiType, k8sVersion)] = amiCatalogCacheEntry{
		value:   val,
		err:     err,
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

func (c *amiCatalogCache) evictLocked() {
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

func (c *amiCatalogCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func amiCatalogKey(t ekstypes.AMITypes, k8sVersion string) string {
	return string(t) + "|" + k8sVersion
}
