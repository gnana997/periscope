package k8s

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

func newPodFake() *fake.Clientset {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	return fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:              "web",
				Namespace:         "default",
				CreationTimestamp: metav1.NewTime(now),
				Labels:            map[string]string{"app": "web"},
				Annotations:       map[string]string{"foo": "bar"},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: "app", Image: "acme/app:v1"},
					{Name: "sidecar", Image: "acme/sidecar:v1"},
				},
				NodeName: "node-1",
			},
			Status: corev1.PodStatus{
				Phase:    corev1.PodRunning,
				PodIP:    "10.0.0.5",
				HostIP:   "10.0.0.1",
				QOSClass: corev1.PodQOSBurstable,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "app", Ready: true, RestartCount: 1, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
					{Name: "sidecar", Ready: false, RestartCount: 3, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff", Message: "boom"}}},
				},
				Conditions: []corev1.PodCondition{
					{Type: "Ready", Status: "False", Reason: "ContainersNotReady"},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "worker", Namespace: "kube-system", CreationTimestamp: metav1.NewTime(now),
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "main"}},
			},
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		},
	)
}

func withFakeClient(t *testing.T, cs kubernetes.Interface) {
	t.Helper()
	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientFn = orig })
}

var testCluster = clusters.Cluster{
	Name: "test", ARN: "arn:aws:eks:us-east-1:1:cluster/test", Region: "us-east-1",
}

func TestListPods(t *testing.T) {
	withFakeClient(t, newPodFake())

	t.Run("all namespaces", func(t *testing.T) {
		result, err := ListPods(context.Background(), stubProvider{}, ListPodsArgs{Cluster: testCluster})
		if err != nil {
			t.Fatalf("ListPods: %v", err)
		}
		if got, want := len(result.Pods), 2; got != want {
			t.Fatalf("got %d pods, want %d", got, want)
		}
	})

	t.Run("namespace filter and field mapping", func(t *testing.T) {
		result, err := ListPods(context.Background(), stubProvider{}, ListPodsArgs{
			Cluster: testCluster, Namespace: "default",
		})
		if err != nil {
			t.Fatalf("ListPods: %v", err)
		}
		if got, want := len(result.Pods), 1; got != want {
			t.Fatalf("got %d pods, want %d", got, want)
		}
		p := result.Pods[0]
		if p.Ready != "1/2" {
			t.Errorf("Ready = %q, want 1/2", p.Ready)
		}
		if p.Restarts != 4 {
			t.Errorf("Restarts = %d, want 4", p.Restarts)
		}
	})
}

func TestGetPod(t *testing.T) {
	withFakeClient(t, newPodFake())

	pd, err := GetPod(context.Background(), stubProvider{}, GetPodArgs{
		Cluster: testCluster, Namespace: "default", Name: "web",
	})
	if err != nil {
		t.Fatalf("GetPod: %v", err)
	}

	if pd.Name != "web" {
		t.Errorf("Name = %q", pd.Name)
	}
	if pd.HostIP != "10.0.0.1" {
		t.Errorf("HostIP = %q", pd.HostIP)
	}
	if pd.QOSClass != "Burstable" {
		t.Errorf("QOSClass = %q", pd.QOSClass)
	}
	if got, want := len(pd.Containers), 2; got != want {
		t.Fatalf("Containers = %d, want %d", got, want)
	}
	app := pd.Containers[0]
	if app.Image != "acme/app:v1" || app.State != "Running" || !app.Ready {
		t.Errorf("app container = %+v", app)
	}
	side := pd.Containers[1]
	if side.State != "Waiting" || side.Reason != "CrashLoopBackOff" {
		t.Errorf("sidecar container = %+v", side)
	}
	if got := pd.Labels["app"]; got != "web" {
		t.Errorf("Labels[app] = %q", got)
	}
}

func TestGetPodYAML(t *testing.T) {
	withFakeClient(t, newPodFake())

	out, err := GetPodYAML(context.Background(), stubProvider{}, GetPodArgs{
		Cluster: testCluster, Namespace: "default", Name: "web",
	})
	if err != nil {
		t.Fatalf("GetPodYAML: %v", err)
	}
	for _, want := range []string{"apiVersion: v1", "kind: Pod", "name: web", "namespace: default"} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml missing %q\nfull:\n%s", want, out)
		}
	}
	if strings.Contains(out, "managedFields") {
		t.Errorf("yaml contained managedFields (should be stripped)")
	}
}

func TestListObjectEvents(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cs := fake.NewSimpleClientset(
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "e1", Namespace: "default"},
			Type:       "Warning",
			Reason:     "BackOff",
			Message:    "Back-off restarting failed container",
			Count:      5,
			LastTimestamp: metav1.NewTime(now),
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod", Name: "web", Namespace: "default",
			},
			Source: corev1.EventSource{Component: "kubelet"},
		},
	)
	withFakeClient(t, cs)

	out, err := ListObjectEvents(context.Background(), stubProvider{}, ListObjectEventsArgs{
		Cluster: testCluster, Kind: "Pod", Namespace: "default", Name: "web",
	})
	if err != nil {
		t.Fatalf("ListObjectEvents: %v", err)
	}
	if got, want := len(out.Events), 1; got != want {
		t.Fatalf("got %d events, want %d", got, want)
	}
	e := out.Events[0]
	if e.Type != "Warning" || e.Reason != "BackOff" {
		t.Errorf("event = %+v", e)
	}
	if e.Source != "kubelet" {
		t.Errorf("Source = %q", e.Source)
	}
}
