package identity

import (
	"sync"
	"testing"
	"time"
)

func TestStore_NewIsEmpty(t *testing.T) {
	s := NewStore()
	if s.HasSnapshot() {
		t.Errorf("HasSnapshot() = true on new store, want false")
	}
	if s.Snapshot() != nil {
		t.Errorf("Snapshot() = non-nil on new store, want nil")
	}
}

func TestStore_ReplaceAndSnapshot(t *testing.T) {
	s := NewStore()
	now := time.Now()
	s.Replace([]SARoleIndexEntry{
		{Cluster: "c1", Namespace: "ns-z", SAName: "sa"},
		{Cluster: "c1", Namespace: "ns-a", SAName: "sa2"},
		{Cluster: "c1", Namespace: "ns-a", SAName: "sa1"},
	}, now)
	if !s.HasSnapshot() {
		t.Errorf("HasSnapshot() = false after Replace, want true")
	}
	got := s.Snapshot()
	if len(got) != 3 {
		t.Fatalf("len(snapshot) = %d, want 3", len(got))
	}
	// Sorted by (ns, sa)
	want := []struct{ ns, sa string }{
		{"ns-a", "sa1"}, {"ns-a", "sa2"}, {"ns-z", "sa"},
	}
	for i, w := range want {
		if got[i].Namespace != w.ns || got[i].SAName != w.sa {
			t.Errorf("snapshot[%d] = (%s,%s), want (%s,%s)", i, got[i].Namespace, got[i].SAName, w.ns, w.sa)
		}
	}
}

func TestStore_ReplaceIsAtomic(t *testing.T) {
	// New Replace fully supplants the old snapshot — no "merge".
	s := NewStore()
	now := time.Now()
	s.Replace([]SARoleIndexEntry{
		{Namespace: "ns", SAName: "old"},
	}, now)
	s.Replace([]SARoleIndexEntry{
		{Namespace: "ns", SAName: "new"},
	}, now.Add(time.Second))
	got := s.Snapshot()
	if len(got) != 1 || got[0].SAName != "new" {
		t.Errorf("after second Replace, snapshot = %+v, want only 'new'", got)
	}
}

func TestStore_AgeBeforeFirstBuild(t *testing.T) {
	s := NewStore()
	if age := s.Age(time.Now()); age < time.Hour {
		t.Errorf("Age() before first build = %v, want effectively infinite", age)
	}
}

func TestStore_AgeAfterReplace(t *testing.T) {
	s := NewStore()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.Replace(nil, t0)
	if age := s.Age(t0.Add(90 * time.Second)); age != 90*time.Second {
		t.Errorf("Age 90s after Replace = %v, want 90s", age)
	}
}

func TestStore_InvalidateForcesAgeRebuild(t *testing.T) {
	s := NewStore()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.Replace([]SARoleIndexEntry{{Namespace: "ns", SAName: "sa"}}, t0)
	if age := s.Age(t0.Add(30 * time.Second)); age != 30*time.Second {
		t.Fatalf("pre-invalidate Age = %v", age)
	}

	s.Invalidate("ns", "sa")
	// Snapshot should still return the entry — invalidation only marks
	// stale, doesn't blank the index.
	if got := s.Snapshot(); len(got) != 1 {
		t.Errorf("after Invalidate, snapshot = %+v, want still-populated", got)
	}
	// Age should now be huge so the next Ensure() rebuilds.
	if age := s.Age(t0.Add(30 * time.Second)); age < time.Hour {
		t.Errorf("after Invalidate, Age = %v, want effectively infinite", age)
	}
}

func TestStore_ConcurrentReadsDuringReplace(t *testing.T) {
	// Stress test: many readers + one writer, race detector verifies
	// no concurrent mutation panic.
	s := NewStore()
	s.Replace([]SARoleIndexEntry{
		{Namespace: "ns", SAName: "sa"},
	}, time.Now())

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = s.Snapshot()
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		s.Replace([]SARoleIndexEntry{
			{Namespace: "ns", SAName: "sa"},
		}, time.Now())
	}
	close(stop)
	wg.Wait()
}
