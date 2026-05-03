package main

// caniCache memoises individual SAR/SSRR check results keyed by
// (actor, cluster, namespace, impersonation-hash, check-tuple-hash).
// Modelled on fleetCache; see fleet_cache.go for the rationale on
// lazy expiry and sync.Map.
//
// Adding the check-tuple to the key lets two requests with overlapping
// checks share entries — the SPA hits the same buttons across pages,
// so this is the common case.
//
// Cardinality is bounded by (actors * clusters * namespaces * tier *
// distinct-check-set). Stays small for v1's deployment shape.

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"
)

type caniCache struct {
	ttl time.Duration
	m   sync.Map // key string → caniCacheEntry
}

type caniCacheEntry struct {
	result  CanIResult
	expires time.Time
}

func newCanICache(ttl time.Duration) *caniCache {
	return &caniCache{ttl: ttl}
}

// Get returns the cached result for the supplied tuple if present and
// not expired.
func (c *caniCache) Get(actor, cluster string, groups []string, check CanICheck) (CanIResult, bool) {
	k := caniCacheKey(actor, cluster, groups, check)
	v, ok := c.m.Load(k)
	if !ok {
		return CanIResult{}, false
	}
	e := v.(caniCacheEntry)
	if time.Now().After(e.expires) {
		c.m.Delete(k)
		return CanIResult{}, false
	}
	return e.result, true
}

// Put stores a result with the cache's TTL.
func (c *caniCache) Put(actor, cluster string, groups []string, check CanICheck, result CanIResult) {
	k := caniCacheKey(actor, cluster, groups, check)
	c.m.Store(k, caniCacheEntry{
		result:  result,
		expires: time.Now().Add(c.ttl),
	})
}

// caniCacheKey hashes the impersonation groups and the check tuple
// into the key. Groups are sorted so {"a","b"} and {"b","a"} collide;
// the check tuple uses a fixed field order.
//
// Shared-mode optimisation: when groups is empty (no impersonation),
// the apiserver evaluates SAR/SSRR against the dashboard's pod role
// and the answer is identical for every user. We drop actor from the
// key in that case so all shared-mode users share one entry per
// (cluster, check) — bounded cardinality, free perf win.
func caniCacheKey(actor, cluster string, groups []string, check CanICheck) string {
	g := append([]string(nil), groups...)
	sort.Strings(g)
	gh := sha256.Sum256([]byte(strings.Join(g, "\x1f")))

	// Include all attributes that affect the apiserver's authorization
	// decision. Name is included because RBAC ResourceNames can scope
	// rules to specific objects.
	tuple := strings.Join([]string{
		check.Verb, check.Group, check.Resource, check.Subresource,
		check.Namespace, check.Name,
	}, "\x1e")
	th := sha256.Sum256([]byte(tuple))

	keyActor := actor
	if len(groups) == 0 {
		// Shared-mode collapse — see comment above.
		keyActor = ""
	}
	return keyActor + "|" + cluster + "|" +
		hex.EncodeToString(gh[:8]) + "|" +
		hex.EncodeToString(th[:8])
}
