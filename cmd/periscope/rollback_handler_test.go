package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// rbSwapClient swaps the package-level newClientFn in internal/k8s
// with one returning the supplied fake clientset. Mirrors swapClient
// in internal/k8s/rollback_test.go — duplicated here because the
// var is package-private.
//
// We round-trip through k8s.NewClientset by overriding the test seam
// the package already exposes for fakes (existing apply / list tests
// rely on this).
func rbSwapClient(t *testing.T, cs kubernetes.Interface) {
	t.Helper()
	orig := k8s.SetNewClientFnForTest(cs)
	t.Cleanup(orig)
}

// rbRegistry returns a Registry with one cluster named "test" — the
// rollback handler's URL pattern uses {cluster}=test. Reuses the shared
// testRegistry helper for consistent setup across handler tests.
func rbRegistry(t *testing.T) *clusters.Registry {
	t.Helper()
	return testRegistry(t)
}

func rbBuildDeployment() (*appsv1.Deployment, *appsv1.ReplicaSet, *appsv1.ReplicaSet) {
	depUID := types.UID("dep-uid")
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "ns", UID: depUID,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
		},
		Spec: appsv1.DeploymentSpec{Selector: selector},
	}
	mkRS := func(name, rev, hash, image string) *appsv1.ReplicaSet {
		return &appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: "ns",
				Annotations: map[string]string{"deployment.kubernetes.io/revision": rev},
				Labels:      map[string]string{"app": "web", "pod-template-hash": hash},
				CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
				OwnerReferences: []metav1.OwnerReference{{UID: depUID, Kind: "Deployment"}},
			},
			Spec: appsv1.ReplicaSetSpec{
				Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web", "pod-template-hash": hash}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}},
				},
			},
		}
	}
	return dep, mkRS("web-old", "1", "h1", "app:v1"), mkRS("web-cur", "2", "h2", "app:v2")
}

func TestListRevisionsHandler_Deployment_HappyPath(t *testing.T) {
	dep, rsOld, rsCur := rbBuildDeployment()
	rbSwapClient(t, fake.NewSimpleClientset(dep, rsOld, rsCur))
	reg := rbRegistry(t)

	rec, _ := invokeAuthenticated(t,
		func(*audit.Emitter) credentials.Handler { return listRevisionsHandler(reg) },
		http.MethodGet, "/api/clusters/test/deployments/ns/web/revisions",
		map[string]string{"cluster": "test", "kind": "deployments", "ns": "ns", "name": "web"},
		nil,
	)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp k8s.RevisionHistory
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.CurrentRevision != 2 {
		t.Errorf("currentRevision = %d, want 2", resp.CurrentRevision)
	}
	if len(resp.Revisions) != 2 {
		t.Errorf("revisions len = %d, want 2", len(resp.Revisions))
	}
}

func TestListRevisionsHandler_UnsupportedKind(t *testing.T) {
	reg := rbRegistry(t)
	rec, _ := invokeAuthenticated(t,
		func(*audit.Emitter) credentials.Handler { return listRevisionsHandler(reg) },
		http.MethodGet, "/api/clusters/test/pods/ns/p/revisions",
		map[string]string{"cluster": "test", "kind": "pods", "ns": "ns", "name": "p"},
		nil,
	)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRollbackHandler_HappyPath_EmitsIntentAndOutcome(t *testing.T) {
	dep, rsOld, rsCur := rbBuildDeployment()
	rbSwapClient(t, fake.NewSimpleClientset(dep, rsOld, rsCur))
	reg := rbRegistry(t)

	body := []byte(`{"revision": 1, "reason": "OOMKill on v2"}`)
	rec, sink := invokeAuthenticated(t,
		func(e *audit.Emitter) credentials.Handler { return rollbackHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/deployments/ns/web/rollback",
		map[string]string{"cluster": "test", "kind": "deployments", "ns": "ns", "name": "web"},
		body,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit emitted %d rows, want 2 (intent + outcome); events=%+v", len(events), events)
	}
	if events[0].Verb != audit.VerbRollbackIntent {
		t.Errorf("events[0].Verb = %q, want %q", events[0].Verb, audit.VerbRollbackIntent)
	}
	if events[0].Outcome != audit.OutcomeSuccess {
		t.Errorf("events[0].Outcome = %q, want success", events[0].Outcome)
	}
	if events[1].Verb != audit.VerbRollback {
		t.Errorf("events[1].Verb = %q, want %q", events[1].Verb, audit.VerbRollback)
	}
	if events[1].Outcome != audit.OutcomeSuccess {
		t.Errorf("events[1].Outcome = %q, want success", events[1].Outcome)
	}
	if got := events[1].Extra["toRevision"]; got != int64(1) {
		t.Errorf("Extra.toRevision = %v, want 1", got)
	}
	if got := events[1].Extra["reason"]; got != "OOMKill on v2" {
		t.Errorf("Extra.reason = %v, want %q", got, "OOMKill on v2")
	}
}

func TestRollbackHandler_RevisionNotFound_EmitsFailureRow(t *testing.T) {
	dep, rsOld, rsCur := rbBuildDeployment()
	rbSwapClient(t, fake.NewSimpleClientset(dep, rsOld, rsCur))
	reg := rbRegistry(t)

	body := []byte(`{"revision": 99}`)
	rec, sink := invokeAuthenticated(t,
		func(e *audit.Emitter) credentials.Handler { return rollbackHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/deployments/ns/web/rollback",
		map[string]string{"cluster": "test", "kind": "deployments", "ns": "ns", "name": "web"},
		body,
	)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	events := sink.snapshot()
	if len(events) != 2 {
		t.Fatalf("audit emitted %d rows, want 2", len(events))
	}
	if events[1].Outcome == audit.OutcomeSuccess {
		t.Errorf("outcome row should be non-success on revision-not-found, got %q", events[1].Outcome)
	}
}

func TestRollbackHandler_PausedDeployment_Returns409(t *testing.T) {
	depUID := types.UID("dep-uid")
	selector := &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: "ns", UID: depUID,
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "2"},
		},
		Spec: appsv1.DeploymentSpec{Selector: selector, Paused: true},
	}
	rsOld := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web-old", Namespace: "ns",
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"},
			Labels:      map[string]string{"app": "web", "pod-template-hash": "h1"},
			OwnerReferences: []metav1.OwnerReference{{UID: depUID, Kind: "Deployment"}},
		},
		Spec: appsv1.ReplicaSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "web"}},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:v1"}}},
			},
		},
	}
	rbSwapClient(t, fake.NewSimpleClientset(dep, rsOld))
	reg := rbRegistry(t)

	body := []byte(`{"revision": 1}`)
	rec, _ := invokeAuthenticated(t,
		func(e *audit.Emitter) credentials.Handler { return rollbackHandler(reg, e) },
		http.MethodPost, "/api/clusters/test/deployments/ns/web/rollback",
		map[string]string{"cluster": "test", "kind": "deployments", "ns": "ns", "name": "web"},
		body,
	)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestRollbackHandler_BadBody_Returns400(t *testing.T) {
	reg := rbRegistry(t)
	cases := []struct {
		name string
		body []byte
	}{
		{name: "not json", body: []byte("not json")},
		{name: "missing revision", body: []byte(`{"reason":"x"}`)},
		{name: "negative revision", body: []byte(`{"revision":-1}`)},
		{name: "zero revision", body: []byte(`{"revision":0}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, _ := invokeAuthenticated(t,
				func(e *audit.Emitter) credentials.Handler { return rollbackHandler(reg, e) },
				http.MethodPost, "/api/clusters/test/deployments/ns/web/rollback",
				map[string]string{"cluster": "test", "kind": "deployments", "ns": "ns", "name": "web"},
				tc.body,
			)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestRollbackHandler_UnknownCluster_Returns404(t *testing.T) {
	reg := rbRegistry(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/clusters/missing/deployments/ns/web/rollback", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", "missing")
	rctx.URLParams.Add("kind", "deployments")
	rctx.URLParams.Add("ns", "ns")
	rctx.URLParams.Add("name", "web")
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	r = r.WithContext(credentials.WithSession(r.Context(), defaultTestSession))
	rollbackHandler(reg, audit.New())(rec, r, defaultTestProvider())
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
