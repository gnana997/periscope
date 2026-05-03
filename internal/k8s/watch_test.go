package k8s

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
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
