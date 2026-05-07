package main

import (
	"testing"
	"time"
)

func TestEKSAddonsCache_ListPutGet(t *testing.T) {
	c := newEKSAddonsCache(time.Hour)
	val := AddonsListResponse{
		Addons: []AddonSummary{{Name: "vpc-cni", Status: "ACTIVE"}},
		Counts: AddonsCounts{Total: 1, Healthy: 1},
	}
	c.PutList("prod", val)

	got, ok := c.GetList("prod")
	if !ok {
		t.Fatalf("expected hit")
	}
	if len(got.Addons) != 1 || got.Addons[0].Name != "vpc-cni" {
		t.Fatalf("got = %+v, want one addon named vpc-cni", got)
	}
	if _, ok := c.GetList("staging"); ok {
		t.Fatalf("staging should miss — different cluster key")
	}
}

func TestEKSAddonsCache_DetailPutGet(t *testing.T) {
	c := newEKSAddonsCache(time.Hour)
	val := AddonDetail{
		AddonSummary: AddonSummary{Name: "coredns", Status: "ACTIVE", Version: "v1.10.1"},
	}
	c.PutDetail("prod", "coredns", val)

	got, ok := c.GetDetail("prod", "coredns")
	if !ok {
		t.Fatalf("expected hit")
	}
	if got.Name != "coredns" || got.Version != "v1.10.1" {
		t.Fatalf("got = %+v", got)
	}
	if _, ok := c.GetDetail("prod", "kube-proxy"); ok {
		t.Fatalf("different addon name should miss")
	}
	if _, ok := c.GetDetail("other", "coredns"); ok {
		t.Fatalf("different cluster should miss")
	}
}

func TestEKSAddonsCache_Expiry(t *testing.T) {
	c := newEKSAddonsCache(time.Millisecond)
	c.PutList("prod", AddonsListResponse{})
	time.Sleep(10 * time.Millisecond)
	if _, ok := c.GetList("prod"); ok {
		t.Fatalf("expected expired entry to miss")
	}
}

func TestEKSAddonsCache_Eviction(t *testing.T) {
	c := newEKSAddonsCache(time.Hour)
	c.max = 3

	c.PutList("a", AddonsListResponse{})
	c.PutList("b", AddonsListResponse{})
	c.PutList("c", AddonsListResponse{})
	c.PutList("d", AddonsListResponse{})

	if got := c.Len(); got > 3 {
		t.Fatalf("cache exceeded cap: len = %d, want <= 3", got)
	}
}
