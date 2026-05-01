package k8s

import (
	"context"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListPodsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

func ListPods(ctx context.Context, p credentials.Provider, args ListPodsArgs) (PodList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PodList{}, fmt.Errorf("build clientset: %w", err)
	}

	raw, err := cs.CoreV1().Pods(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return PodList{}, fmt.Errorf("list pods: %w", err)
	}

	out := PodList{Pods: make([]Pod, 0, len(raw.Items))}
	for _, pod := range raw.Items {
		out.Pods = append(out.Pods, podSummary(&pod))
	}
	return out, nil
}

type GetPodArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetPod(ctx context.Context, p credentials.Provider, args GetPodArgs) (PodDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PodDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Pods(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return PodDetail{}, fmt.Errorf("get pod %s/%s: %w", args.Namespace, args.Name, err)
	}

	conds := make([]PodCondition, 0, len(raw.Status.Conditions))
	for _, c := range raw.Status.Conditions {
		conds = append(conds, PodCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	containerByName := map[string]corev1.ContainerStatus{}
	for _, cs := range raw.Status.ContainerStatuses {
		containerByName[cs.Name] = cs
	}
	initByName := map[string]corev1.ContainerStatus{}
	for _, cs := range raw.Status.InitContainerStatuses {
		initByName[cs.Name] = cs
	}

	containers := make([]ContainerStatus, 0, len(raw.Spec.Containers))
	for _, spec := range raw.Spec.Containers {
		containers = append(containers, containerStatusFor(spec, containerByName[spec.Name]))
	}
	inits := make([]ContainerStatus, 0, len(raw.Spec.InitContainers))
	for _, spec := range raw.Spec.InitContainers {
		inits = append(inits, containerStatusFor(spec, initByName[spec.Name]))
	}

	return PodDetail{
		Pod:            podSummary(raw),
		HostIP:         raw.Status.HostIP,
		QOSClass:       string(raw.Status.QOSClass),
		Conditions:     conds,
		Containers:     containers,
		InitContainers: inits,
		Labels:         raw.Labels,
		Annotations:    raw.Annotations,
	}, nil
}

func GetPodYAML(ctx context.Context, p credentials.Provider, args GetPodArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Pods(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pod %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "v1"
	raw.Kind = "Pod"
	return formatYAML(raw)
}

// podSummary builds the list-view Pod DTO from a corev1.Pod.
func podSummary(pod *corev1.Pod) Pod {
	totalContainers := len(pod.Spec.Containers)
	readyContainers := 0
	var restarts int32
	for _, cstat := range pod.Status.ContainerStatuses {
		if cstat.Ready {
			readyContainers++
		}
		restarts += cstat.RestartCount
	}
	return Pod{
		Name:      pod.Name,
		Namespace: pod.Namespace,
		Phase:     string(pod.Status.Phase),
		NodeName:  pod.Spec.NodeName,
		PodIP:     pod.Status.PodIP,
		Ready:     strconv.Itoa(readyContainers) + "/" + strconv.Itoa(totalContainers),
		Restarts:  restarts,
		CreatedAt: pod.CreationTimestamp.Time,
	}
}

func containerStatusFor(spec corev1.Container, status corev1.ContainerStatus) ContainerStatus {
	out := ContainerStatus{
		Name:         spec.Name,
		Image:        spec.Image,
		Ready:        status.Ready,
		RestartCount: status.RestartCount,
	}
	switch {
	case status.State.Running != nil:
		out.State = "Running"
	case status.State.Waiting != nil:
		out.State = "Waiting"
		out.Reason = status.State.Waiting.Reason
		out.Message = status.State.Waiting.Message
	case status.State.Terminated != nil:
		out.State = "Terminated"
		out.Reason = status.State.Terminated.Reason
		out.Message = status.State.Terminated.Message
	default:
		out.State = "Unknown"
	}
	return out
}
