package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	kib = 1024
	mib = 1024 * kib
	gib = 1024 * mib
)

// formatCPU converts millicore values to a human-readable string.
func formatCPU(milliCores int64) string {
	if milliCores >= 1000 {
		return fmt.Sprintf("%.2f cores", float64(milliCores)/1000)
	}
	return fmt.Sprintf("%dm", milliCores)
}

// formatMemory converts a byte count to a human-readable binary string.
func formatMemory(bytes int64) string {
	switch {
	case bytes >= gib:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(gib))
	case bytes >= mib:
		return fmt.Sprintf("%.1f MiB", float64(bytes)/float64(mib))
	case bytes >= kib:
		return fmt.Sprintf("%.1f KiB", float64(bytes)/float64(kib))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// childPodLimit caps inline child pods across all resource types.
const childPodLimit = 20

// childPodsBySelector fetches child pods using a LabelSelector (Jobs, Deployments,
// StatefulSets, DaemonSets — anything that sets spec.selector as a LabelSelector).
func childPodsBySelector(
	ctx context.Context,
	cs kubernetes.Interface,
	namespace string,
	selector *metav1.LabelSelector,
) ([]JobChildPod, error) {
	if selector == nil {
		return nil, nil
	}
	sel, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, fmt.Errorf("convert selector: %w", err)
	}
	return listChildPods(ctx, cs, namespace, sel.String())
}

// childPodsByLabelMap fetches child pods using a plain label map (Services use
// spec.selector as map[string]string rather than a LabelSelector).
func childPodsByLabelMap(
	ctx context.Context,
	cs kubernetes.Interface,
	namespace string,
	sel map[string]string,
) ([]JobChildPod, error) {
	if len(sel) == 0 {
		return nil, nil
	}
	pairs := make([]string, 0, len(sel))
	for k, v := range sel {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return listChildPods(ctx, cs, namespace, strings.Join(pairs, ","))
}

func listChildPods(ctx context.Context, cs kubernetes.Interface, namespace, labelSel string) ([]JobChildPod, error) {
	raw, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSel,
	})
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	pods := make([]JobChildPod, 0, len(raw.Items))
	for _, pod := range raw.Items {
		pods = append(pods, JobChildPod{
			Name:      pod.Name,
			Phase:     computePodStatus(&pod),
			Ready:     readyCount(&pod),
			Restarts:  totalRestarts(&pod),
			CreatedAt: pod.CreationTimestamp.Time,
		})
	}
	sort.Slice(pods, func(i, j int) bool {
		return pods[i].CreatedAt.After(pods[j].CreatedAt)
	})
	if len(pods) > childPodLimit {
		pods = pods[:childPodLimit]
	}
	return pods, nil
}

func readyCount(pod *corev1.Pod) string {
	total := len(pod.Spec.Containers)
	ready := 0
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			ready++
		}
	}
	return fmt.Sprintf("%d/%d", ready, total)
}

func totalRestarts(pod *corev1.Pod) int32 {
	var n int32
	for _, cs := range pod.Status.ContainerStatuses {
		n += cs.RestartCount
	}
	return n
}
