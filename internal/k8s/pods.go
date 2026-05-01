package k8s

import (
	"context"
	"fmt"
	"strconv"

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
		totalContainers := len(pod.Spec.Containers)
		readyContainers := 0
		var restarts int32
		for _, cstat := range pod.Status.ContainerStatuses {
			if cstat.Ready {
				readyContainers++
			}
			restarts += cstat.RestartCount
		}

		out.Pods = append(out.Pods, Pod{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Phase:     string(pod.Status.Phase),
			NodeName:  pod.Spec.NodeName,
			PodIP:     pod.Status.PodIP,
			Ready:     strconv.Itoa(readyContainers) + "/" + strconv.Itoa(totalContainers),
			Restarts:  restarts,
			CreatedAt: pod.CreationTimestamp.Time,
		})
	}
	return out, nil
}
