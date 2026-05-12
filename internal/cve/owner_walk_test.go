package cve

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	appslisters "k8s.io/client-go/listers/apps/v1"
)

// buildRSLister returns an apps/v1 ReplicaSet lister seeded with the
// given replicasets. Uses a SharedInformer's indexer so the lister
// matches the production code path 1:1.
func buildRSLister(t *testing.T, rss ...*appsv1.ReplicaSet) appslisters.ReplicaSetLister {
	t.Helper()
	cs := fake.NewSimpleClientset()
	factory := informers.NewSharedInformerFactory(cs, 0)
	rsInf := factory.Apps().V1().ReplicaSets()
	for _, rs := range rss {
		if err := rsInf.Informer().GetIndexer().Add(rs); err != nil {
			t.Fatalf("seed indexer: %v", err)
		}
	}
	return rsInf.Lister()
}

func pod(ns, name string, owners ...metav1.OwnerReference) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, OwnerReferences: owners},
	}
}

func TestPodOwnedBy_DirectStatefulSet(t *testing.T) {
	p := pod("ns", "my-sts-0",
		metav1.OwnerReference{Kind: "StatefulSet", Name: "my-sts"})
	if !PodOwnedBy(p, "StatefulSet", "ns", "my-sts", nil) {
		t.Error("StatefulSet direct match should return true")
	}
}

func TestPodOwnedBy_DirectDaemonSet(t *testing.T) {
	p := pod("kube-system", "fluentd-abc",
		metav1.OwnerReference{Kind: "DaemonSet", Name: "fluentd"})
	if !PodOwnedBy(p, "DaemonSet", "kube-system", "fluentd", nil) {
		t.Error("DaemonSet direct match should return true")
	}
}

func TestPodOwnedBy_DirectJob(t *testing.T) {
	p := pod("ns", "batch-x", metav1.OwnerReference{Kind: "Job", Name: "batch"})
	if !PodOwnedBy(p, "Job", "ns", "batch", nil) {
		t.Error("Job direct match should return true")
	}
}

func TestPodOwnedBy_TwoHopDeployment(t *testing.T) {
	// Pod owned by ReplicaSet that's owned by Deployment.
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: "my-app-abc123",
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "my-app"}},
		},
	}
	rsLister := buildRSLister(t, rs)
	p := pod("ns", "my-app-abc123-xyz",
		metav1.OwnerReference{Kind: "ReplicaSet", Name: "my-app-abc123"})

	if !PodOwnedBy(p, "Deployment", "ns", "my-app", rsLister) {
		t.Error("two-hop Deployment match should return true")
	}
	// And the direct ReplicaSet query should still hit.
	if !PodOwnedBy(p, "ReplicaSet", "ns", "my-app-abc123", rsLister) {
		t.Error("direct ReplicaSet match should return true")
	}
}

func TestPodOwnedBy_TwoHopMissingLister_NoMatch(t *testing.T) {
	// Caller asked for Deployment but didn't pass an rsLister. The
	// direct-match branch can't see Deployment kind on the pod
	// owner, so the result must be false (and must not panic).
	p := pod("ns", "p",
		metav1.OwnerReference{Kind: "ReplicaSet", Name: "rs-1"})
	if PodOwnedBy(p, "Deployment", "ns", "my-app", nil) {
		t.Error("Deployment query without rsLister must not match")
	}
}

func TestPodOwnedBy_TwoHopWrongDeployment_NoMatch(t *testing.T) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: "other-app-xyz",
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "other-app"}},
		},
	}
	rsLister := buildRSLister(t, rs)
	p := pod("ns", "p",
		metav1.OwnerReference{Kind: "ReplicaSet", Name: "other-app-xyz"})
	if PodOwnedBy(p, "Deployment", "ns", "my-app", rsLister) {
		t.Error("different Deployment must not match")
	}
}

func TestPodOwnedBy_NamespaceMismatch(t *testing.T) {
	p := pod("other-ns", "p",
		metav1.OwnerReference{Kind: "StatefulSet", Name: "my-sts"})
	if PodOwnedBy(p, "StatefulSet", "ns", "my-sts", nil) {
		t.Error("namespace mismatch must not match")
	}
}

func TestPodOwnedBy_OwnerlessPod_NoMatch(t *testing.T) {
	p := pod("ns", "standalone")
	if PodOwnedBy(p, "Deployment", "ns", "anything", nil) {
		t.Error("ownerless pod must not match any workload")
	}
}

func TestIsSupportedWorkloadKind(t *testing.T) {
	for _, k := range []string{"Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job"} {
		if !IsSupportedWorkloadKind(k) {
			t.Errorf("%s should be supported", k)
		}
	}
	for _, k := range []string{"CronJob", "Pod", "", "deployment", "Service"} {
		if IsSupportedWorkloadKind(k) {
			t.Errorf("%q should NOT be supported", k)
		}
	}
}
