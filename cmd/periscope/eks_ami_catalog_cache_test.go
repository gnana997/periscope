package main

import (
	"errors"
	"testing"
	"time"

	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

func TestAMICatalogCache_PutGet(t *testing.T) {
	c := newAMICatalogCache(time.Hour)
	val := &LatestAMI{ImageID: "ami-1", Source: "ssm"}
	c.Put(ekstypes.AMITypesAl2023X8664Standard, "1.30", val, nil)

	got, err, hit := c.Get(ekstypes.AMITypesAl2023X8664Standard, "1.30")
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

func TestAMICatalogCache_StickyError(t *testing.T) {
	c := newAMICatalogCache(time.Hour)
	want := errors.New("AccessDenied")
	c.Put(ekstypes.AMITypesAl2023X8664Standard, "1.30", nil, want)

	got, err, hit := c.Get(ekstypes.AMITypesAl2023X8664Standard, "1.30")
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

func TestAMICatalogCache_Expiry(t *testing.T) {
	c := newAMICatalogCache(time.Millisecond)
	c.Put(ekstypes.AMITypesAl2023X8664Standard, "1.30", &LatestAMI{}, nil)
	time.Sleep(10 * time.Millisecond)
	if _, _, hit := c.Get(ekstypes.AMITypesAl2023X8664Standard, "1.30"); hit {
		t.Errorf("expected expired entry to miss")
	}
}

func TestAMICatalogCache_KeyDistinguishesAMITypeAndK8s(t *testing.T) {
	c := newAMICatalogCache(time.Hour)
	c.Put(ekstypes.AMITypesAl2023X8664Standard, "1.30", &LatestAMI{ImageID: "a"}, nil)
	c.Put(ekstypes.AMITypesAl2023Arm64Standard, "1.30", &LatestAMI{ImageID: "b"}, nil)
	c.Put(ekstypes.AMITypesAl2023X8664Standard, "1.31", &LatestAMI{ImageID: "c"}, nil)

	a, _, _ := c.Get(ekstypes.AMITypesAl2023X8664Standard, "1.30")
	b, _, _ := c.Get(ekstypes.AMITypesAl2023Arm64Standard, "1.30")
	d, _, _ := c.Get(ekstypes.AMITypesAl2023X8664Standard, "1.31")

	if a == nil || b == nil || d == nil {
		t.Fatalf("a=%v b=%v d=%v", a, b, d)
	}
	if a.ImageID == b.ImageID || a.ImageID == d.ImageID {
		t.Errorf("keys collided: a=%s b=%s d=%s", a.ImageID, b.ImageID, d.ImageID)
	}
}
