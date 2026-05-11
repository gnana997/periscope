package cve

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
)

// TestPodHook_OnUpdate_FiresOnImageIDChange covers the headline
// requirement of the issue: a pod whose spec.image string didn't
// change but whose resolved imageID digest churned (think rolling
// :latest pull) MUST trigger a refresh, because Inspector keys on
// digest.
func TestPodHook_OnUpdate_FiresOnImageIDChange(t *testing.T) {
	m, hook, store := newHookFixture(t)
	defer m.Stop()

	oldPod := podWithImageAndID("app", "myrepo/app:latest", "docker-pullable://myrepo/app@sha256:aaaa")
	newPod := podWithImageAndID("app", "myrepo/app:latest", "docker-pullable://myrepo/app@sha256:bbbb")

	hook.onUpdate(oldPod, newPod)
	// Wait for the async digest enqueue to land.
	waitForDigestRef(t, store, "sha256:bbbb", 1)

	if got := store.GetDigest("sha256:bbbb"); got == nil {
		t.Errorf("new digest sha256:bbbb should be tracked")
	}
}

// TestPodHook_OnUpdate_NoFireOnImageOnly covers the other side: tag
// changed (the operator pushed a new tag), but the digest underneath
// didn't change. We should NOT fire — there's nothing new for
// Inspector to scan.
func TestPodHook_OnUpdate_NoFireOnImageOnly(t *testing.T) {
	m, hook, store := newHookFixture(t)
	defer m.Stop()

	oldPod := podWithImageAndID("app", "myrepo/app:v1", "docker-pullable://myrepo/app@sha256:cccc")
	newPod := podWithImageAndID("app", "myrepo/app:v2", "docker-pullable://myrepo/app@sha256:cccc")

	beforeCalls := m.inspector.(*stubInspector).digestCalls.Load()
	hook.onUpdate(oldPod, newPod)
	// Tiny wait for async path — must not have fired.
	flushAsync(t, m)
	afterCalls := m.inspector.(*stubInspector).digestCalls.Load()

	if afterCalls != beforeCalls {
		t.Errorf("unexpected Inspector call: before=%d after=%d", beforeCalls, afterCalls)
	}
	if got := store.GetDigest("sha256:cccc"); got != nil && got.PodRefs != 0 {
		t.Errorf("PodRefs should be unchanged, got %d", got.PodRefs)
	}
}

func TestPodHook_OnAddIncrementsRef(t *testing.T) {
	m, hook, store := newHookFixture(t)
	defer m.Stop()

	hook.onAdd(podWithImageAndID("app", "x:1", "docker-pullable://x@sha256:dddd"))
	waitForDigestRef(t, store, "sha256:dddd", 1)
}

func TestPodHook_OnDeleteDecrementsRef(t *testing.T) {
	m, hook, store := newHookFixture(t)
	defer m.Stop()

	store.IncDigestRef("sha256:eeee")
	store.IncDigestRef("sha256:eeee")
	hook.onDelete(podWithImageAndID("app", "x:1", "docker-pullable://x@sha256:eeee"))
	if got := store.GetDigest("sha256:eeee"); got == nil || got.PodRefs != 1 {
		t.Errorf("PodRefs after onDelete: want 1, got %+v", got)
	}
}

// --- helpers ---

func podWithImageAndID(name, image, imageID string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "n"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: name, Image: image}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: name, Image: image, ImageID: imageID},
			},
		},
	}
}

func newHookFixture(t *testing.T) (*Manager, *podHook, *Store) {
	t.Helper()
	insp := &stubInspector{}
	mgr := NewManager(insp, &stubEC2{}, nil, nil, Config{
		RefreshInterval:      time.Hour,
		EvictAfter:           time.Hour,
		HydrateBatchSize:     50,
		TTLScanInterval:      time.Hour,
		EvictionScanInterval: time.Hour,
	}, nil)
	store := NewStore()
	store.MarkHydrated(time.Now())
	st := &clusterState{store: store}
	hook := newPodHook(context.Background(), mgr, dummyCluster(), st)
	// Watch-hook unit tests exercise the steady-state delta path:
	// the initial informer sync has completed, so Add/Update events
	// represent real pod churn (not cache replay) and should bump
	// refs.
	hook.syncDone.Store(true)
	return mgr, hook, store
}

func waitForDigestRef(t *testing.T, store *Store, digest string, want int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if e := store.GetDigest(digest); e != nil && e.PodRefs >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("digest %q never reached ref %d", digest, want)
}

// flushAsync waits long enough that any pending enqueueDigest goroutine
// would have fired. Cheap upper bound; the hook fires the goroutine
// immediately and our stubs have no blocking I/O.
func flushAsync(t *testing.T, m *Manager) {
	t.Helper()
	// Wait for the singleflight Group to settle by issuing a
	// throwaway Do under a unique key — Do is a sync barrier per
	// key, but we want a sync barrier across keys. Sleep is the
	// honest answer here.
	time.Sleep(20 * time.Millisecond)
}

func dummyCluster() clusters.Cluster { return clusters.Cluster{Name: "test"} }
