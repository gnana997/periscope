package k8s

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var fixedNow = time.Date(2026, 5, 12, 12, 0, 0, 0, time.UTC)

// minutesAgo returns a metav1.Time `mins` minutes before fixedNow.
// Test fixtures stay readable: condA{minutesAgo(20), Progressing, False, ProgressDeadlineExceeded}.
func minutesAgo(mins int) metav1.Time {
	return metav1.NewTime(fixedNow.Add(time.Duration(-mins) * time.Minute))
}

// ── DetectDeploymentStuck ──────────────────────────────────────────

func TestDetectDeploymentStuck(t *testing.T) {
	replicas := func(n int32) *int32 { return &n }

	cases := []struct {
		name     string
		dep      *appsv1.Deployment
		wantNil  bool
		wantKind StuckReason
		wantMs   int64 // approximate; compared with a 1s tolerance
	}{
		{
			name: "nil deployment returns nil",
			dep:  nil,
			wantNil: true,
		},
		{
			name: "nil when healthy",
			dep: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: replicas(3)},
				Status: appsv1.DeploymentStatus{
					Replicas:        3,
					UpdatedReplicas: 3,
				},
			},
			wantNil: true,
		},
		{
			name: "nil when no replicas",
			dep: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: replicas(0)},
				Status: appsv1.DeploymentStatus{
					Conditions: []appsv1.DeploymentCondition{{
						Type:               appsv1.DeploymentProgressing,
						Status:             "False",
						Reason:             "ProgressDeadlineExceeded",
						LastTransitionTime: minutesAgo(20),
					}},
				},
			},
			wantNil: true,
		},
		{
			name: "progress_deadline_exceeded — flagged with since=condition time",
			dep: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: replicas(3)},
				Status: appsv1.DeploymentStatus{
					Replicas:        3,
					UpdatedReplicas: 1,
					Conditions: []appsv1.DeploymentCondition{{
						Type:               appsv1.DeploymentProgressing,
						Status:             "False",
						Reason:             "ProgressDeadlineExceeded",
						LastTransitionTime: minutesAgo(14),
					}},
				},
			},
			wantKind: StuckReasonProgressDeadlineExceeded,
			wantMs:   int64(14 * time.Minute / time.Millisecond),
		},
		{
			name: "progress_deadline_exceeded takes priority over stalled",
			dep: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: replicas(5)},
				Status: appsv1.DeploymentStatus{
					Replicas:        5,
					UpdatedReplicas: 2,
					Conditions: []appsv1.DeploymentCondition{
						{
							Type:               appsv1.DeploymentAvailable,
							Status:             "False",
							LastTransitionTime: minutesAgo(40),
						},
						{
							Type:               appsv1.DeploymentProgressing,
							Status:             "False",
							Reason:             "ProgressDeadlineExceeded",
							LastTransitionTime: minutesAgo(7),
						},
					},
				},
			},
			wantKind: StuckReasonProgressDeadlineExceeded,
			wantMs:   int64(7 * time.Minute / time.Millisecond),
		},
		{
			name: "stalled when updated < replicas and condition is older than threshold",
			dep: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: replicas(4)},
				Status: appsv1.DeploymentStatus{
					Replicas:        4,
					UpdatedReplicas: 1,
					Conditions: []appsv1.DeploymentCondition{{
						Type:               appsv1.DeploymentProgressing,
						Status:             "True",
						Reason:             "NewReplicaSetCreated",
						LastTransitionTime: minutesAgo(20),
					}},
				},
			},
			wantKind: StuckReasonStalled,
			wantMs:   int64(20 * time.Minute / time.Millisecond),
		},
		{
			name: "not stalled when threshold not reached",
			dep: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: replicas(4)},
				Status: appsv1.DeploymentStatus{
					Replicas:        4,
					UpdatedReplicas: 1,
					Conditions: []appsv1.DeploymentCondition{{
						Type:               appsv1.DeploymentProgressing,
						Status:             "True",
						LastTransitionTime: minutesAgo(5),
					}},
				},
			},
			wantNil: true,
		},
		{
			name: "stalled uses creation timestamp when no conditions present",
			dep: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					CreationTimestamp: minutesAgo(30),
				},
				Spec: appsv1.DeploymentSpec{Replicas: replicas(2)},
				Status: appsv1.DeploymentStatus{
					Replicas:        2,
					UpdatedReplicas: 0,
				},
			},
			wantKind: StuckReasonStalled,
			wantMs:   int64(30 * time.Minute / time.Millisecond),
		},
		{
			name: "sinceMs clamps to zero on future condition timestamp",
			dep: &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{Replicas: replicas(3)},
				Status: appsv1.DeploymentStatus{
					Replicas:        3,
					UpdatedReplicas: 1,
					Conditions: []appsv1.DeploymentCondition{{
						Type:               appsv1.DeploymentProgressing,
						Status:             "False",
						Reason:             "ProgressDeadlineExceeded",
						LastTransitionTime: metav1.NewTime(fixedNow.Add(5 * time.Minute)),
					}},
				},
			},
			wantKind: StuckReasonProgressDeadlineExceeded,
			wantMs:   0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectDeploymentStuck(c.dep, fixedNow)
			if c.wantNil {
				if got != nil {
					t.Fatalf("want nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want %s, got nil", c.wantKind)
			}
			if got.Reason != c.wantKind {
				t.Errorf("reason: want %s, got %s", c.wantKind, got.Reason)
			}
			if diff := got.SinceMs - c.wantMs; diff < -1000 || diff > 1000 {
				t.Errorf("sinceMs: want ~%d, got %d", c.wantMs, got.SinceMs)
			}
		})
	}
}

// ── DetectStatefulSetStuck ─────────────────────────────────────────

func TestDetectStatefulSetStuck(t *testing.T) {
	replicas := func(n int32) *int32 { return &n }

	cases := []struct {
		name    string
		sts     *appsv1.StatefulSet
		wantNil bool
		wantMs  int64
	}{
		{name: "nil returns nil", sts: nil, wantNil: true},
		{
			name: "nil when healthy",
			sts: &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{Replicas: replicas(3)},
				Status: appsv1.StatefulSetStatus{
					Replicas:        3,
					UpdatedReplicas: 3,
				},
			},
			wantNil: true,
		},
		{
			name: "nil when no replicas",
			sts: &appsv1.StatefulSet{
				Spec:   appsv1.StatefulSetSpec{Replicas: replicas(0)},
				Status: appsv1.StatefulSetStatus{},
			},
			wantNil: true,
		},
		{
			name: "stalled when updated < replicas and condition older than threshold",
			sts: &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{Replicas: replicas(3)},
				Status: appsv1.StatefulSetStatus{
					Replicas:        3,
					UpdatedReplicas: 1,
					Conditions: []appsv1.StatefulSetCondition{{
						Type:               "Available",
						Status:             "False",
						LastTransitionTime: minutesAgo(15),
					}},
				},
			},
			wantMs: int64(15 * time.Minute / time.Millisecond),
		},
		{
			name: "not stalled within threshold",
			sts: &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{Replicas: replicas(3)},
				Status: appsv1.StatefulSetStatus{
					Replicas:        3,
					UpdatedReplicas: 1,
					Conditions: []appsv1.StatefulSetCondition{{
						LastTransitionTime: minutesAgo(5),
					}},
				},
			},
			wantNil: true,
		},
		{
			name: "stalled uses creation timestamp when no conditions",
			sts: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: minutesAgo(45)},
				Spec:       appsv1.StatefulSetSpec{Replicas: replicas(2)},
				Status: appsv1.StatefulSetStatus{
					Replicas:        2,
					UpdatedReplicas: 0,
				},
			},
			wantMs: int64(45 * time.Minute / time.Millisecond),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectStatefulSetStuck(c.sts, fixedNow)
			if c.wantNil {
				if got != nil {
					t.Fatalf("want nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want Stalled, got nil")
			}
			if got.Reason != StuckReasonStalled {
				t.Errorf("reason: want stalled, got %s", got.Reason)
			}
			if diff := got.SinceMs - c.wantMs; diff < -1000 || diff > 1000 {
				t.Errorf("sinceMs: want ~%d, got %d", c.wantMs, got.SinceMs)
			}
		})
	}
}

// ── DetectDaemonSetStuck ───────────────────────────────────────────

func TestDetectDaemonSetStuck(t *testing.T) {
	cases := []struct {
		name    string
		ds      *appsv1.DaemonSet
		wantNil bool
		wantMs  int64
	}{
		{name: "nil returns nil", ds: nil, wantNil: true},
		{
			name: "nil when healthy",
			ds: &appsv1.DaemonSet{
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 4,
					UpdatedNumberScheduled: 4,
				},
			},
			wantNil: true,
		},
		{
			name: "nil when desired is zero",
			ds: &appsv1.DaemonSet{
				Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 0},
			},
			wantNil: true,
		},
		{
			name: "stalled when updated < desired and condition older than threshold",
			ds: &appsv1.DaemonSet{
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 5,
					UpdatedNumberScheduled: 2,
					Conditions: []appsv1.DaemonSetCondition{{
						LastTransitionTime: minutesAgo(25),
					}},
				},
			},
			wantMs: int64(25 * time.Minute / time.Millisecond),
		},
		{
			name: "not stalled within threshold",
			ds: &appsv1.DaemonSet{
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 5,
					UpdatedNumberScheduled: 2,
					Conditions: []appsv1.DaemonSetCondition{{
						LastTransitionTime: minutesAgo(3),
					}},
				},
			},
			wantNil: true,
		},
		{
			name: "stalled uses creation timestamp when no conditions",
			ds: &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{CreationTimestamp: minutesAgo(60)},
				Status: appsv1.DaemonSetStatus{
					DesiredNumberScheduled: 3,
					UpdatedNumberScheduled: 0,
				},
			},
			wantMs: int64(60 * time.Minute / time.Millisecond),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectDaemonSetStuck(c.ds, fixedNow)
			if c.wantNil {
				if got != nil {
					t.Fatalf("want nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want Stalled, got nil")
			}
			if got.Reason != StuckReasonStalled {
				t.Errorf("reason: want stalled, got %s", got.Reason)
			}
			if diff := got.SinceMs - c.wantMs; diff < -1000 || diff > 1000 {
				t.Errorf("sinceMs: want ~%d, got %d", c.wantMs, got.SinceMs)
			}
		})
	}
}

// ── effectiveReplicas ──────────────────────────────────────────────

func TestEffectiveReplicas(t *testing.T) {
	n := int32(7)
	cases := []struct {
		name   string
		spec   *int32
		status int32
		want   int32
	}{
		{"spec set wins", &n, 3, 7},
		{"spec nil falls back to status", nil, 5, 5},
		{"both zero -> zero", new(int32), 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveReplicas(c.spec, c.status); got != c.want {
				t.Errorf("want %d, got %d", c.want, got)
			}
		})
	}
}

// ── sinceMs ────────────────────────────────────────────────────────

func TestSinceMs(t *testing.T) {
	now := fixedNow
	cases := []struct {
		name  string
		since time.Time
		want  int64
	}{
		{"5s ago", now.Add(-5 * time.Second), 5000},
		{"5m ago", now.Add(-5 * time.Minute), int64(5 * time.Minute / time.Millisecond)},
		{"future since clamps to zero", now.Add(2 * time.Minute), 0},
		{"same moment", now, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sinceMs(c.since, now); got != c.want {
				t.Errorf("want %d, got %d", c.want, got)
			}
		})
	}
}

// ── latest*ConditionTime helpers ────────────────────────────────────

func TestLatestDeploymentConditionTime(t *testing.T) {
	t.Run("empty slice returns zero time", func(t *testing.T) {
		if got := latestDeploymentConditionTime(nil); !got.IsZero() {
			t.Errorf("want zero, got %v", got)
		}
	})
	t.Run("returns most recent across multiple", func(t *testing.T) {
		conds := []appsv1.DeploymentCondition{
			{LastTransitionTime: minutesAgo(30)},
			{LastTransitionTime: minutesAgo(10)},
			{LastTransitionTime: minutesAgo(20)},
		}
		got := latestDeploymentConditionTime(conds)
		want := minutesAgo(10).Time
		if !got.Equal(want) {
			t.Errorf("want %v, got %v", want, got)
		}
	})
}

func TestLatestStatefulSetConditionTime(t *testing.T) {
	conds := []appsv1.StatefulSetCondition{
		{LastTransitionTime: minutesAgo(50)},
		{LastTransitionTime: minutesAgo(15)},
	}
	got := latestStatefulSetConditionTime(conds)
	want := minutesAgo(15).Time
	if !got.Equal(want) {
		t.Errorf("want %v, got %v", want, got)
	}
}

func TestLatestDaemonSetConditionTime(t *testing.T) {
	conds := []appsv1.DaemonSetCondition{
		{LastTransitionTime: minutesAgo(2)},
		{LastTransitionTime: minutesAgo(99)},
	}
	got := latestDaemonSetConditionTime(conds)
	want := minutesAgo(2).Time
	if !got.Equal(want) {
		t.Errorf("want %v, got %v", want, got)
	}
}
