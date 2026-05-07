package main

import (
	"errors"
	"testing"
	"time"
)

func TestAddonConfigSchemaCache_PutGet(t *testing.T) {
	c := newAddonConfigSchemaCache(time.Hour)
	val := &addonConfigSchemaCacheValue{ConfigurationSchema: `{"type":"object"}`}
	c.Put("vpc-cni", "v1.18.0", val, nil)

	got, err, hit := c.Get("vpc-cni", "v1.18.0")
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

func TestAddonConfigSchemaCache_StickyError(t *testing.T) {
	c := newAddonConfigSchemaCache(time.Hour)
	want := errors.New("AccessDenied")
	c.Put("vpc-cni", "v1.18.0", nil, want)

	got, err, hit := c.Get("vpc-cni", "v1.18.0")
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

func TestAddonConfigSchemaCache_Expiry(t *testing.T) {
	c := newAddonConfigSchemaCache(time.Millisecond)
	c.Put("vpc-cni", "v1.18.0", &addonConfigSchemaCacheValue{}, nil)
	time.Sleep(10 * time.Millisecond)
	if _, _, hit := c.Get("vpc-cni", "v1.18.0"); hit {
		t.Errorf("expected expired entry to miss")
	}
}

func TestAddonConfigSchemaCache_KeyDistinguishesAddonAndVersion(t *testing.T) {
	c := newAddonConfigSchemaCache(time.Hour)
	c.Put("vpc-cni", "v1.18.0", &addonConfigSchemaCacheValue{ConfigurationSchema: "a"}, nil)
	c.Put("vpc-cni", "v1.17.0", &addonConfigSchemaCacheValue{ConfigurationSchema: "b"}, nil)
	c.Put("coredns", "v1.18.0", &addonConfigSchemaCacheValue{ConfigurationSchema: "c"}, nil)

	a, _, _ := c.Get("vpc-cni", "v1.18.0")
	b, _, _ := c.Get("vpc-cni", "v1.17.0")
	d, _, _ := c.Get("coredns", "v1.18.0")
	if a == nil || b == nil || d == nil {
		t.Fatalf("a=%v b=%v d=%v", a, b, d)
	}
	if a.ConfigurationSchema == b.ConfigurationSchema || a.ConfigurationSchema == d.ConfigurationSchema {
		t.Errorf("keys collided: a=%s b=%s d=%s", a.ConfigurationSchema, b.ConfigurationSchema, d.ConfigurationSchema)
	}
}

func TestAddonConfigSchemaCache_Eviction(t *testing.T) {
	c := newAddonConfigSchemaCache(time.Hour)
	c.max = 3
	c.Put("a", "v1", &addonConfigSchemaCacheValue{}, nil)
	c.Put("b", "v1", &addonConfigSchemaCacheValue{}, nil)
	c.Put("c", "v1", &addonConfigSchemaCacheValue{}, nil)
	c.Put("d", "v1", &addonConfigSchemaCacheValue{}, nil)
	if got := c.Len(); got > 3 {
		t.Fatalf("cache exceeded cap: len = %d, want <= 3", got)
	}
}
