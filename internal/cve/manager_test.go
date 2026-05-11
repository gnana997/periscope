package cve

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/awsec2"
	"github.com/gnana997/periscope/internal/clusters"
)

// --- stub clients shared across tests ---

type stubInspector struct {
	enabled       bool
	enabledErr    error
	digestCalls   atomic.Int32
	instanceCalls atomic.Int32

	digestFindings   map[string][]Finding
	instanceFindings map[string][]Finding
}

func (s *stubInspector) IsEnabled(_ context.Context) (bool, error) {
	if s.enabledErr != nil {
		return false, s.enabledErr
	}
	return s.enabled, nil
}

func (s *stubInspector) ListFindingsByInstance(_ context.Context, ids []string) ([]Finding, error) {
	s.instanceCalls.Add(1)
	var out []Finding
	for _, id := range ids {
		out = append(out, s.instanceFindings[id]...)
	}
	return out, nil
}

func (s *stubInspector) ListFindingsByImageDigest(_ context.Context, digests []string) ([]Finding, error) {
	s.digestCalls.Add(1)
	var out []Finding
	for _, d := range digests {
		out = append(out, s.digestFindings[d]...)
	}
	return out, nil
}

type stubEC2 struct {
	instances []awsec2.InstanceMeta
}

func (s *stubEC2) DescribeInstances(_ context.Context, ids []string) ([]awsec2.InstanceMeta, error) {
	if s.instances != nil {
		return s.instances, nil
	}
	out := make([]awsec2.InstanceMeta, 0, len(ids))
	for _, id := range ids {
		out = append(out, awsec2.InstanceMeta{InstanceID: id, Tags: map[string]string{}})
	}
	return out, nil
}

// --- tests ---

func TestManager_EnsureHydrated_DisabledFastPath(t *testing.T) {
	// Inspector probe returns enabled=false → store.disabled=true,
	// no further calls made, EnsureHydrated returns nil error.
	insp := &stubInspector{enabled: false}
	mgr := NewManager(insp, &stubEC2{}, clientFactory(fake.NewSimpleClientset()), clockwork.NewFakeClock(), Config{}, nil)
	defer mgr.Stop()

	cluster := clusters.Cluster{Name: "c1"}
	st, err := mgr.EnsureHydrated(context.Background(), cluster)
	if err != nil {
		t.Fatalf("EnsureHydrated: %v", err)
	}
	if !st.Disabled() {
		t.Fatal("store should be marked Disabled")
	}
	if insp.digestCalls.Load() != 0 || insp.instanceCalls.Load() != 0 {
		t.Errorf("no Inspector list calls expected: digest=%d instance=%d",
			insp.digestCalls.Load(), insp.instanceCalls.Load())
	}
}

func TestManager_EnsureHydrated_PopulatesStore(t *testing.T) {
	// Set up: 1 node mapped to EC2 instance i-1, 1 pod with one
	// digest sha256:abc. Inspector returns one finding for each.
	cs := fake.NewSimpleClientset(
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Spec:       corev1.NodeSpec{ProviderID: "aws:///us-east-1a/i-1"},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p1", Namespace: "default"},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "app", ImageID: "docker-pullable://r/app@sha256:abc"},
				},
			},
		},
	)
	insp := &stubInspector{
		enabled:          true,
		digestFindings:   map[string][]Finding{"sha256:abc": {{CVE: "CVE-2026-1"}}},
		instanceFindings: map[string][]Finding{"i-1": {{CVE: "CVE-2026-2"}}},
	}
	ec2 := &stubEC2{
		instances: []awsec2.InstanceMeta{
			{InstanceID: "i-1", Tags: map[string]string{"eks:nodegroup-name": "ng-a"}},
		},
	}

	mgr := NewManager(insp, ec2, clientFactory(cs), clockwork.NewFakeClock(), Config{}, nil)
	defer mgr.Stop()

	st, err := mgr.EnsureHydrated(context.Background(), clusters.Cluster{Name: "c1"})
	if err != nil {
		t.Fatalf("EnsureHydrated: %v", err)
	}
	if st.Disabled() {
		t.Fatal("store should not be Disabled")
	}
	if d := st.GetDigest("sha256:abc"); d == nil || d.PodRefs != 1 || len(d.Findings) == 0 {
		t.Errorf("digest entry: %+v", d)
	}
	if i := st.GetInstance("i-1"); i == nil || i.OwnerKind != OwnerManagedNodegroup || i.OwnerName != "ng-a" {
		t.Errorf("instance entry: %+v", i)
	}
}

func TestManager_TTL_RefreshesStale(t *testing.T) {
	// Use a fake clock so we can fast-forward past 6h without
	// sleeping. After advancing, the TTL scan should re-fetch the
	// stale digest.
	cs := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "c", ImageID: "docker-pullable://r@sha256:ttl"},
				},
			},
		},
	)
	clock := clockwork.NewFakeClock()
	insp := &stubInspector{enabled: true, digestFindings: map[string][]Finding{"sha256:ttl": {{CVE: "x"}}}}
	mgr := NewManager(insp, &stubEC2{}, clientFactory(cs), clock, Config{
		RefreshInterval:      1 * time.Hour,
		EvictAfter:           10 * time.Hour,
		TTLScanInterval:      1 * time.Minute,
		EvictionScanInterval: 5 * time.Minute,
	}, nil)
	defer mgr.Stop()
	mgr.Start(context.Background())

	if _, err := mgr.EnsureHydrated(context.Background(), clusters.Cluster{Name: "c1"}); err != nil {
		t.Fatalf("EnsureHydrated: %v", err)
	}
	baselineDigestCalls := insp.digestCalls.Load()

	// runLoops registers two waiters on the fake clock (the TTL +
	// eviction tickers). Block until both are in place before
	// advancing — otherwise Advance can race the goroutine and
	// silently drop the tick.
	clock.BlockUntil(2)
	clock.Advance(2 * time.Hour) // past TTL boundary, fires both ticks
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if insp.digestCalls.Load() > baselineDigestCalls {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := insp.digestCalls.Load(); got <= baselineDigestCalls {
		t.Errorf("TTL refresh did not fire: digest calls %d → %d", baselineDigestCalls, got)
	}
}

func TestManager_Eviction_DropsZeroRefStale(t *testing.T) {
	// Hydrate a store with one digest, decrement its ref to 0,
	// fast-forward past EvictAfter, drive the eviction tick, assert
	// the digest is gone.
	cs := fake.NewSimpleClientset()
	clock := clockwork.NewFakeClock()
	mgr := NewManager(&stubInspector{enabled: true}, &stubEC2{}, clientFactory(cs), clock, Config{
		RefreshInterval:      10 * time.Hour,
		EvictAfter:           1 * time.Hour,
		TTLScanInterval:      10 * time.Minute,
		EvictionScanInterval: 1 * time.Minute,
	}, nil)
	defer mgr.Stop()
	mgr.Start(context.Background())

	cluster := clusters.Cluster{Name: "c1"}
	st, _ := mgr.EnsureHydrated(context.Background(), cluster)
	st.UpsertDigest("evict-me", nil, clock.Now())
	st.IncDigestRef("evict-me")
	st.DecDigestRef("evict-me")

	// Wait until both tickers (TTL + eviction) are registered with
	// the fake clock so Advance reliably fires them.
	clock.BlockUntil(2)
	clock.Advance(2 * time.Hour) // past EvictAfter

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st.GetDigest("evict-me") == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("evict-me digest was not evicted")
}

func TestManager_Refresh_BypassesTTL(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "c", ImageID: "docker-pullable://r@sha256:rfsh"},
				},
			},
		},
	)
	insp := &stubInspector{enabled: true, digestFindings: map[string][]Finding{"sha256:rfsh": {{CVE: "z"}}}}
	mgr := NewManager(insp, &stubEC2{}, clientFactory(cs), clockwork.NewFakeClock(), Config{}, nil)
	defer mgr.Stop()

	cluster := clusters.Cluster{Name: "c1"}
	if _, err := mgr.EnsureHydrated(context.Background(), cluster); err != nil {
		t.Fatalf("EnsureHydrated: %v", err)
	}
	baseline := insp.digestCalls.Load()

	if err := mgr.Refresh(context.Background(), cluster, []string{"sha256:rfsh"}, nil); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := insp.digestCalls.Load(); got <= baseline {
		t.Errorf("Refresh did not call Inspector: %d → %d", baseline, got)
	}
}

// clientFactory builds a ClientFactory that always returns the given
// fake clientset. Production code would build a real client per
// cluster via internal/k8s.NewClientset.
func clientFactory(cs kubernetes.Interface) ClientFactory {
	return func(_ context.Context, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
}
