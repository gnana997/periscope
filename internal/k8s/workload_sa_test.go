package k8s

import (
	"context"
	"errors"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// podWithSA / deploymentWithSA / etc. are tiny constructors so the
// table tests below stay readable.
func podWithSA(ns, name, sa string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       corev1.PodSpec{ServiceAccountName: sa},
	}
}

func deploymentWithSA(ns, name, sa string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: sa}}},
	}
}

func statefulsetWithSA(ns, name, sa string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: sa}}},
	}
}

func daemonsetWithSA(ns, name, sa string) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       appsv1.DaemonSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{ServiceAccountName: sa}}},
	}
}

func TestWorkloadSAWithClient_Pod(t *testing.T) {
	cs := fake.NewClientset(podWithSA("ns1", "p1", "app-sa"))
	got, err := workloadSAWithClient(context.Background(), cs, WorkloadKindPod, "ns1", "p1")
	if err != nil {
		t.Fatalf("Pod: %v", err)
	}
	if got != "app-sa" {
		t.Errorf("got %q, want app-sa", got)
	}
}

func TestWorkloadSAWithClient_Pod_EmptySANormalizesToDefault(t *testing.T) {
	cs := fake.NewClientset(podWithSA("ns1", "p1", ""))
	got, err := workloadSAWithClient(context.Background(), cs, WorkloadKindPod, "ns1", "p1")
	if err != nil {
		t.Fatalf("Pod: %v", err)
	}
	if got != DefaultServiceAccountName {
		t.Errorf("got %q, want %q", got, DefaultServiceAccountName)
	}
}

func TestWorkloadSAWithClient_ServiceAccount_Shortcircuits(t *testing.T) {
	// Note: passing a clientset that has no SA objects — kind=SA
	// must NOT issue an API call, just return the name.
	cs := fake.NewClientset()
	got, err := workloadSAWithClient(context.Background(), cs, WorkloadKindServiceAccount, "ns1", "my-sa")
	if err != nil {
		t.Fatalf("SA: %v", err)
	}
	if got != "my-sa" {
		t.Errorf("got %q, want my-sa", got)
	}
}

func TestWorkloadSAWithClient_Deployment(t *testing.T) {
	cs := fake.NewClientset(deploymentWithSA("ns1", "d1", "deploy-sa"))
	got, err := workloadSAWithClient(context.Background(), cs, WorkloadKindDeployment, "ns1", "d1")
	if err != nil {
		t.Fatalf("Deployment: %v", err)
	}
	if got != "deploy-sa" {
		t.Errorf("got %q, want deploy-sa", got)
	}
}

func TestWorkloadSAWithClient_StatefulSet(t *testing.T) {
	cs := fake.NewClientset(statefulsetWithSA("ns1", "s1", "sts-sa"))
	got, err := workloadSAWithClient(context.Background(), cs, WorkloadKindStatefulSet, "ns1", "s1")
	if err != nil {
		t.Fatalf("StatefulSet: %v", err)
	}
	if got != "sts-sa" {
		t.Errorf("got %q, want sts-sa", got)
	}
}

func TestWorkloadSAWithClient_DaemonSet(t *testing.T) {
	cs := fake.NewClientset(daemonsetWithSA("ns1", "ds1", "ds-sa"))
	got, err := workloadSAWithClient(context.Background(), cs, WorkloadKindDaemonSet, "ns1", "ds1")
	if err != nil {
		t.Fatalf("DaemonSet: %v", err)
	}
	if got != "ds-sa" {
		t.Errorf("got %q, want ds-sa", got)
	}
}

func TestWorkloadSAWithClient_NotFound_Propagates(t *testing.T) {
	cs := fake.NewClientset()
	_, err := workloadSAWithClient(context.Background(), cs, WorkloadKindPod, "ns1", "missing")
	if err == nil {
		t.Fatal("want error for missing pod")
	}
	if !apierrors.IsNotFound(err) {
		t.Errorf("want apierrors.IsNotFound, got %v", err)
	}
}

func TestWorkloadSAWithClient_UnknownKind(t *testing.T) {
	cs := fake.NewClientset()
	_, err := workloadSAWithClient(context.Background(), cs, "Job", "ns1", "j1")
	if err == nil {
		t.Fatal("want ErrUnknownWorkloadKind")
	}
	var uk ErrUnknownWorkloadKind
	if !errors.As(err, &uk) {
		t.Errorf("want ErrUnknownWorkloadKind, got %T: %v", err, err)
	}
}
