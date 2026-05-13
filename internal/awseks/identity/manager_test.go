package identity

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
)

// stubPodIdentity returns a canned list and tracks call count.
type stubPodIdentity struct {
	list  []PodIdentityAssoc
	err   error
	calls int32
}

func (s *stubPodIdentity) ListPodIdentityAssociations(ctx context.Context, clusterName string) ([]PodIdentityAssoc, error) {
	atomic.AddInt32(&s.calls, 1)
	return s.list, s.err
}

// stubResolver answers role-existence questions with a map and
// counts probes per ARN.
type stubResolver struct {
	exists map[string]bool
	err    error
	calls  map[string]int
}

func (s *stubResolver) RoleExists(ctx context.Context, roleArn string) (bool, error) {
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	s.calls[roleArn]++
	if s.err != nil {
		return false, s.err
	}
	return s.exists[roleArn], nil
}

func newTestManager(t *testing.T, pi *stubPodIdentity, rr *stubResolver, irsa map[SAKey]string) (*Manager, *clockwork.FakeClock) {
	t.Helper()
	clock := clockwork.NewFakeClock()
	mgr := NewManager("c1", pi, rr, clock, Config{}, nil)
	mgr.SetIRSALister(func() (map[SAKey]string, error) {
		return irsa, nil
	})
	return mgr, clock
}

func TestManager_EnsureFirstCallRebuilds(t *testing.T) {
	pi := &stubPodIdentity{list: []PodIdentityAssoc{
		{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/pi", Namespace: "ns", ServiceAccount: "sa"},
	}}
	rr := &stubResolver{exists: map[string]bool{
		"arn:aws:iam::123:role/pi": true,
	}}
	mgr, _ := newTestManager(t, pi, rr, nil)

	got, err := mgr.Ensure(context.Background())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}
	if pi.calls != 1 {
		t.Errorf("ListPodIdentity calls = %d, want 1", pi.calls)
	}
}

func TestManager_EnsureWithinTTLServesCached(t *testing.T) {
	pi := &stubPodIdentity{list: []PodIdentityAssoc{
		{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/r", Namespace: "ns", ServiceAccount: "sa"},
	}}
	rr := &stubResolver{exists: map[string]bool{"arn:aws:iam::123:role/r": true}}
	mgr, clock := newTestManager(t, pi, rr, nil)

	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	clock.Advance(time.Minute) // < 5 min TTL
	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if pi.calls != 1 {
		t.Errorf("ListPodIdentity calls = %d, want 1 (cached)", pi.calls)
	}
}

func TestManager_EnsureAfterTTLRebuilds(t *testing.T) {
	pi := &stubPodIdentity{list: []PodIdentityAssoc{
		{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/r", Namespace: "ns", ServiceAccount: "sa"},
	}}
	rr := &stubResolver{exists: map[string]bool{"arn:aws:iam::123:role/r": true}}
	mgr, clock := newTestManager(t, pi, rr, nil)

	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	clock.Advance(6 * time.Minute) // > 5 min TTL
	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if pi.calls != 2 {
		t.Errorf("ListPodIdentity calls = %d, want 2 (TTL expired)", pi.calls)
	}
}

func TestManager_InvalidateForcesRebuild(t *testing.T) {
	pi := &stubPodIdentity{list: []PodIdentityAssoc{
		{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/r", Namespace: "ns", ServiceAccount: "sa"},
	}}
	rr := &stubResolver{exists: map[string]bool{"arn:aws:iam::123:role/r": true}}
	mgr, _ := newTestManager(t, pi, rr, nil)

	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	mgr.Store().Invalidate("ns", "sa")
	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if pi.calls != 2 {
		t.Errorf("ListPodIdentity calls = %d, want 2 (after Invalidate)", pi.calls)
	}
}

func TestManager_RoleCacheAvoidsRepeatProbes(t *testing.T) {
	pi := &stubPodIdentity{list: []PodIdentityAssoc{
		{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/r", Namespace: "ns", ServiceAccount: "sa"},
	}}
	rr := &stubResolver{exists: map[string]bool{"arn:aws:iam::123:role/r": true}}
	mgr, clock := newTestManager(t, pi, rr, nil)

	// First rebuild probes the role.
	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	// Force a rebuild < RoleTTL away — the IAM probe should be cached.
	clock.Advance(6 * time.Minute)
	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got := rr.calls["arn:aws:iam::123:role/r"]; got != 1 {
		t.Errorf("RoleExists probes = %d, want 1 (cached across rebuilds)", got)
	}
}

func TestManager_RoleCacheTTLExpires(t *testing.T) {
	pi := &stubPodIdentity{list: []PodIdentityAssoc{
		{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/r", Namespace: "ns", ServiceAccount: "sa"},
	}}
	rr := &stubResolver{exists: map[string]bool{"arn:aws:iam::123:role/r": true}}
	mgr, clock := newTestManager(t, pi, rr, nil)

	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	clock.Advance(20 * time.Minute) // > 15-min RoleTTL
	if _, err := mgr.Ensure(context.Background()); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got := rr.calls["arn:aws:iam::123:role/r"]; got != 2 {
		t.Errorf("RoleExists probes = %d, want 2 (after RoleTTL)", got)
	}
}

func TestManager_IRSAListerNilReturnsTypedErr(t *testing.T) {
	pi := &stubPodIdentity{}
	rr := &stubResolver{}
	mgr := NewManager("c1", pi, rr, clockwork.NewFakeClock(), Config{}, nil)
	// Don't SetIRSALister — informer hasn't synced.
	_, err := mgr.Ensure(context.Background())
	if !errors.Is(err, ErrIRSAListerNotReady) {
		t.Errorf("err = %v, want ErrIRSAListerNotReady", err)
	}
}

func TestManager_PodIdentityFailurePropagates(t *testing.T) {
	pi := &stubPodIdentity{err: errors.New("aws throttled")}
	rr := &stubResolver{}
	mgr, _ := newTestManager(t, pi, rr, nil)

	_, err := mgr.Ensure(context.Background())
	if err == nil {
		t.Fatalf("want error from podIdentity")
	}
}

func TestManager_RoleExistsSoftFailure(t *testing.T) {
	// If the IAM probe errors, rebuild still succeeds but the role
	// is rendered as not-existing. Bindings remain visible.
	pi := &stubPodIdentity{list: []PodIdentityAssoc{
		{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/r", Namespace: "ns", ServiceAccount: "sa"},
	}}
	rr := &stubResolver{err: errors.New("AccessDenied")}
	mgr, _ := newTestManager(t, pi, rr, nil)

	got, err := mgr.Ensure(context.Background())
	if err != nil {
		t.Fatalf("unexpected err on soft-fail: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1 (bindings still visible)", len(got))
	}
	if got[0].Bindings[0].RoleExists {
		t.Errorf("RoleExists = true on IAM error, want false")
	}
}

func TestManager_UnifiesIRSAAndPodIdentity(t *testing.T) {
	pi := &stubPodIdentity{list: []PodIdentityAssoc{
		{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/pi", Namespace: "ns", ServiceAccount: "sa"},
	}}
	rr := &stubResolver{exists: map[string]bool{
		"arn:aws:iam::123:role/pi":   true,
		"arn:aws:iam::123:role/irsa": true,
	}}
	mgr, _ := newTestManager(t, pi, rr, map[SAKey]string{
		{Namespace: "ns", Name: "sa"}: "arn:aws:iam::123:role/irsa",
	})

	got, err := mgr.Ensure(context.Background())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}
	if !got[0].DualSource {
		t.Errorf("DualSource = false, want true")
	}
	if len(got[0].Bindings) != 2 {
		t.Errorf("bindings = %d, want 2", len(got[0].Bindings))
	}
}
