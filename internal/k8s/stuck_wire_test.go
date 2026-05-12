package k8s

// stuck_wire_test.go — proves the per-kind detector is actually wired
// through the summary builders. These tests don't re-cover the
// detector's branch matrix (stuck_test.go does that exhaustively);
// they only assert that listing a deliberately-stuck workload returns
// a Row with the `Stuck` field populated.
//
// Fixtures use a creation timestamp ~30m before real wall-clock so
// the detector's `time.Now()` will reliably read as past the
// StuckThreshold without coupling the test to a fake clock.

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

)

// ── Deployment wiring ──────────────────────────────────────────────

func TestListDeployments_StuckFieldPopulated(t *testing.T) {
	thirtyMinAgo := metav1.NewTime(time.Now().Add(-30 * time.Minute))
	replicas := int32(3)

	cs := fake.NewSimpleClientset(
		// Stuck: Progressing condition tripped 14m ago.
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "stuck", Namespace: "default",
				CreationTimestamp: thirtyMinAgo,
			},
			Spec: appsv1.DeploymentSpec{Replicas: &replicas},
			Status: appsv1.DeploymentStatus{
				Replicas:        3,
				UpdatedReplicas: 1,
				Conditions: []appsv1.DeploymentCondition{{
					Type:               appsv1.DeploymentProgressing,
					Status:             "False",
					Reason:             "ProgressDeadlineExceeded",
					LastTransitionTime: metav1.NewTime(time.Now().Add(-14 * time.Minute)),
				}},
			},
		},
		// Healthy.
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "healthy", Namespace: "default",
				CreationTimestamp: thirtyMinAgo,
			},
			Spec: appsv1.DeploymentSpec{Replicas: &replicas},
			Status: appsv1.DeploymentStatus{
				Replicas: 3, UpdatedReplicas: 3, ReadyReplicas: 3, AvailableReplicas: 3,
			},
		},
	)
	withFakeClient(t, cs)

	got, err := ListDeployments(context.Background(), stubProvider{}, ListDeploymentsArgs{
		Cluster: testCluster, Namespace: "default",
	})
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	byName := map[string]Deployment{}
	for _, d := range got.Deployments {
		byName[d.Name] = d
	}
	if byName["stuck"].Stuck == nil {
		t.Fatalf("stuck deployment: want Stuck populated, got nil")
	}
	if byName["stuck"].Stuck.Reason != StuckReasonProgressDeadlineExceeded {
		t.Errorf("stuck deployment: want reason %s, got %s",
			StuckReasonProgressDeadlineExceeded, byName["stuck"].Stuck.Reason)
	}
	if byName["healthy"].Stuck != nil {
		t.Errorf("healthy deployment: want Stuck nil, got %+v", byName["healthy"].Stuck)
	}
}

// ── StatefulSet wiring ─────────────────────────────────────────────

func TestListStatefulSets_StuckFieldPopulated(t *testing.T) {
	thirtyMinAgo := metav1.NewTime(time.Now().Add(-30 * time.Minute))
	replicas := int32(3)

	cs := fake.NewSimpleClientset(
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "stuck-sts", Namespace: "default",
				CreationTimestamp: thirtyMinAgo,
			},
			Spec: appsv1.StatefulSetSpec{Replicas: &replicas},
			Status: appsv1.StatefulSetStatus{
				Replicas:        3,
				UpdatedReplicas: 1,
			},
		},
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "healthy-sts", Namespace: "default",
				CreationTimestamp: thirtyMinAgo,
			},
			Spec: appsv1.StatefulSetSpec{Replicas: &replicas},
			Status: appsv1.StatefulSetStatus{
				Replicas: 3, UpdatedReplicas: 3, ReadyReplicas: 3,
			},
		},
	)
	withFakeClient(t, cs)

	got, err := ListStatefulSets(context.Background(), stubProvider{}, ListStatefulSetsArgs{
		Cluster: testCluster, Namespace: "default",
	})
	if err != nil {
		t.Fatalf("ListStatefulSets: %v", err)
	}
	byName := map[string]StatefulSet{}
	for _, s := range got.StatefulSets {
		byName[s.Name] = s
	}
	if byName["stuck-sts"].Stuck == nil {
		t.Fatalf("stuck-sts: want Stuck populated, got nil")
	}
	if byName["stuck-sts"].Stuck.Reason != StuckReasonStalled {
		t.Errorf("stuck-sts: want reason %s, got %s",
			StuckReasonStalled, byName["stuck-sts"].Stuck.Reason)
	}
	if byName["healthy-sts"].Stuck != nil {
		t.Errorf("healthy-sts: want Stuck nil, got %+v", byName["healthy-sts"].Stuck)
	}
}

// ── DaemonSet wiring ───────────────────────────────────────────────

func TestListDaemonSets_StuckFieldPopulated(t *testing.T) {
	thirtyMinAgo := metav1.NewTime(time.Now().Add(-30 * time.Minute))

	cs := fake.NewSimpleClientset(
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "stuck-ds", Namespace: "default",
				CreationTimestamp: thirtyMinAgo,
			},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 5,
				UpdatedNumberScheduled: 2,
			},
		},
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "healthy-ds", Namespace: "default",
				CreationTimestamp: thirtyMinAgo,
			},
			Status: appsv1.DaemonSetStatus{
				DesiredNumberScheduled: 5,
				UpdatedNumberScheduled: 5,
				NumberReady:            5,
			},
		},
	)
	withFakeClient(t, cs)

	got, err := ListDaemonSets(context.Background(), stubProvider{}, ListDaemonSetsArgs{
		Cluster: testCluster, Namespace: "default",
	})
	if err != nil {
		t.Fatalf("ListDaemonSets: %v", err)
	}
	byName := map[string]DaemonSet{}
	for _, d := range got.DaemonSets {
		byName[d.Name] = d
	}
	if byName["stuck-ds"].Stuck == nil {
		t.Fatalf("stuck-ds: want Stuck populated, got nil")
	}
	if byName["stuck-ds"].Stuck.Reason != StuckReasonStalled {
		t.Errorf("stuck-ds: want reason %s, got %s",
			StuckReasonStalled, byName["stuck-ds"].Stuck.Reason)
	}
	if byName["healthy-ds"].Stuck != nil {
		t.Errorf("healthy-ds: want Stuck nil, got %+v", byName["healthy-ds"].Stuck)
	}
}
