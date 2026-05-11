package cve

import (
	corev1 "k8s.io/api/core/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"
)

// SupportedWorkloadKinds is the closed set of workload kinds the
// /cve/by-workload endpoint accepts. CronJob is intentionally
// omitted: Pod → Job → CronJob is a three-hop walk that would need a
// Job informer too, and CronJob CVE surfacing matters far less than
// the long-running Deployment / STS / DS case. Revisit in v1.2 if
// operators ask.
var SupportedWorkloadKinds = []string{
	"Deployment",
	"StatefulSet",
	"DaemonSet",
	"ReplicaSet",
	"Job",
}

// IsSupportedWorkloadKind reports whether kind is in the supported
// set. Handler-side validation gate.
func IsSupportedWorkloadKind(kind string) bool {
	for _, k := range SupportedWorkloadKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// PodOwnedBy reports whether pod is (directly or transitively) owned
// by a workload (kind, namespace, name). Walks the ownerRef chain:
//
//   - Direct: pod.ownerReferences contains a match. Covers
//     StatefulSet, DaemonSet, ReplicaSet, Job, and self-owned pods.
//
//   - Two-hop via ReplicaSet: pod is owned by a ReplicaSet R; R is
//     owned by (kind, name). Covers Deployment, since the Deployment
//     controller spawns a ReplicaSet which spawns the pods.
//
// rsLister may be nil when the caller knows kind != "Deployment"; in
// that case only the direct match is attempted, and a query for a
// Deployment returns false (with no panic).
//
// Namespace must match — owners are namespaced; a Deployment in
// `payments` cannot own a pod in `default`.
func PodOwnedBy(pod *corev1.Pod, kind, namespace, name string, rsLister appslisters.ReplicaSetLister) bool {
	if pod == nil || pod.Namespace != namespace {
		return false
	}
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == kind && ref.Name == name {
			return true
		}
		// Two-hop: pod -> ReplicaSet -> kind. Only relevant when the
		// caller is asking about a Deployment (RS direct match was
		// already covered above for kind=ReplicaSet).
		if kind == "Deployment" && ref.Kind == "ReplicaSet" && rsLister != nil {
			rs, err := rsLister.ReplicaSets(namespace).Get(ref.Name)
			if err != nil || rs == nil {
				continue
			}
			for _, rsOwner := range rs.OwnerReferences {
				if rsOwner.Kind == "Deployment" && rsOwner.Name == name {
					return true
				}
			}
		}
	}
	return false
}
