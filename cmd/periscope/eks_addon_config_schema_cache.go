package main

// eks_addon_config_schema_cache.go — TTL cache for AWS-published
// add-on configuration JSON schemas, keyed by (addonName, version)
// (issue #119, PR-2).
//
// Different shape from eksAddonsCache and addonCatalogCache: schemas
// are immutable per addon-version pair (AWS publishes a schema with
// each version). The cache exists to spare the install dialog from
// re-fetching the schema when the operator toggles between versions
// in the picker, and to keep AWS API budget low across users.
//
// 24h TTL: schemas are content-addressed by version, so a long TTL
// is safe — any change to a schema means a new version with its own
// cache entry. Sticky errors at the same TTL prevent retry storms.

import (
	"sort"
	"sync"
	"time"
)

const addonConfigSchemaCacheMaxEntries = 128

type addonConfigSchemaCacheValue struct {
	// ConfigurationSchema is the raw JSON Schema string AWS returned.
	// Empty when AWS responded but had no schema for the version
	// (legitimate — older addon versions ship without one).
	ConfigurationSchema string
}

type addonConfigSchemaCacheEntry struct {
	value   *addonConfigSchemaCacheValue
	err     error
	expires time.Time
}

type addonConfigSchemaCache struct {
	ttl time.Duration
	max int
	mu  sync.Mutex
	m   map[string]addonConfigSchemaCacheEntry
}

func newAddonConfigSchemaCache(ttl time.Duration) *addonConfigSchemaCache {
	return &addonConfigSchemaCache{
		ttl: ttl,
		max: addonConfigSchemaCacheMaxEntries,
		m:   make(map[string]addonConfigSchemaCacheEntry),
	}
}

func (c *addonConfigSchemaCache) Get(addonName, version string) (*addonConfigSchemaCacheValue, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[addonConfigSchemaKey(addonName, version)]
	if !ok {
		return nil, nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.m, addonConfigSchemaKey(addonName, version))
		return nil, nil, false
	}
	return e.value, e.err, true
}

func (c *addonConfigSchemaCache) Put(addonName, version string, val *addonConfigSchemaCacheValue, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[addonConfigSchemaKey(addonName, version)] = addonConfigSchemaCacheEntry{
		value:   val,
		err:     err,
		expires: time.Now().Add(c.ttl),
	}
	if len(c.m) > c.max {
		c.evictLocked()
	}
}

func (c *addonConfigSchemaCache) evictLocked() {
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

func (c *addonConfigSchemaCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

func addonConfigSchemaKey(addonName, version string) string {
	return addonName + "|" + version
}
