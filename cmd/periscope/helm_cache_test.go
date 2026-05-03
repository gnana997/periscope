package main

import (
	"strconv"
	"testing"
	"time"
)

func TestHelmListCache_SizeCapEvictsOldest(t *testing.T) {
	// Tiny cap (4) to exercise eviction without thousands of entries.
	// Insert 6, expect size ≤ 4 with the two oldest gone (we trim to
	// 90% of cap == 3; the 4th insert was the trigger so 3 survive +
	// the 5th and 6th = 4).
	c := &helmListCache{
		ttl: 10 * time.Minute,
		max: 4,
		m:   make(map[string]helmListCacheEntry),
	}
	for i := 1; i <= 6; i++ {
		c.Put("actor", "cluster-"+strconv.Itoa(i), nil, HelmReleasesResponse{})
		time.Sleep(time.Microsecond) // distinct expiry timestamps
	}
	if got := c.Len(); got > 4 {
		t.Errorf("after eviction, expected len <= 4, got %d", got)
	}
	if _, ok := c.Get("actor", "cluster-1", nil); ok {
		t.Error("cluster-1 should have been evicted (oldest)")
	}
	if _, ok := c.Get("actor", "cluster-6", nil); !ok {
		t.Error("cluster-6 should still be cached (newest)")
	}
}

func TestHelmListCache_GetExpired(t *testing.T) {
	c := &helmListCache{
		ttl: 1 * time.Millisecond,
		max: 100,
		m:   make(map[string]helmListCacheEntry),
	}
	c.Put("actor", "cluster", nil, HelmReleasesResponse{})
	time.Sleep(5 * time.Millisecond)
	if _, ok := c.Get("actor", "cluster", nil); ok {
		t.Error("expected expired entry to be evicted on Get")
	}
}
