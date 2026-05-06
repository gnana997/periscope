package main

import (
	"testing"
	"time"
)

func TestEKSInsightsCache_ListPutGet(t *testing.T) {
	c := newEKSInsightsCache(time.Hour)
	val := UpgradeInsightsListResponse{
		Insights: []UpgradeInsightSummary{{ID: "abc", Status: "PASSING"}},
		Counts:   UpgradeInsightCounts{Passing: 1},
	}
	c.PutList("prod", val)

	got, ok := c.GetList("prod")
	if !ok {
		t.Fatalf("expected hit")
	}
	if len(got.Insights) != 1 || got.Insights[0].ID != "abc" {
		t.Fatalf("got = %+v, want one insight with ID abc", got)
	}
	if _, ok := c.GetList("staging"); ok {
		t.Fatalf("staging should miss — different cluster key")
	}
}

func TestEKSInsightsCache_DetailPutGet(t *testing.T) {
	c := newEKSInsightsCache(time.Hour)
	val := UpgradeInsightDetail{
		UpgradeInsightSummary: UpgradeInsightSummary{ID: "i-1", Status: "ERROR"},
		Resources: []UpgradeInsightResourceRef{
			{KubernetesResourceURI: "/api/v1/pods/x"},
		},
	}
	c.PutDetail("prod", "i-1", val)

	got, ok := c.GetDetail("prod", "i-1")
	if !ok {
		t.Fatalf("expected hit")
	}
	if got.ID != "i-1" || len(got.Resources) != 1 {
		t.Fatalf("got = %+v", got)
	}
	if _, ok := c.GetDetail("prod", "i-2"); ok {
		t.Fatalf("different insightId should miss")
	}
	if _, ok := c.GetDetail("other", "i-1"); ok {
		t.Fatalf("different cluster should miss")
	}
}

func TestEKSInsightsCache_Expiry(t *testing.T) {
	c := newEKSInsightsCache(time.Millisecond)
	c.PutList("prod", UpgradeInsightsListResponse{})
	time.Sleep(10 * time.Millisecond)
	if _, ok := c.GetList("prod"); ok {
		t.Fatalf("expected expired entry to miss")
	}
}

func TestEKSInsightsCache_Eviction(t *testing.T) {
	// Force the cap below the natural ceiling so we can exercise the
	// eviction path deterministically. 4 entries → cap 3 → after the
	// 4th Put we should be at len 3 (90% of cap, rounded down to int).
	c := newEKSInsightsCache(time.Hour)
	c.max = 3

	c.PutList("a", UpgradeInsightsListResponse{})
	c.PutList("b", UpgradeInsightsListResponse{})
	c.PutList("c", UpgradeInsightsListResponse{})
	c.PutList("d", UpgradeInsightsListResponse{})

	if got := c.Len(); got > 3 {
		t.Fatalf("cache exceeded cap: len = %d, want <= 3", got)
	}
}
