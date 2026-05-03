package k8s

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	policyv1 "k8s.io/api/policy/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// testSink collects events on a channel; Send is non-blocking and
// returns false either when the deny channel is closed (simulates
// backpressure) or when the events channel is full.
type testSink struct {
	events chan WatchEvent
	deny   chan struct{}
	denied atomic.Bool
}

func newTestSink(buf int) *testSink {
	return &testSink{
		events: make(chan WatchEvent, buf),
		deny:   make(chan struct{}),
	}
}

func (s *testSink) Send(ev WatchEvent) bool {
	if s.denied.Load() {
		return false
	}
	select {
	case s.events <- ev:
		return true
	default:
		return false
	}
}

func (s *testSink) startDenying() {
	if s.denied.CompareAndSwap(false, true) {
		close(s.deny)
	}
}

// awaitEvent reads the next event from sink with a timeout.
func awaitEvent(t *testing.T, sink *testSink) WatchEvent {
	t.Helper()
	select {
	case ev := <-sink.events:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for watch event")
		return WatchEvent{}
	}
}

func swapNewClientFn(t *testing.T, cs kubernetes.Interface) {
	t.Helper()
	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientFn = orig })
}

func newWatchTestPod(name, ns, rv string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       ns,
			ResourceVersion: rv,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "acme/app:v1"}},
			NodeName:   "node-1",
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestWatchPods_InitialSnapshot(t *testing.T) {
	cs := fake.NewSimpleClientset(
		newWatchTestPod("a", "default", "1"),
		newWatchTestPod("b", "default", "2"),
	)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- WatchPods(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	ev := awaitEvent(t, sink)
	if ev.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", ev.Type)
	}
	items, ok := ev.Items.([]Pod)
	if !ok {
		t.Fatalf("snapshot items type = %T, want []Pod", ev.Items)
	}
	if len(items) != 2 {
		t.Errorf("snapshot len = %d, want 2", len(items))
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("WatchPods returned %v after cancel, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchPods did not return after ctx cancel")
	}
}

func TestWatchPods_AddedThenDeleted(t *testing.T) {
	cs := fake.NewSimpleClientset()
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchPods(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	// Drain the initial empty snapshot.
	if ev := awaitEvent(t, sink); ev.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", ev.Type)
	}

	// fake.NewSimpleClientset's tracker drives the watch.
	pod := newWatchTestPod("c", "default", "10")
	if _, err := cs.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	ev := awaitEvent(t, sink)
	if ev.Type != WatchAdded {
		t.Fatalf("event = %v, want added", ev.Type)
	}
	got, ok := ev.Object.(Pod)
	if !ok {
		t.Fatalf("Object type = %T, want Pod", ev.Object)
	}
	if got.Name != "c" {
		t.Errorf("added pod name = %q, want c", got.Name)
	}

	if err := cs.CoreV1().Pods("default").Delete(ctx, "c", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete pod: %v", err)
	}
	ev = awaitEvent(t, sink)
	if ev.Type != WatchDeleted {
		t.Fatalf("event = %v, want deleted", ev.Type)
	}
}

func TestWatchPods_ContextCancellation(t *testing.T) {
	cs := fake.NewSimpleClientset()
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- WatchPods(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	// Drain initial snapshot so the watch is established.
	awaitEvent(t, sink)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("WatchPods returned %v, want nil on ctx cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchPods did not return within 2s of ctx cancel")
	}
}

func TestWatchPods_BackpressureCloses(t *testing.T) {
	cs := fake.NewSimpleClientset(newWatchTestPod("a", "default", "1"))
	swapNewClientFn(t, cs)

	sink := newTestSink(0) // zero buffer + denied = always returns false
	sink.startDenying()

	done := make(chan error, 1)
	go func() {
		done <- WatchPods(context.Background(), stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("WatchPods returned %v, want nil on backpressure close", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchPods did not return within 2s of backpressure")
	}
}

// fakeWatcher wraps watch.RaceFreeFakeWatcher so a custom WatchReactor
// can drive arbitrary events into the watch loop, including watch.Error
// with a 410 status.
type fakeWatcher struct {
	*watch.RaceFreeFakeWatcher
}

func TestWatchPods_GoneTriggersRelist(t *testing.T) {
	cs := fake.NewSimpleClientset(newWatchTestPod("a", "default", "1"))
	fw := &fakeWatcher{RaceFreeFakeWatcher: watch.NewRaceFreeFake()}

	cs.PrependWatchReactor("pods", func(_ ktesting.Action) (bool, watch.Interface, error) {
		return true, fw, nil
	})
	swapNewClientFn(t, cs)

	sink := newTestSink(16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- WatchPods(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	// Drain the initial snapshot.
	if ev := awaitEvent(t, sink); ev.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", ev.Type)
	}

	// Push a 410 Gone status through the watcher.
	fw.Error(&metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure,
		Code:     410,
		Reason:   metav1.StatusReasonGone,
		Message:  "too old resource version",
	})

	relistEv := awaitEvent(t, sink)
	if relistEv.Type != WatchRelist {
		t.Fatalf("event = %v, want relist", relistEv.Type)
	}

	// After relist the loop calls List + Watch again. Our reactor returns
	// the same fakeWatcher, and the next List will return the same pod —
	// so we expect a fresh snapshot.
	snap2 := awaitEvent(t, sink)
	if snap2.Type != WatchSnapshot {
		t.Fatalf("event after relist = %v, want snapshot", snap2.Type)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("WatchPods returned %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchPods did not return after cancel")
	}
}

func TestWatchPods_NonGoneWatchErrorPropagates(t *testing.T) {
	cs := fake.NewSimpleClientset()
	fw := &fakeWatcher{RaceFreeFakeWatcher: watch.NewRaceFreeFake()}
	cs.PrependWatchReactor("pods", func(_ ktesting.Action) (bool, watch.Interface, error) {
		return true, fw, nil
	})
	swapNewClientFn(t, cs)

	sink := newTestSink(16)

	done := make(chan error, 1)
	go func() {
		done <- WatchPods(context.Background(), stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	// Drain initial snapshot.
	awaitEvent(t, sink)

	// 500 server error — must propagate, not relist.
	fw.Error(&metav1.Status{
		Status:  metav1.StatusFailure,
		Code:    500,
		Reason:  metav1.StatusReasonInternalError,
		Message: "boom",
	})

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("WatchPods returned nil on 500, want error")
		}
		if !strings.Contains(err.Error(), "boom") {
			t.Errorf("err = %v, want it to contain 'boom'", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchPods did not return within 2s of error")
	}
}

// --- WatchEvents tests ---

func newTestK8sEvent(name, ns, rv string, last time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       ns,
			ResourceVersion: rv,
		},
		InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "p"},
		Type:           "Warning",
		Reason:         "FailedScheduling",
		Message:        "no nodes available",
		Count:          3,
		FirstTimestamp: metav1.NewTime(last.Add(-time.Minute)),
		LastTimestamp:  metav1.NewTime(last),
		Source:         corev1.EventSource{Component: "scheduler"},
	}
}

func TestWatchEvents_InitialSnapshotSortedNewestFirst(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	older := newTestK8sEvent("old", "default", "1", now.Add(-1*time.Hour))
	newer := newTestK8sEvent("new", "default", "2", now)

	cs := fake.NewSimpleClientset(older, newer)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchEvents(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	ev := awaitEvent(t, sink)
	if ev.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", ev.Type)
	}
	items, ok := ev.Items.([]ClusterEvent)
	if !ok {
		t.Fatalf("Items type = %T, want []ClusterEvent", ev.Items)
	}
	if len(items) != 2 {
		t.Fatalf("snapshot len = %d, want 2", len(items))
	}
	// Newer first.
	if !items[0].Last.After(items[1].Last) {
		t.Errorf("snapshot not sorted newest-first: %v then %v", items[0].Last, items[1].Last)
	}
}

func TestWatchEvents_AddedDelivered(t *testing.T) {
	cs := fake.NewSimpleClientset()
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchEvents(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	awaitEvent(t, sink) // empty snapshot

	ev := newTestK8sEvent("kicked", "default", "10", time.Now())
	if _, err := cs.CoreV1().Events("default").Create(ctx, ev, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event type = %v, want added", got.Type)
	}
	ce, ok := got.Object.(ClusterEvent)
	if !ok {
		t.Fatalf("Object type = %T, want ClusterEvent", got.Object)
	}
	if ce.Reason != "FailedScheduling" {
		t.Errorf("ClusterEvent.Reason = %q, want FailedScheduling", ce.Reason)
	}
}

func TestWatchEvents_ContextCancellation(t *testing.T) {
	cs := fake.NewSimpleClientset()
	swapNewClientFn(t, cs)
	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- WatchEvents(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	awaitEvent(t, sink)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("WatchEvents returned %v after cancel, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WatchEvents did not return after cancel")
	}
}

func TestWatchEvents_GoneTriggersRelist(t *testing.T) {
	cs := fake.NewSimpleClientset()
	fw := &fakeWatcher{RaceFreeFakeWatcher: watch.NewRaceFreeFake()}
	cs.PrependWatchReactor("events", func(_ ktesting.Action) (bool, watch.Interface, error) {
		return true, fw, nil
	})
	swapNewClientFn(t, cs)

	sink := newTestSink(16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchEvents(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	awaitEvent(t, sink) // initial snapshot

	fw.Error(&metav1.Status{Status: metav1.StatusFailure, Code: 410, Reason: metav1.StatusReasonGone, Message: "rv expired"})

	if got := awaitEvent(t, sink); got.Type != WatchRelist {
		t.Fatalf("event = %v, want relist", got.Type)
	}
	if got := awaitEvent(t, sink); got.Type != WatchSnapshot {
		t.Fatalf("event = %v, want snapshot after relist", got.Type)
	}
}

// --- Last-Event-ID resume tests ---

func TestWatchPods_ResumeSkipsListAndSnapshot(t *testing.T) {
	// Fake clientset with one pod. With ResumeFrom set, watchKind must
	// open Watch directly (no List call, no Snapshot event) and just
	// stream subsequent deltas. We assert this by pushing an ADDED
	// through the fake watcher and confirming it's the first event the
	// sink sees.
	cs := fake.NewSimpleClientset(newWatchTestPod("pre-existing", "default", "5"))

	// Track whether List was called — resume path must not List.
	var listCalled atomic.Bool
	cs.PrependReactor("list", "pods", func(_ ktesting.Action) (bool, runtime.Object, error) {
		listCalled.Store(true)
		return false, nil, nil // chain through to the default tracker
	})

	// Sync point: signal once Watch has been opened, so the test can
	// fire its Create after the watcher is established (otherwise the
	// Create races the goroutine and the event may not be observed).
	watchOpened := make(chan struct{}, 1)
	cs.PrependWatchReactor("pods", func(_ ktesting.Action) (bool, watch.Interface, error) {
		select {
		case watchOpened <- struct{}{}:
		default:
		}
		return false, nil, nil // chain through to the default tracker
	})
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- WatchPods(ctx, stubProvider{}, WatchArgs{
			Cluster:    clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace:  "default",
			ResumeFrom: "5",
		}, sink)
	}()

	select {
	case <-watchOpened:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch was never called; resume path may be broken")
	}

	// Push an ADDED via the tracker.
	pod := newWatchTestPod("after-resume", "default", "10")
	if _, err := cs.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pod: %v", err)
	}

	first := awaitEvent(t, sink)
	if first.Type != WatchAdded {
		t.Fatalf("first event = %v, want added (resume should not emit snapshot)", first.Type)
	}
	if listCalled.Load() {
		t.Error("List was called during resume; expected Watch only")
	}

	cancel()
	<-done
}

func TestWatchPods_ResumeStaleRVFallsBackToFreshList(t *testing.T) {
	cs := fake.NewSimpleClientset(newWatchTestPod("a", "default", "1"))

	// Fake the Watch opener: first call (the resume attempt) returns
	// 410 Gone; subsequent call (the fallback) returns the real watcher.
	var watchCalls atomic.Int32
	cs.PrependWatchReactor("pods", func(action ktesting.Action) (bool, watch.Interface, error) {
		n := watchCalls.Add(1)
		if n == 1 {
			return true, nil, apierrors.NewResourceExpired("too old resource version")
		}
		// Subsequent calls fall through to the default tracker.
		return false, nil, nil
	})
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- WatchPods(ctx, stubProvider{}, WatchArgs{
			Cluster:    clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace:  "default",
			ResumeFrom: "stale-rv",
		}, sink)
	}()

	// On 410 Gone, watchKind should fall through to fresh List+Watch.
	// Expect: snapshot event with the pre-existing pod.
	ev := awaitEvent(t, sink)
	if ev.Type != WatchSnapshot {
		t.Fatalf("event = %v, want snapshot after 410 fallback", ev.Type)
	}
	items, ok := ev.Items.([]Pod)
	if !ok || len(items) != 1 || items[0].Name != "a" {
		t.Errorf("snapshot items = %+v, want one pod 'a'", ev.Items)
	}
	if got := watchCalls.Load(); got != 2 {
		t.Errorf("Watch was called %d times, want 2 (resume + fallback)", got)
	}

	cancel()
	<-done
}

// --- WatchReplicaSets / WatchJobs smoke tests ---
//
// The generic watchKind is exercised thoroughly by the WatchPods and
// WatchEvents suites above. These two tests verify only that the
// kind-specific spec wiring (clientset method chain + summary
// function) is hooked up correctly for the new primitives. A single
// snapshot + ADDED assertion per kind is enough.

func TestWatchReplicaSets_SnapshotAndAdded(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs-a", Namespace: "default", ResourceVersion: "1"},
		Spec:       appsv1.ReplicaSetSpec{Replicas: ptr32(1)},
	}
	cs := fake.NewSimpleClientset(rs)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchReplicaSets(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]ReplicaSet)
	if !ok {
		t.Fatalf("Items type = %T, want []ReplicaSet", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "rs-a" {
		t.Fatalf("snapshot items = %+v, want one rs-a", items)
	}

	rs2 := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "rs-b", Namespace: "default", ResourceVersion: "2"},
		Spec:       appsv1.ReplicaSetSpec{Replicas: ptr32(1)},
	}
	if _, err := cs.AppsV1().ReplicaSets("default").Create(ctx, rs2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create rs: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	rsObj, ok := got.Object.(ReplicaSet)
	if !ok {
		t.Fatalf("Object type = %T, want ReplicaSet", got.Object)
	}
	if rsObj.Name != "rs-b" {
		t.Errorf("added rs name = %q, want rs-b", rsObj.Name)
	}
}

func TestWatchJobs_SnapshotAndAdded(t *testing.T) {
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "job-a", Namespace: "default", ResourceVersion: "1"},
	}
	cs := fake.NewSimpleClientset(job)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchJobs(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]Job)
	if !ok {
		t.Fatalf("Items type = %T, want []Job", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "job-a" {
		t.Fatalf("snapshot items = %+v, want one job-a", items)
	}

	job2 := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "job-b", Namespace: "default", ResourceVersion: "2"},
	}
	if _, err := cs.BatchV1().Jobs("default").Create(ctx, job2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create job: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	jobObj, ok := got.Object.(Job)
	if !ok {
		t.Fatalf("Object type = %T, want Job", got.Object)
	}
	if jobObj.Name != "job-b" {
		t.Errorf("added job name = %q, want job-b", jobObj.Name)
	}
}

func ptr32(v int32) *int32 { return &v }

// --- Tier-A workload-controller smoke tests ---
//
// Same shape as TestWatchReplicaSets / TestWatchJobs above: one
// snapshot + one ADDED assertion per kind, just enough to confirm the
// kind-specific spec wiring (clientset method chain + summary
// projection) is correct. The generic watchKind primitive is
// exercised by the WatchPods / WatchEvents suites and need not be
// duplicated here.

func TestWatchDeployments_SnapshotAndAdded(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep-a", Namespace: "default", ResourceVersion: "1"},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr32(2)},
	}
	cs := fake.NewSimpleClientset(dep)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchDeployments(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]Deployment)
	if !ok {
		t.Fatalf("Items type = %T, want []Deployment", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "dep-a" {
		t.Fatalf("snapshot items = %+v, want one dep-a", items)
	}

	dep2 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "dep-b", Namespace: "default", ResourceVersion: "2"},
	}
	if _, err := cs.AppsV1().Deployments("default").Create(ctx, dep2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create deployment: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	depObj, ok := got.Object.(Deployment)
	if !ok {
		t.Fatalf("Object type = %T, want Deployment", got.Object)
	}
	if depObj.Name != "dep-b" {
		t.Errorf("added deployment name = %q, want dep-b", depObj.Name)
	}
}

func TestWatchStatefulSets_SnapshotAndAdded(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts-a", Namespace: "default", ResourceVersion: "1"},
		Spec:       appsv1.StatefulSetSpec{Replicas: ptr32(3)},
	}
	cs := fake.NewSimpleClientset(sts)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchStatefulSets(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]StatefulSet)
	if !ok {
		t.Fatalf("Items type = %T, want []StatefulSet", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "sts-a" {
		t.Fatalf("snapshot items = %+v, want one sts-a", items)
	}

	sts2 := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sts-b", Namespace: "default", ResourceVersion: "2"},
	}
	if _, err := cs.AppsV1().StatefulSets("default").Create(ctx, sts2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create statefulset: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	stsObj, ok := got.Object.(StatefulSet)
	if !ok {
		t.Fatalf("Object type = %T, want StatefulSet", got.Object)
	}
	if stsObj.Name != "sts-b" {
		t.Errorf("added statefulset name = %q, want sts-b", stsObj.Name)
	}
}

func TestWatchDaemonSets_SnapshotAndAdded(t *testing.T) {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ds-a", Namespace: "default", ResourceVersion: "1"},
	}
	cs := fake.NewSimpleClientset(ds)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchDaemonSets(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]DaemonSet)
	if !ok {
		t.Fatalf("Items type = %T, want []DaemonSet", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "ds-a" {
		t.Fatalf("snapshot items = %+v, want one ds-a", items)
	}

	ds2 := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "ds-b", Namespace: "default", ResourceVersion: "2"},
	}
	if _, err := cs.AppsV1().DaemonSets("default").Create(ctx, ds2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create daemonset: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	dsObj, ok := got.Object.(DaemonSet)
	if !ok {
		t.Fatalf("Object type = %T, want DaemonSet", got.Object)
	}
	if dsObj.Name != "ds-b" {
		t.Errorf("added daemonset name = %q, want ds-b", dsObj.Name)
	}
}

func TestWatchCronJobs_SnapshotAndAdded(t *testing.T) {
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "cj-a", Namespace: "default", ResourceVersion: "1"},
		Spec:       batchv1.CronJobSpec{Schedule: "*/5 * * * *"},
	}
	cs := fake.NewSimpleClientset(cj)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchCronJobs(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]CronJob)
	if !ok {
		t.Fatalf("Items type = %T, want []CronJob", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "cj-a" {
		t.Fatalf("snapshot items = %+v, want one cj-a", items)
	}

	cj2 := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "cj-b", Namespace: "default", ResourceVersion: "2"},
		Spec:       batchv1.CronJobSpec{Schedule: "0 * * * *"},
	}
	if _, err := cs.BatchV1().CronJobs("default").Create(ctx, cj2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create cronjob: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	cjObj, ok := got.Object.(CronJob)
	if !ok {
		t.Fatalf("Object type = %T, want CronJob", got.Object)
	}
	if cjObj.Name != "cj-b" {
		t.Errorf("added cronjob name = %q, want cj-b", cjObj.Name)
	}
}

func TestWatchHorizontalPodAutoscalers_SnapshotAndAdded(t *testing.T) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "hpa-a", Namespace: "default", ResourceVersion: "1"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr32(1),
			MaxReplicas: 10,
		},
	}
	cs := fake.NewSimpleClientset(hpa)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchHorizontalPodAutoscalers(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]HPA)
	if !ok {
		t.Fatalf("Items type = %T, want []HPA", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "hpa-a" {
		t.Fatalf("snapshot items = %+v, want one hpa-a", items)
	}

	hpa2 := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "hpa-b", Namespace: "default", ResourceVersion: "2"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MinReplicas: ptr32(1),
			MaxReplicas: 5,
		},
	}
	if _, err := cs.AutoscalingV2().HorizontalPodAutoscalers("default").Create(ctx, hpa2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create hpa: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	hpaObj, ok := got.Object.(HPA)
	if !ok {
		t.Fatalf("Object type = %T, want HPA", got.Object)
	}
	if hpaObj.Name != "hpa-b" {
		t.Errorf("added hpa name = %q, want hpa-b", hpaObj.Name)
	}
}

func TestWatchPodDisruptionBudgets_SnapshotAndAdded(t *testing.T) {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "pdb-a", Namespace: "default", ResourceVersion: "1"},
	}
	cs := fake.NewSimpleClientset(pdb)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchPodDisruptionBudgets(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]PDB)
	if !ok {
		t.Fatalf("Items type = %T, want []PDB", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "pdb-a" {
		t.Fatalf("snapshot items = %+v, want one pdb-a", items)
	}

	pdb2 := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "pdb-b", Namespace: "default", ResourceVersion: "2"},
	}
	if _, err := cs.PolicyV1().PodDisruptionBudgets("default").Create(ctx, pdb2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pdb: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	pdbObj, ok := got.Object.(PDB)
	if !ok {
		t.Fatalf("Object type = %T, want PDB", got.Object)
	}
	if pdbObj.Name != "pdb-b" {
		t.Errorf("added pdb name = %q, want pdb-b", pdbObj.Name)
	}
}

// --- Tier-B networking smoke tests ---

func TestWatchServices_SnapshotAndAdded(t *testing.T) {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-a", Namespace: "default", ResourceVersion: "1"},
		Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.1"},
	}
	cs := fake.NewSimpleClientset(svc)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchServices(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]Service)
	if !ok {
		t.Fatalf("Items type = %T, want []Service", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "svc-a" {
		t.Fatalf("snapshot items = %+v, want one svc-a", items)
	}

	svc2 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc-b", Namespace: "default", ResourceVersion: "2"},
	}
	if _, err := cs.CoreV1().Services("default").Create(ctx, svc2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create service: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	svcObj, ok := got.Object.(Service)
	if !ok {
		t.Fatalf("Object type = %T, want Service", got.Object)
	}
	if svcObj.Name != "svc-b" {
		t.Errorf("added service name = %q, want svc-b", svcObj.Name)
	}
}

func TestWatchIngresses_SnapshotAndAdded(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ing-a", Namespace: "default", ResourceVersion: "1"},
	}
	cs := fake.NewSimpleClientset(ing)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchIngresses(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]Ingress)
	if !ok {
		t.Fatalf("Items type = %T, want []Ingress", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "ing-a" {
		t.Fatalf("snapshot items = %+v, want one ing-a", items)
	}

	ing2 := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ing-b", Namespace: "default", ResourceVersion: "2"},
	}
	if _, err := cs.NetworkingV1().Ingresses("default").Create(ctx, ing2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ingress: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	ingObj, ok := got.Object.(Ingress)
	if !ok {
		t.Fatalf("Object type = %T, want Ingress", got.Object)
	}
	if ingObj.Name != "ing-b" {
		t.Errorf("added ingress name = %q, want ing-b", ingObj.Name)
	}
}

func TestWatchNetworkPolicies_SnapshotAndAdded(t *testing.T) {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "np-a", Namespace: "default", ResourceVersion: "1"},
	}
	cs := fake.NewSimpleClientset(np)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchNetworkPolicies(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]NetworkPolicy)
	if !ok {
		t.Fatalf("Items type = %T, want []NetworkPolicy", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "np-a" {
		t.Fatalf("snapshot items = %+v, want one np-a", items)
	}

	np2 := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "np-b", Namespace: "default", ResourceVersion: "2"},
	}
	if _, err := cs.NetworkingV1().NetworkPolicies("default").Create(ctx, np2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create networkpolicy: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	npObj, ok := got.Object.(NetworkPolicy)
	if !ok {
		t.Fatalf("Object type = %T, want NetworkPolicy", got.Object)
	}
	if npObj.Name != "np-b" {
		t.Errorf("added networkpolicy name = %q, want np-b", npObj.Name)
	}
}

func TestWatchEndpointSlices_SnapshotAndAdded(t *testing.T) {
	es := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "es-a", Namespace: "default", ResourceVersion: "1", Labels: map[string]string{kubernetesIOServiceNameLabel: "svc-a"}},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	cs := fake.NewSimpleClientset(es)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = WatchEndpointSlices(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]EndpointSlice)
	if !ok {
		t.Fatalf("Items type = %T, want []EndpointSlice", snap.Items)
	}
	if len(items) != 1 || items[0].Name != "es-a" || items[0].ServiceName != "svc-a" {
		t.Fatalf("snapshot items = %+v, want one es-a serving svc-a", items)
	}

	es2 := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "es-b", Namespace: "default", ResourceVersion: "2"},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	if _, err := cs.DiscoveryV1().EndpointSlices("default").Create(ctx, es2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create endpointslice: %v", err)
	}

	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	esObj, ok := got.Object.(EndpointSlice)
	if !ok {
		t.Fatalf("Object type = %T, want EndpointSlice", got.Object)
	}
	if esObj.Name != "es-b" {
		t.Errorf("added endpointslice name = %q, want es-b", esObj.Name)
	}
}

// --- Tier-C cluster-scoped + storage smoke tests ---

func TestWatchNodes_SnapshotAndAdded(t *testing.T) {
	n := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-a", ResourceVersion: "1"}}
	cs := fake.NewSimpleClientset(n)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = WatchNodes(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]Node)
	if !ok || len(items) != 1 || items[0].Name != "node-a" {
		t.Fatalf("snapshot items = %+v, want one node-a", snap.Items)
	}

	n2 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-b", ResourceVersion: "2"}}
	if _, err := cs.CoreV1().Nodes().Create(ctx, n2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create node: %v", err)
	}
	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	nObj, ok := got.Object.(Node)
	if !ok || nObj.Name != "node-b" {
		t.Errorf("added node = %+v, want node-b", got.Object)
	}
}

func TestWatchNamespaces_SnapshotAndAdded(t *testing.T) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-a", ResourceVersion: "1"}}
	cs := fake.NewSimpleClientset(ns)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = WatchNamespaces(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]Namespace)
	if !ok || len(items) != 1 || items[0].Name != "ns-a" {
		t.Fatalf("snapshot items = %+v, want one ns-a", snap.Items)
	}

	ns2 := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-b", ResourceVersion: "2"}}
	if _, err := cs.CoreV1().Namespaces().Create(ctx, ns2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create namespace: %v", err)
	}
	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	nsObj, ok := got.Object.(Namespace)
	if !ok || nsObj.Name != "ns-b" {
		t.Errorf("added namespace = %+v, want ns-b", got.Object)
	}
}

func TestWatchPVs_SnapshotAndAdded(t *testing.T) {
	pv := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv-a", ResourceVersion: "1"}}
	cs := fake.NewSimpleClientset(pv)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = WatchPVs(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]PV)
	if !ok || len(items) != 1 || items[0].Name != "pv-a" {
		t.Fatalf("snapshot items = %+v, want one pv-a", snap.Items)
	}

	pv2 := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "pv-b", ResourceVersion: "2"}}
	if _, err := cs.CoreV1().PersistentVolumes().Create(ctx, pv2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pv: %v", err)
	}
	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	pvObj, ok := got.Object.(PV)
	if !ok || pvObj.Name != "pv-b" {
		t.Errorf("added pv = %+v, want pv-b", got.Object)
	}
}

func TestWatchPVCs_SnapshotAndAdded(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-a", Namespace: "default", ResourceVersion: "1"},
	}
	cs := fake.NewSimpleClientset(pvc)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = WatchPVCs(ctx, stubProvider{}, WatchArgs{
			Cluster:   clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
			Namespace: "default",
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]PVC)
	if !ok || len(items) != 1 || items[0].Name != "pvc-a" {
		t.Fatalf("snapshot items = %+v, want one pvc-a", snap.Items)
	}

	pvc2 := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-b", Namespace: "default", ResourceVersion: "2"},
	}
	if _, err := cs.CoreV1().PersistentVolumeClaims("default").Create(ctx, pvc2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create pvc: %v", err)
	}
	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	pvcObj, ok := got.Object.(PVC)
	if !ok || pvcObj.Name != "pvc-b" {
		t.Errorf("added pvc = %+v, want pvc-b", got.Object)
	}
}

func TestWatchStorageClasses_SnapshotAndAdded(t *testing.T) {
	sc := &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: "sc-a", ResourceVersion: "1"},
		Provisioner: "kubernetes.io/example",
	}
	cs := fake.NewSimpleClientset(sc)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = WatchStorageClasses(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]StorageClass)
	if !ok || len(items) != 1 || items[0].Name != "sc-a" {
		t.Fatalf("snapshot items = %+v, want one sc-a", snap.Items)
	}

	sc2 := &storagev1.StorageClass{
		ObjectMeta:  metav1.ObjectMeta{Name: "sc-b", ResourceVersion: "2"},
		Provisioner: "kubernetes.io/example",
	}
	if _, err := cs.StorageV1().StorageClasses().Create(ctx, sc2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create storageclass: %v", err)
	}
	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	scObj, ok := got.Object.(StorageClass)
	if !ok || scObj.Name != "sc-b" {
		t.Errorf("added storageclass = %+v, want sc-b", got.Object)
	}
}

func TestWatchIngressClasses_SnapshotAndAdded(t *testing.T) {
	ic := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "ic-a", ResourceVersion: "1"}}
	cs := fake.NewSimpleClientset(ic)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = WatchIngressClasses(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]IngressClass)
	if !ok || len(items) != 1 || items[0].Name != "ic-a" {
		t.Fatalf("snapshot items = %+v, want one ic-a", snap.Items)
	}

	ic2 := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "ic-b", ResourceVersion: "2"}}
	if _, err := cs.NetworkingV1().IngressClasses().Create(ctx, ic2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create ingressclass: %v", err)
	}
	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	icObj, ok := got.Object.(IngressClass)
	if !ok || icObj.Name != "ic-b" {
		t.Errorf("added ingressclass = %+v, want ic-b", got.Object)
	}
}

func TestWatchPriorityClasses_SnapshotAndAdded(t *testing.T) {
	pc := &schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{Name: "pc-a", ResourceVersion: "1"},
		Value:      1000,
	}
	cs := fake.NewSimpleClientset(pc)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = WatchPriorityClasses(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]PriorityClass)
	if !ok || len(items) != 1 || items[0].Name != "pc-a" {
		t.Fatalf("snapshot items = %+v, want one pc-a", snap.Items)
	}

	pc2 := &schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{Name: "pc-b", ResourceVersion: "2"},
		Value:      500,
	}
	if _, err := cs.SchedulingV1().PriorityClasses().Create(ctx, pc2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create priorityclass: %v", err)
	}
	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	pcObj, ok := got.Object.(PriorityClass)
	if !ok || pcObj.Name != "pc-b" {
		t.Errorf("added priorityclass = %+v, want pc-b", got.Object)
	}
}

func TestWatchRuntimeClasses_SnapshotAndAdded(t *testing.T) {
	rc := &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{Name: "rc-a", ResourceVersion: "1"},
		Handler:    "runc",
	}
	cs := fake.NewSimpleClientset(rc)
	swapNewClientFn(t, cs)

	sink := newTestSink(8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = WatchRuntimeClasses(ctx, stubProvider{}, WatchArgs{
			Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
		}, sink)
	}()

	snap := awaitEvent(t, sink)
	if snap.Type != WatchSnapshot {
		t.Fatalf("first event = %v, want snapshot", snap.Type)
	}
	items, ok := snap.Items.([]RuntimeClass)
	if !ok || len(items) != 1 || items[0].Name != "rc-a" {
		t.Fatalf("snapshot items = %+v, want one rc-a", snap.Items)
	}

	rc2 := &nodev1.RuntimeClass{
		ObjectMeta: metav1.ObjectMeta{Name: "rc-b", ResourceVersion: "2"},
		Handler:    "kata",
	}
	if _, err := cs.NodeV1().RuntimeClasses().Create(ctx, rc2, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create runtimeclass: %v", err)
	}
	got := awaitEvent(t, sink)
	if got.Type != WatchAdded {
		t.Fatalf("event = %v, want added", got.Type)
	}
	rcObj, ok := got.Object.(RuntimeClass)
	if !ok || rcObj.Name != "rc-b" {
		t.Errorf("added runtimeclass = %+v, want rc-b", got.Object)
	}
}

func TestWatchPods_ListErrorPropagates(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "pods", func(_ ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("apiserver-down")
	})
	swapNewClientFn(t, cs)

	err := WatchPods(context.Background(), stubProvider{}, WatchArgs{
		Cluster: clusters.Cluster{Name: "demo", Backend: clusters.BackendKubeconfig},
	}, newTestSink(8))
	if err == nil {
		t.Fatal("WatchPods returned nil, want list error")
	}
	if !strings.Contains(err.Error(), "apiserver-down") {
		t.Errorf("err = %v, want it to contain 'apiserver-down'", err)
	}
}
