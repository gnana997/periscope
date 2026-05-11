package cve

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestStore_UpsertGetRoundtrip(t *testing.T) {
	s := NewStore()
	now := time.Now()
	findings := []Finding{{CVE: "CVE-2026-0001", Severity: "HIGH"}}
	s.UpsertDigest("sha256:abc", findings, now)
	got := s.GetDigest("sha256:abc")
	if got == nil || len(got.Findings) != 1 || got.Findings[0].CVE != "CVE-2026-0001" {
		t.Fatalf("digest get: %+v", got)
	}
	if got.LastFetched != now {
		t.Errorf("LastFetched: want %v, got %v", now, got.LastFetched)
	}
	if s.GetDigest("missing") != nil {
		t.Error("missing digest: want nil")
	}
}

func TestStore_PodRefPreservedOnUpsert(t *testing.T) {
	s := NewStore()
	s.IncDigestRef("sha256:a")
	s.IncDigestRef("sha256:a")
	s.UpsertDigest("sha256:a", []Finding{{CVE: "x"}}, time.Now())
	got := s.GetDigest("sha256:a")
	if got == nil || got.PodRefs != 2 {
		t.Fatalf("PodRefs: want 2, got %+v", got)
	}
}

func TestStore_DecRefFloorAtZero(t *testing.T) {
	s := NewStore()
	s.IncDigestRef("d")
	s.DecDigestRef("d")
	s.DecDigestRef("d") // over-decrement; must not go negative
	if got := s.GetDigest("d"); got == nil || got.PodRefs != 0 {
		t.Fatalf("PodRefs: want 0, got %+v", got)
	}
}

func TestStore_IterStale(t *testing.T) {
	s := NewStore()
	old := time.Now().Add(-2 * time.Hour)
	fresh := time.Now()
	s.UpsertDigest("old-d", nil, old)
	s.UpsertDigest("fresh-d", nil, fresh)
	s.UpsertInstance("old-i", nil, OwnerUnmanaged, "", old)
	s.UpsertInstance("fresh-i", nil, OwnerUnmanaged, "", fresh)
	d, i := s.IterStale(time.Now(), 1*time.Hour)
	if len(d) != 1 || d[0] != "old-d" {
		t.Errorf("stale digests: %v", d)
	}
	if len(i) != 1 || i[0] != "old-i" {
		t.Errorf("stale instances: %v", i)
	}
}

func TestStore_IterEvictable(t *testing.T) {
	s := NewStore()
	now := time.Now()
	// Old, zero-ref → evictable.
	s.UpsertDigest("evict-me", nil, now.Add(-25*time.Hour))
	// Old, nonzero-ref → NOT evictable.
	s.UpsertDigest("keep-ref", nil, now.Add(-25*time.Hour))
	s.IncDigestRef("keep-ref")
	// Recent, zero-ref → NOT evictable yet.
	s.UpsertDigest("keep-recent", nil, now.Add(-1*time.Hour))

	d, _ := s.IterEvictable(now, 24*time.Hour)
	if len(d) != 1 || d[0] != "evict-me" {
		t.Fatalf("evictable: want [evict-me], got %v", d)
	}
}

func TestStore_DisabledState(t *testing.T) {
	s := NewStore()
	if s.Hydrated() || s.Disabled() {
		t.Fatal("fresh store should be neither hydrated nor disabled")
	}
	s.MarkDisabled()
	if !s.Hydrated() || !s.Disabled() {
		t.Fatal("after MarkDisabled: want hydrated && disabled")
	}
	// Idempotent: a second call must not panic.
	s.MarkDisabled()
}

func TestStore_WaitHydratedUnblocks(t *testing.T) {
	s := NewStore()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.WaitHydrated(ctx) }()
	// Give the goroutine a moment to enter the select.
	time.Sleep(10 * time.Millisecond)
	s.MarkHydrated()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitHydrated: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitHydrated did not return after MarkHydrated")
	}
}

func TestStore_WaitHydrated_AlreadyHydrated(t *testing.T) {
	s := NewStore()
	s.MarkHydrated()
	if err := s.WaitHydrated(context.Background()); err != nil {
		t.Fatalf("WaitHydrated: %v", err)
	}
}

func TestStore_WaitHydrated_CtxCancel(t *testing.T) {
	s := NewStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.WaitHydrated(ctx); err == nil {
		t.Fatal("WaitHydrated: want context.Canceled, got nil")
	}
}

func TestStore_ConcurrentReadsWrites(t *testing.T) {
	// Race-detector smoke test. 8 writers and 8 readers churn for
	// 1k iterations each; the test fails (under -race) on any
	// data race.
	s := NewStore()
	const n = 1000
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < n; i++ {
				k := "k" + itoa(i%32)
				s.UpsertDigest(k, []Finding{{CVE: "c"}}, time.Now())
				if i%4 == 0 {
					s.IncDigestRef(k)
				}
				if i%5 == 0 {
					s.DecDigestRef(k)
				}
			}
		}(w)
	}
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				_ = s.GetDigest("k" + itoa(i%32))
				if i%64 == 0 {
					_, _ = s.IterStale(time.Now(), time.Hour)
				}
			}
		}()
	}
	wg.Wait()
}

func TestStore_SnapshotIsIndependent(t *testing.T) {
	s := NewStore()
	s.UpsertDigest("d1", []Finding{{CVE: "x"}}, time.Now())
	digests, _ := s.Snapshot()
	// Mutate the snapshot — the original must be unaffected.
	digests["d1"].PodRefs = 99
	if got := s.GetDigest("d1"); got.PodRefs != 0 {
		t.Errorf("snapshot mutation leaked: PodRefs=%d", got.PodRefs)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := []byte{}
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	return string(out)
}
