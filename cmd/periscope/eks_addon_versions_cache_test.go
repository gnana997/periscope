package main

import (
	"errors"
	"testing"
	"time"
)

func TestAddonVersionsCache_PutGet(t *testing.T) {
	c := newAddonVersionsCache(time.Hour)
	val := &addonVersionsCacheValue{
		Versions: []AddonVersionEntry{{Version: "v1.18.0"}},
		Latest:   "v1.18.0",
	}
	c.Put("vpc-cni", "1.30", val, nil)

	got, err, hit := c.Get("vpc-cni", "1.30")
	if !hit {
		t.Fatalf("expected hit")
	}
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got != val {
		t.Errorf("got = %+v", got)
	}
}

func TestAddonVersionsCache_StickyError(t *testing.T) {
	c := newAddonVersionsCache(time.Hour)
	want := errors.New("AccessDenied")
	c.Put("vpc-cni", "1.30", nil, want)

	got, err, hit := c.Get("vpc-cni", "1.30")
	if !hit {
		t.Fatalf("expected hit")
	}
	if !errors.Is(err, want) && err.Error() != want.Error() {
		t.Errorf("err = %v", err)
	}
	if got != nil {
		t.Errorf("got = %+v", got)
	}
}

func TestAddonVersionsCache_Expiry(t *testing.T) {
	c := newAddonVersionsCache(time.Millisecond)
	c.Put("vpc-cni", "1.30", &addonVersionsCacheValue{}, nil)
	time.Sleep(10 * time.Millisecond)
	if _, _, hit := c.Get("vpc-cni", "1.30"); hit {
		t.Errorf("expected expired entry to miss")
	}
}

func TestAddonVersionsCache_KeyDistinguishesAddonAndK8s(t *testing.T) {
	c := newAddonVersionsCache(time.Hour)
	c.Put("vpc-cni", "1.30", &addonVersionsCacheValue{Latest: "a"}, nil)
	c.Put("coredns", "1.30", &addonVersionsCacheValue{Latest: "b"}, nil)
	c.Put("vpc-cni", "1.31", &addonVersionsCacheValue{Latest: "c"}, nil)

	a, _, _ := c.Get("vpc-cni", "1.30")
	b, _, _ := c.Get("coredns", "1.30")
	d, _, _ := c.Get("vpc-cni", "1.31")

	if a == nil || b == nil || d == nil {
		t.Fatalf("a=%v b=%v d=%v", a, b, d)
	}
	if a.Latest == b.Latest || a.Latest == d.Latest {
		t.Errorf("keys collided: a=%s b=%s d=%s", a.Latest, b.Latest, d.Latest)
	}
}

func TestAddonVersionsCache_Eviction(t *testing.T) {
	c := newAddonVersionsCache(time.Hour)
	c.max = 3

	c.Put("a", "1.30", &addonVersionsCacheValue{}, nil)
	c.Put("b", "1.30", &addonVersionsCacheValue{}, nil)
	c.Put("c", "1.30", &addonVersionsCacheValue{}, nil)
	c.Put("d", "1.30", &addonVersionsCacheValue{}, nil)

	if got := c.Len(); got > 3 {
		t.Fatalf("cache exceeded cap: len = %d, want <= 3", got)
	}
}
