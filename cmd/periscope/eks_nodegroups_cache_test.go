package main

import (
	"testing"
	"time"
)

func TestEKSNodegroupsCache_ListPutGet(t *testing.T) {
	c := newEKSNodegroupsCache(time.Hour)
	val := NodegroupsListResponse{
		Nodegroups: []NodegroupSummary{{Name: "ng-1"}},
		Counts:     NodegroupsCounts{Total: 1},
	}
	c.PutList("prod", val)

	got, ok := c.GetList("prod")
	if !ok || len(got.Nodegroups) != 1 || got.Nodegroups[0].Name != "ng-1" {
		t.Fatalf("got %+v", got)
	}
	if _, ok := c.GetList("staging"); ok {
		t.Fatalf("staging miss expected")
	}
}

func TestEKSNodegroupsCache_DetailKeying(t *testing.T) {
	c := newEKSNodegroupsCache(time.Hour)
	c.PutDetail("prod", "ng-a", NodegroupDetail{NodegroupSummary: NodegroupSummary{Name: "ng-a"}})

	if _, ok := c.GetDetail("prod", "ng-a"); !ok {
		t.Fatalf("expected hit")
	}
	if _, ok := c.GetDetail("prod", "ng-b"); ok {
		t.Fatalf("different name should miss")
	}
	if _, ok := c.GetDetail("other", "ng-a"); ok {
		t.Fatalf("different cluster should miss")
	}
}

func TestEKSNodegroupsCache_Expiry(t *testing.T) {
	c := newEKSNodegroupsCache(time.Millisecond)
	c.PutList("prod", NodegroupsListResponse{})
	time.Sleep(10 * time.Millisecond)
	if _, ok := c.GetList("prod"); ok {
		t.Fatalf("expected expired entry to miss")
	}
}

func TestEKSNodegroupsCache_InvalidateCluster(t *testing.T) {
	c := newEKSNodegroupsCache(time.Hour)
	c.PutList("prod", NodegroupsListResponse{})
	c.PutDetail("prod", "ng-1", NodegroupDetail{})
	c.PutList("staging", NodegroupsListResponse{})

	c.InvalidateCluster("prod")

	if _, ok := c.GetList("prod"); ok {
		t.Errorf("prod list should be evicted")
	}
	if _, ok := c.GetDetail("prod", "ng-1"); ok {
		t.Errorf("prod detail should be evicted")
	}
	if _, ok := c.GetList("staging"); !ok {
		t.Errorf("staging list should survive")
	}
}

func TestEKSNodegroupsCache_Eviction(t *testing.T) {
	c := newEKSNodegroupsCache(time.Hour)
	c.max = 3
	c.PutList("a", NodegroupsListResponse{})
	c.PutList("b", NodegroupsListResponse{})
	c.PutList("c", NodegroupsListResponse{})
	c.PutList("d", NodegroupsListResponse{})
	if got := c.Len(); got > 3 {
		t.Fatalf("cache exceeded cap: len = %d", got)
	}
}
