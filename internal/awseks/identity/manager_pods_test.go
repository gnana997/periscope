package identity

import (
	"context"
	"errors"
	"testing"
)

// fakePodLister returns canned per-SA pod lists. The two-level map
// mirrors the indexer-backed lister contract: per (namespace,
// saName) key, return PodRefs and the untruncated total count.
type fakePodLister struct {
	byKey map[string][]PodRef
	err   error
}

func (f *fakePodLister) lister() PodLister {
	return func(namespace, saName string) ([]PodRef, int, error) {
		if f.err != nil {
			return nil, 0, f.err
		}
		if saName == "" {
			saName = "default"
		}
		refs := f.byKey[namespace+"/"+saName]
		return append([]PodRef(nil), refs...), len(refs), nil
	}
}

func newPodTestManager(t *testing.T) *Manager {
	t.Helper()
	pi := &stubPodIdentity{}
	rr := &stubResolver{exists: map[string]bool{}}
	mgr, _ := newTestManager(t, pi, rr, nil)
	return mgr
}

func TestPodsForSA_ListerNotReady(t *testing.T) {
	mgr := newPodTestManager(t)
	if _, _, err := mgr.PodsForSA(context.Background(), "default", "sa", 5); !errors.Is(err, ErrPodListerNotReady) {
		t.Fatalf("PodsForSA before SetPodLister = %v, want ErrPodListerNotReady", err)
	}
}

func TestPodsForSA_ReturnsBucket(t *testing.T) {
	mgr := newPodTestManager(t)
	fp := &fakePodLister{byKey: map[string][]PodRef{
		"ns1/sa1": {
			{Namespace: "ns1", Name: "p-b", NodeName: "n1"},
			{Namespace: "ns1", Name: "p-a", NodeName: "n2"},
			{Namespace: "ns1", Name: "p-c"},
		},
	}}
	mgr.SetPodLister(fp.lister())

	refs, total, err := mgr.PodsForSA(context.Background(), "ns1", "sa1", 10)
	if err != nil {
		t.Fatalf("PodsForSA: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	// Sort key is (namespace, name).
	if got := []string{refs[0].Name, refs[1].Name, refs[2].Name}; got[0] != "p-a" || got[1] != "p-b" || got[2] != "p-c" {
		t.Errorf("PodsForSA names = %v, want sorted [p-a p-b p-c]", got)
	}
}

func TestPodsForSA_Truncates(t *testing.T) {
	mgr := newPodTestManager(t)
	fp := &fakePodLister{byKey: map[string][]PodRef{
		"default/sa1": {
			{Namespace: "default", Name: "p-1"},
			{Namespace: "default", Name: "p-2"},
			{Namespace: "default", Name: "p-3"},
			{Namespace: "default", Name: "p-4"},
			{Namespace: "default", Name: "p-5"},
		},
	}}
	mgr.SetPodLister(fp.lister())

	refs, total, err := mgr.PodsForSA(context.Background(), "default", "sa1", 2)
	if err != nil {
		t.Fatalf("PodsForSA: %v", err)
	}
	if len(refs) != 2 {
		t.Errorf("len(refs) = %d, want 2", len(refs))
	}
	if total != 5 {
		t.Errorf("total = %d, want 5 (untruncated)", total)
	}
}

func TestPodsForSA_EmptySANormalizesToDefault(t *testing.T) {
	mgr := newPodTestManager(t)
	fp := &fakePodLister{byKey: map[string][]PodRef{
		"ns/default": {{Namespace: "ns", Name: "p-1"}},
	}}
	mgr.SetPodLister(fp.lister())

	refs, total, err := mgr.PodsForSA(context.Background(), "ns", "", 5)
	if err != nil {
		t.Fatalf("PodsForSA: %v", err)
	}
	if total != 1 || len(refs) != 1 || refs[0].Name != "p-1" {
		t.Errorf("empty SA didn't normalize to 'default': total=%d refs=%+v", total, refs)
	}
}

func TestPodsForSA_NoMatches(t *testing.T) {
	mgr := newPodTestManager(t)
	fp := &fakePodLister{byKey: map[string][]PodRef{}}
	mgr.SetPodLister(fp.lister())

	refs, total, err := mgr.PodsForSA(context.Background(), "ns", "sa1", 5)
	if err != nil {
		t.Fatalf("PodsForSA: %v", err)
	}
	if total != 0 || len(refs) != 0 {
		t.Errorf("no-matches: total=%d len(refs)=%d, want both 0", total, len(refs))
	}
}

func TestPodsForSA_LimitZeroReturnsAll(t *testing.T) {
	mgr := newPodTestManager(t)
	fp := &fakePodLister{byKey: map[string][]PodRef{
		"ns/sa": {
			{Namespace: "ns", Name: "p-1"},
			{Namespace: "ns", Name: "p-2"},
		},
	}}
	mgr.SetPodLister(fp.lister())

	refs, total, err := mgr.PodsForSA(context.Background(), "ns", "sa", 0)
	if err != nil {
		t.Fatalf("PodsForSA: %v", err)
	}
	if len(refs) != 2 || total != 2 {
		t.Errorf("limit=0: got len=%d total=%d, want 2/2", len(refs), total)
	}
}
