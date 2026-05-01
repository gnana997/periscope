package k8s

import (
	"context"
	"errors"
	"fmt"
	"math"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/credentials"
)

func GetNodeMetrics(ctx context.Context, p credentials.Provider, args GetNodeArgs) (NodeMetrics, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return NodeMetrics{}, fmt.Errorf("build clientset: %w", err)
	}
	node, err := cs.CoreV1().Nodes().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return NodeMetrics{}, fmt.Errorf("get node %s: %w", args.Name, err)
	}

	mc, err := newMetricsClientFn(ctx, p, args.Cluster)
	if err != nil {
		return NodeMetrics{}, fmt.Errorf("build metrics clientset: %w", err)
	}
	raw, err := mc.MetricsV1beta1().NodeMetricses().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		if isMetricsUnavailable(err) {
			return NodeMetrics{Available: false}, nil
		}
		return NodeMetrics{}, fmt.Errorf("get node metrics %s: %w", args.Name, err)
	}

	cpuUsage := raw.Usage.Cpu().MilliValue()
	memUsage := raw.Usage.Memory().Value()
	cpuAlloc := node.Status.Allocatable.Cpu().MilliValue()
	memAlloc := node.Status.Allocatable.Memory().Value()

	return NodeMetrics{
		Available:     true,
		CPUPercent:    pct(cpuUsage, cpuAlloc),
		MemoryPercent: pct(memUsage, memAlloc),
		CPUUsage:      formatCPU(cpuUsage),
		MemoryUsage:   formatMemory(memUsage),
	}, nil
}

func GetPodMetrics(ctx context.Context, p credentials.Provider, args GetPodArgs) (PodMetrics, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PodMetrics{}, fmt.Errorf("build clientset: %w", err)
	}
	pod, err := cs.CoreV1().Pods(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return PodMetrics{}, fmt.Errorf("get pod %s/%s: %w", args.Namespace, args.Name, err)
	}

	mc, err := newMetricsClientFn(ctx, p, args.Cluster)
	if err != nil {
		return PodMetrics{}, fmt.Errorf("build metrics clientset: %w", err)
	}
	raw, err := mc.MetricsV1beta1().PodMetricses(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		if isMetricsUnavailable(err) {
			return PodMetrics{Available: false}, nil
		}
		return PodMetrics{}, fmt.Errorf("get pod metrics %s/%s: %w", args.Namespace, args.Name, err)
	}

	specByName := make(map[string]corev1.Container, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		specByName[c.Name] = c
	}

	containers := make([]ContainerMetrics, 0, len(raw.Containers))
	for _, cm := range raw.Containers {
		spec := specByName[cm.Name]
		cpuUsage := cm.Usage.Cpu().MilliValue()
		memUsage := cm.Usage.Memory().Value()

		cpuLimitPct := -1.0
		if lim := spec.Resources.Limits.Cpu(); !lim.IsZero() {
			cpuLimitPct = pct(cpuUsage, lim.MilliValue())
		}
		memLimitPct := -1.0
		if lim := spec.Resources.Limits.Memory(); !lim.IsZero() {
			memLimitPct = pct(memUsage, lim.Value())
		}

		containers = append(containers, ContainerMetrics{
			Name:            cm.Name,
			CPUUsage:        formatCPU(cpuUsage),
			MemoryUsage:     formatMemory(memUsage),
			CPULimitPercent: cpuLimitPct,
			MemLimitPercent: memLimitPct,
		})
	}

	return PodMetrics{
		Available:  true,
		Containers: containers,
	}, nil
}

// pct returns usage/total*100 rounded to one decimal, clamped to [0, 100].
func pct(usage, total int64) float64 {
	if total <= 0 {
		return 0
	}
	v := float64(usage) / float64(total) * 100
	return math.Round(v*10) / 10
}

// isMetricsUnavailable returns true when the Metrics API server is not
// installed or not yet ready — the UI should show a graceful notice
// rather than surfacing an error.
func isMetricsUnavailable(err error) bool {
	var statusErr *k8serrors.StatusError
	if errors.As(err, &statusErr) {
		code := int(statusErr.Status().Code)
		return code == 404 || code == 503
	}
	return false
}
