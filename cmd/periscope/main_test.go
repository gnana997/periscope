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
		{name: "all", raw: "all", want: watchStreamsConfig{pods: true}},
		{name: "with spaces", raw: " pods , events ", want: watchStreamsConfig{pods: true}},
		{name: "unknown only", raw: "events", want: watchStreamsConfig{}},
		{name: "unknown plus pods", raw: "events,pods", want: watchStreamsConfig{pods: true}},
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
