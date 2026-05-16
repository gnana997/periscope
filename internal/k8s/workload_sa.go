package k8s

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// DefaultServiceAccountName is the k8s implicit ServiceAccount name
// for pods (and pod templates) that don't declare one. Periscope
// surfaces this as a concrete name in the AWS Access UI so chains
// like "Pod → SA(default) → IRSA?" render without a blank cell.
const DefaultServiceAccountName = "default"

// WorkloadKind enumerates the kinds the AWS Access surface (#188)
// resolves to a ServiceAccount. The case-sensitive form matches
// k8s `Kind` field semantics and the SPA's tab routing param.
const (
	WorkloadKindPod            = "Pod"
	WorkloadKindServiceAccount = "ServiceAccount"
	WorkloadKindDeployment     = "Deployment"
	WorkloadKindStatefulSet    = "StatefulSet"
	WorkloadKindDaemonSet      = "DaemonSet"
)

// ErrUnknownWorkloadKind is returned by WorkloadSA when kind is
// outside the v1.1 AWS Access kind set. The HTTP handler maps to
// 400.
type ErrUnknownWorkloadKind struct{ Kind string }

func (e ErrUnknownWorkloadKind) Error() string {
	return fmt.Sprintf("unknown workload kind %q (want one of: Pod, ServiceAccount, Deployment, StatefulSet, DaemonSet)", e.Kind)
}

// WorkloadSA resolves the ServiceAccount name a workload runs as.
// For Pod, reads Pod.Spec.ServiceAccountName; for the controllers,
// reads Spec.Template.Spec.ServiceAccountName; for ServiceAccount
// kind, the name IS the SA itself (short-circuit, no API call).
//
// Empty serviceAccountName resolves to "default" (k8s implicit
// default) so callers always get a concrete name to look up in the
// SA→Role index.
//
// kind is case-sensitive (matches k8s API conventions). Unknown
// kinds return ErrUnknownWorkloadKind.
//
// k8s apierrors.IsNotFound is propagated so handlers can return a
// clean 404; other errors are wrapped with the lookup context.
func WorkloadSA(ctx context.Context, p credentials.Provider, c clusters.Cluster, kind, namespace, name string) (string, error) {
	kind = strings.TrimSpace(kind)
	namespace = strings.TrimSpace(namespace)
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("workload name is required")
	}
	if kind != WorkloadKindServiceAccount && namespace == "" {
		return "", fmt.Errorf("namespace is required for kind %q", kind)
	}

	cs, err := NewClientset(ctx, p, c)
	if err != nil {
		return "", fmt.Errorf("build k8s clientset: %w", err)
	}
	return workloadSAWithClient(ctx, cs, kind, namespace, name)
}

// workloadSAWithClient is the test seam — package tests inject a
// fake clientset rather than threading credentials.Provider through.
func workloadSAWithClient(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string) (string, error) {
	opts := metav1.GetOptions{}
	switch kind {
	case WorkloadKindServiceAccount:
		// Short-circuit: the SA's name IS the answer. The SPA's
		// validation of "does this SA exist?" happens downstream
		// at the SA→Role index lookup.
		return name, nil

	case WorkloadKindPod:
		pod, err := cs.CoreV1().Pods(namespace).Get(ctx, name, opts)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return "", err
			}
			return "", fmt.Errorf("get pod %s/%s: %w", namespace, name, err)
		}
		return defaultIfEmpty(pod.Spec.ServiceAccountName), nil

	case WorkloadKindDeployment:
		d, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, opts)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return "", err
			}
			return "", fmt.Errorf("get deployment %s/%s: %w", namespace, name, err)
		}
		return defaultIfEmpty(d.Spec.Template.Spec.ServiceAccountName), nil

	case WorkloadKindStatefulSet:
		s, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, opts)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return "", err
			}
			return "", fmt.Errorf("get statefulset %s/%s: %w", namespace, name, err)
		}
		return defaultIfEmpty(s.Spec.Template.Spec.ServiceAccountName), nil

	case WorkloadKindDaemonSet:
		d, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, opts)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return "", err
			}
			return "", fmt.Errorf("get daemonset %s/%s: %w", namespace, name, err)
		}
		return defaultIfEmpty(d.Spec.Template.Spec.ServiceAccountName), nil

	default:
		return "", ErrUnknownWorkloadKind{Kind: kind}
	}
}

func defaultIfEmpty(sa string) string {
	if strings.TrimSpace(sa) == "" {
		return DefaultServiceAccountName
	}
	return sa
}
