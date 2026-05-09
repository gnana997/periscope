package main

import (
	"errors"
	"testing"
	"time"
)

func TestAddonCatalogCache_PutGet(t *testing.T) {
	c := newAddonCatalogCache(time.Hour)
	val := &addonCatalogCacheValue{
		KubernetesVersion: "1.30",
		Entries: []CatalogAddon{
			{Name: "vpc-cni", Owner: "aws"},
		},
	}
	c.Put("1.30", val, nil)

	got, err, hit := c.Get("1.30")
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

func TestAddonCatalogCache_StickyError(t *testing.T) {
	c := newAddonCatalogCache(time.Hour)
	want := errors.New("AccessDenied")
	c.Put("1.30", nil, want)

	got, err, hit := c.Get("1.30")
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

func TestAddonCatalogCache_Expiry(t *testing.T) {
	c := newAddonCatalogCache(time.Millisecond)
	c.Put("1.30", &addonCatalogCacheValue{}, nil)
	time.Sleep(10 * time.Millisecond)
	if _, _, hit := c.Get("1.30"); hit {
		t.Errorf("expected expired entry to miss")
	}
}

func TestAddonCatalogCache_KeyPerK8sVersion(t *testing.T) {
	c := newAddonCatalogCache(time.Hour)
	c.Put("1.29", &addonCatalogCacheValue{KubernetesVersion: "1.29"}, nil)
	c.Put("1.30", &addonCatalogCacheValue{KubernetesVersion: "1.30"}, nil)

	a, _, _ := c.Get("1.29")
	b, _, _ := c.Get("1.30")
	if a == nil || b == nil {
		t.Fatalf("a=%v b=%v", a, b)
	}
	if a.KubernetesVersion == b.KubernetesVersion {
		t.Errorf("keys collided: a=%s b=%s", a.KubernetesVersion, b.KubernetesVersion)
	}
}

func TestAddonCatalogCache_Eviction(t *testing.T) {
	c := newAddonCatalogCache(time.Hour)
	c.max = 3

	c.Put("1.27", &addonCatalogCacheValue{}, nil)
	c.Put("1.28", &addonCatalogCacheValue{}, nil)
	c.Put("1.29", &addonCatalogCacheValue{}, nil)
	c.Put("1.30", &addonCatalogCacheValue{}, nil)

	if got := c.Len(); got > 3 {
		t.Fatalf("cache exceeded cap: len = %d, want <= 3", got)
	}
}
