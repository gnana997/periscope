package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	clientgotesting "k8s.io/client-go/testing"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

func swapClient(t *testing.T, cs kubernetes.Interface) {
	t.Helper()
	orig := newClientFn
	newClientFn = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	t.Cleanup(func() { newClientFn = orig })
}

func rbCluster() clusters.Cluster {
	return clusters.Cluster{Name: "test"}
}

// --- Deployment ----------------------------------------------------

func TestListRevisions_Deployment(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	depUID := types.UID("dep-uid-1")
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}

	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "ns", UID: depUID,
			Annotations: map[string]string{
				"deployment.kubernetes.io/revision": "3",
			},
		},
		Spec: appsv1.DeploymentSpec{Selector: selector, Paused: false},
	}
	rsOld := buildRS("web-old", "ns", depUID, "1", "old-hash", "promote v1.4.0", now.Add(-48*time.Hour),
		map[string]string{"app": "web", "pod-template-hash": "old-hash"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": "old-hash"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1.4.0"}}},
		},
	)
	rsMid := buildRS("web-mid", "ns", depUID, "2", "mid-hash", "promote v1.4.1", now.Add(-24*time.Hour),
		map[string]string{"app": "web", "pod-template-hash": "mid-hash"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": "mid-hash"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1.4.1"}}},
		},
	)
	rsCur := buildRS("web-cur", "ns", depUID, "3", "cur-hash", "promote v1.4.2", now.Add(-2*time.Hour),
		map[string]string{"app": "web", "pod-template-hash": "cur-hash"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": "cur-hash"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1.4.2"}}},
		},
	)
	cs := fake.NewSimpleClientset(dep, rsOld, rsMid, rsCur)
	swapClient(t, cs)

	hist, err := ListRevisions(context.Background(), nil, ListRevisionsArgs{
		Cluster: rbCluster(), Kind: KindDeployment, Namespace: "ns", Name: "web",
	})
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if hist.CurrentRevision != 3 {
		t.Errorf("currentRevision = %d, want 3", hist.CurrentRevision)
	}
	if got := len(hist.Revisions); got != 3 {
		t.Fatalf("revisions len = %d, want 3", got)
	}
	// Newest first.
	if hist.Revisions[0].Revision != 3 || !hist.Revisions[0].IsCurrent {
		t.Errorf("Revisions[0] = %+v; want revision 3 + isCurrent", hist.Revisions[0])
	}
	if hist.Revisions[2].Revision != 1 {
		t.Errorf("Revisions[2].revision = %d, want 1", hist.Revisions[2].Revision)
	}
	if got := hist.Revisions[0].Images; len(got) != 1 || got[0] != "app:v1.4.2" {
		t.Errorf("Images = %v, want [app:v1.4.2]", got)
	}
	if hist.Revisions[1].ChangeCause != "promote v1.4.1" {
		t.Errorf("ChangeCause = %q, want %q", hist.Revisions[1].ChangeCause, "promote v1.4.1")
	}
	if hist.Paused == nil || *hist.Paused {
		t.Errorf("Paused = %v, want non-nil false", hist.Paused)
	}
	if hist.ManagedBy != nil {
		t.Errorf("ManagedBy = %+v, want nil", hist.ManagedBy)
	}
}

func TestListRevisions_Deployment_GitOpsAndPaused(t *testing.T) {
	depUID := types.UID("dep-uid-2")
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "argo-app"}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "argo-app", Namespace: "default", UID: depUID,
			Annotations: map[string]string{
				"deployment.kubernetes.io/revision": "5",
				"argocd.argoproj.io/instance":       "web-prod",
			},
		},
		Spec: appsv1.DeploymentSpec{Selector: selector, Paused: true},
	}
	rs := buildRS("argo-app-rs", "default", depUID, "5", "h5", "", time.Now(),
		map[string]string{"app": "argo-app", "pod-template-hash": "h5"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "argo-app", "pod-template-hash": "h5"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1"}}},
		},
	)
	swapClient(t, fake.NewSimpleClientset(dep, rs))

	hist, err := ListRevisions(context.Background(), nil, ListRevisionsArgs{
		Cluster: rbCluster(), Kind: KindDeployment, Namespace: "default", Name: "argo-app",
	})
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if hist.Paused == nil || !*hist.Paused {
		t.Errorf("Paused = %v, want non-nil true", hist.Paused)
	}
	if hist.ManagedBy == nil || hist.ManagedBy.Controller != "argocd" || hist.ManagedBy.Instance != "web-prod" {
		t.Errorf("ManagedBy = %+v, want argocd/web-prod", hist.ManagedBy)
	}
}

func TestRollback_Deployment_PatchShape(t *testing.T) {
	depUID := types.UID("dep-uid-3")
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "ns", UID: depUID,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
		},
		Spec: appsv1.DeploymentSpec{Selector: selector},
	}
	rsOld := buildRS("web-old", "ns", depUID, "1", "h1", "v1", time.Now().Add(-time.Hour),
		map[string]string{"app": "web", "pod-template-hash": "h1"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": "h1"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1"}}},
		},
	)
	rsCur := buildRS("web-cur", "ns", depUID, "2", "h2", "v2", time.Now(),
		map[string]string{"app": "web", "pod-template-hash": "h2"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": "h2"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v2"}}},
		},
	)
	cs := fake.NewSimpleClientset(dep, rsOld, rsCur)

	// Capture the PATCH so we can assert on its body.
	var capturedPatch []byte
	cs.PrependReactor("patch", "deployments", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		patchAction, ok := action.(clientgotesting.PatchAction)
		if !ok {
			return false, nil, nil
		}
		capturedPatch = patchAction.GetPatch()
		// Return the dep so the typed client can re-apply the patch
		// against it. The fake client doesn't really apply strategic
		// merge patches; we rely on captured bytes for assertions.
		return true, dep, nil
	})

	swapClient(t, cs)
	got, err := Rollback(context.Background(), nil, RollbackArgs{
		Cluster: rbCluster(), Kind: KindDeployment, Namespace: "ns", Name: "web",
		ToRevision: 1, Reason: "OOMKill on v2", ActorSubject: "alice@example.com",
	})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	_ = got // we don't assert on NewRevision because the fake echoes the input

	if len(capturedPatch) == 0 {
		t.Fatal("no patch captured")
	}
	var patchBody map[string]any
	if err := json.Unmarshal(capturedPatch, &patchBody); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	meta := patchBody["metadata"].(map[string]any)
	annotations := meta["annotations"].(map[string]any)
	cc, _ := annotations[changeCauseAnnotation].(string)
	if cc == "" || !strings.Contains(cc, "revision 1") || !strings.Contains(cc, "alice@example.com") || !strings.Contains(cc, "OOMKill on v2") {
		t.Errorf("change-cause = %q; want to mention rev 1, actor, and reason", cc)
	}
	spec := patchBody["spec"].(map[string]any)
	if _, ok := spec["template"]; !ok {
		t.Errorf("patch missing spec.template")
	}
}

func TestRollback_RevisionNotFound(t *testing.T) {
	depUID := types.UID("dep-uid-4")
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "ns", UID: depUID,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"},
		},
		Spec: appsv1.DeploymentSpec{Selector: selector},
	}
	rs := buildRS("web-rs", "ns", depUID, "1", "h1", "", time.Now(),
		map[string]string{"app": "web"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1"}}},
		},
	)
	swapClient(t, fake.NewSimpleClientset(dep, rs))

	_, err := Rollback(context.Background(), nil, RollbackArgs{
		Cluster: rbCluster(), Kind: KindDeployment, Namespace: "ns", Name: "web", ToRevision: 99,
	})
	if !errors.Is(err, ErrRevisionNotFound) {
		t.Errorf("err = %v, want ErrRevisionNotFound", err)
	}
}

func TestRollback_AlreadyAtRevision(t *testing.T) {
	depUID := types.UID("dep-uid-5")
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "ns", UID: depUID,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
		},
		Spec: appsv1.DeploymentSpec{Selector: selector},
	}
	rsCur := buildRS("web-cur", "ns", depUID, "2", "h2", "", time.Now(),
		map[string]string{"app": "web"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v2"}}},
		},
	)
	swapClient(t, fake.NewSimpleClientset(dep, rsCur))

	_, err := Rollback(context.Background(), nil, RollbackArgs{
		Cluster: rbCluster(), Kind: KindDeployment, Namespace: "ns", Name: "web", ToRevision: 2,
	})
	if !errors.Is(err, ErrAlreadyAtRevision) {
		t.Errorf("err = %v, want ErrAlreadyAtRevision", err)
	}
}

func TestRollback_PausedDeployment(t *testing.T) {
	depUID := types.UID("dep-uid-6")
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "ns", UID: depUID,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
		},
		Spec: appsv1.DeploymentSpec{Selector: selector, Paused: true},
	}
	rsOld := buildRS("web-old", "ns", depUID, "1", "h1", "", time.Now().Add(-time.Hour),
		map[string]string{"app": "web"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1"}}},
		},
	)
	rsCur := buildRS("web-cur", "ns", depUID, "2", "h2", "", time.Now(),
		map[string]string{"app": "web"},
		corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v2"}}},
		},
	)
	swapClient(t, fake.NewSimpleClientset(dep, rsOld, rsCur))

	_, err := Rollback(context.Background(), nil, RollbackArgs{
		Cluster: rbCluster(), Kind: KindDeployment, Namespace: "ns", Name: "web", ToRevision: 1,
	})
	if !errors.Is(err, ErrDeploymentPaused) {
		t.Errorf("err = %v, want ErrDeploymentPaused", err)
	}
}

func TestRollback_UnsupportedKind(t *testing.T) {
	_, err := Rollback(context.Background(), nil, RollbackArgs{
		Cluster: rbCluster(), Kind: "pods", Namespace: "ns", Name: "p",
	})
	if !errors.Is(err, ErrUnsupportedKind) {
		t.Errorf("err = %v, want ErrUnsupportedKind", err)
	}
}

// --- DetectManagedBy ----------------------------------------------

func TestDetectManagedBy(t *testing.T) {
	cases := []struct {
		name        string
		annotations map[string]string
		labels      map[string]string
		want        *ManagedBy
	}{
		{name: "argocd via annotation",
			annotations: map[string]string{"argocd.argoproj.io/instance": "web-prod"},
			want:        &ManagedBy{Controller: "argocd", Instance: "web-prod"}},
		{name: "helm via annotation",
			annotations: map[string]string{"meta.helm.sh/release-name": "my-release"},
			want:        &ManagedBy{Controller: "helm", Instance: "my-release"}},
		{name: "helm via label",
			labels: map[string]string{"app.kubernetes.io/managed-by": "Helm"},
			want:   &ManagedBy{Controller: "helm"}},
		{name: "flux via annotation",
			annotations: map[string]string{"kustomize.toolkit.fluxcd.io/name": "platform"},
			want:        &ManagedBy{Controller: "flux", Instance: "platform"}},
		{name: "flux via label",
			labels: map[string]string{"app.kubernetes.io/managed-by": "Flux"},
			want:   &ManagedBy{Controller: "flux"}},
		{name: "neither", want: nil},
		{name: "argocd wins over helm on conflict",
			annotations: map[string]string{
				"argocd.argoproj.io/instance":   "argo-app",
				"meta.helm.sh/release-name":     "helm-release",
			},
			want: &ManagedBy{Controller: "argocd", Instance: "argo-app"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectManagedBy(tc.annotations, tc.labels)
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if got != nil && (got.Controller != tc.want.Controller || got.Instance != tc.want.Instance) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// --- HPA detection -------------------------------------------------

func TestFindHPATarget(t *testing.T) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: "web-hpa", Namespace: "ns"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				Kind: "Deployment",
				Name: "web",
			},
		},
	}
	cs := fake.NewSimpleClientset(hpa)
	if got := findHPATarget(context.Background(), cs, "ns", "Deployment", "web"); got != "web-hpa" {
		t.Errorf("findHPATarget = %q, want web-hpa", got)
	}
	if got := findHPATarget(context.Background(), cs, "ns", "Deployment", "other"); got != "" {
		t.Errorf("findHPATarget unmatched = %q, want empty", got)
	}
}

// --- buildChangeCause ---------------------------------------------

func TestBuildChangeCause(t *testing.T) {
	cases := []struct {
		name     string
		args     RollbackArgs
		contains []string
	}{
		{
			name: "with reason and actor",
			args: RollbackArgs{ToRevision: 5, Reason: "incident-2026", ActorSubject: "bob"},
			contains: []string{"revision 5", "bob", "incident-2026"},
		},
		{
			name:     "without reason",
			args:     RollbackArgs{ToRevision: 3, ActorSubject: "carol"},
			contains: []string{"revision 3", "carol"},
		},
		{
			name:     "without actor falls back to periscope",
			args:     RollbackArgs{ToRevision: 7, Reason: "rollback"},
			contains: []string{"revision 7", "periscope", "rollback"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildChangeCause(tc.args)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("change-cause = %q; missing %q", got, want)
				}
			}
		})
	}
}

func TestBuildChangeCause_TruncatesLongReason(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := buildChangeCause(RollbackArgs{ToRevision: 1, Reason: long, ActorSubject: "x"})
	if !strings.Contains(got, "…") {
		t.Errorf("expected truncation marker, got %q", got)
	}
	if len(got) > 300 {
		t.Errorf("change-cause too long: %d chars", len(got))
	}
}

// --- helpers ------------------------------------------------------

// buildRS constructs a ReplicaSet for tests.
func buildRS(
	name, ns string,
	ownerUID types.UID,
	revision string,
	hash string,
	changeCause string,
	created time.Time,
	selectorLabels map[string]string,
	template corev1.PodTemplateSpec,
) *appsv1.ReplicaSet {
	annotations := map[string]string{
		"deployment.kubernetes.io/revision": revision,
	}
	if changeCause != "" {
		annotations[changeCauseAnnotation] = changeCause
	}
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         ns,
			Annotations:       annotations,
			Labels:            map[string]string{"pod-template-hash": hash, "app": selectorLabels["app"]},
			CreationTimestamp: metav1.NewTime(created),
			OwnerReferences:   []metav1.OwnerReference{{UID: ownerUID, Kind: "Deployment"}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: selectorLabels},
			Template: template,
		},
	}
}

