package main

import (
	"sync"
	"testing"
	"time"
)

func TestParseWatchStreamsEnv(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want watchStreamsConfig
	}{
		{name: "empty", raw: "", want: watchStreamsConfig{}},
		{name: "whitespace", raw: "   ", want: watchStreamsConfig{}},
		{name: "pods", raw: "pods", want: watchStreamsConfig{pods: true}},
		{name: "events", raw: "events", want: watchStreamsConfig{events: true}},
		{name: "pods,events", raw: "pods,events", want: watchStreamsConfig{pods: true, events: true}},
		{name: "all", raw: "all", want: watchStreamsConfig{pods: true, events: true}},
		{name: "with spaces", raw: " pods , events ", want: watchStreamsConfig{pods: true, events: true}},
		{name: "unknown only", raw: "deployments", want: watchStreamsConfig{}},
		{name: "unknown plus pods", raw: "deployments,pods", want: watchStreamsConfig{pods: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWatchStreamsEnv(tt.raw)
			if got != tt.want {
				t.Errorf("parseWatchStreamsEnv(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestStreamTracker_RegisterSnapshotDeregister(t *testing.T) {
	tr := newStreamTracker()

	if got := tr.snapshot(); len(got) != 0 {
		t.Fatalf("empty tracker snapshot len = %d, want 0", len(got))
	}

	_, dereg1 := tr.register(streamEntry{Actor: "a@x", Cluster: "c1", Kind: "pods", OpenedAt: time.Now()})
	id2, dereg2 := tr.register(streamEntry{Actor: "b@x", Cluster: "c1", Kind: "pods", OpenedAt: time.Now()})

	got := tr.snapshot()
	if len(got) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(got))
	}
	if got[0].ID >= got[1].ID {
		t.Errorf("snapshot not sorted by id: %d, %d", got[0].ID, got[1].ID)
	}

	dereg2()
	got = tr.snapshot()
	if len(got) != 1 {
		t.Fatalf("after deregister len = %d, want 1", len(got))
	}
	if got[0].ID == id2 {
		t.Errorf("deregistered id %d still present", id2)
	}

	dereg1()
	if got := tr.snapshot(); len(got) != 0 {
		t.Errorf("after both deregistered len = %d, want 0", len(got))
	}
}

func TestUserStreamLimiter_BasicAcquireRelease(t *testing.T) {
	l := newUserStreamLimiter(2)

	if !l.acquire("alice") {
		t.Fatal("first acquire should succeed")
	}
	if !l.acquire("alice") {
		t.Fatal("second acquire should succeed (cap=2)")
	}
	if l.acquire("alice") {
		t.Fatal("third acquire should fail (over cap)")
	}
	// Different actor not affected.
	if !l.acquire("bob") {
		t.Fatal("bob's first acquire should succeed regardless of alice's count")
	}

	l.release("alice")
	if !l.acquire("alice") {
		t.Fatal("acquire after release should succeed")
	}
}

func TestUserStreamLimiter_ZeroDisablesCap(t *testing.T) {
	l := newUserStreamLimiter(0)
	for i := 0; i < 1000; i++ {
		if !l.acquire("alice") {
			t.Fatalf("acquire #%d should succeed when cap is 0", i)
		}
	}
}

func TestUserStreamLimiter_ReleaseGCsEntry(t *testing.T) {
	l := newUserStreamLimiter(2)
	l.acquire("alice")
	l.release("alice")
	l.mu.Lock()
	_, present := l.counts["alice"]
	l.mu.Unlock()
	if present {
		t.Error("counts['alice'] should be deleted after release brings count to 0")
	}
}

func TestParseIntEnv(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback int
		want     int
	}{
		{name: "unset returns fallback", value: "", fallback: 30, want: 30},
		{name: "valid integer", value: "100", fallback: 30, want: 100},
		{name: "zero is valid", value: "0", fallback: 30, want: 0},
		{name: "negative falls back", value: "-1", fallback: 30, want: 30},
		{name: "garbage falls back", value: "abc", fallback: 30, want: 30},
		{name: "whitespace falls back", value: "   ", fallback: 30, want: 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PERISCOPE_TEST_INT_ENV", tt.value)
			got := parseIntEnv("PERISCOPE_TEST_INT_ENV", tt.fallback)
			if got != tt.want {
				t.Errorf("parseIntEnv(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestStreamTracker_ConcurrentSafe(t *testing.T) {
	// Run with -race. 50 goroutines register + deregister; another 50
	// snapshot concurrently. No assertions beyond "no race detected".
	tr := newStreamTracker()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, dereg := tr.register(streamEntry{Actor: "x", Kind: "pods", OpenedAt: time.Now()})
			dereg()
		}()
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = tr.snapshot()
		}()
	}
	wg.Wait()
}
