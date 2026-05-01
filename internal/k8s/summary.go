package k8s

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type GetClusterSummaryArgs struct {
	Cluster clusters.Cluster
}

func GetClusterSummary(ctx context.Context, p credentials.Provider, args GetClusterSummaryArgs) (ClusterSummary, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ClusterSummary{}, fmt.Errorf("build clientset: %w", err)
	}

	// K8s server version
	serverVersion, err := cs.Discovery().ServerVersion()
	if err != nil {
		return ClusterSummary{}, fmt.Errorf("get server version: %w", err)
	}

	// Nodes — count + sum allocatable
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSummary{}, fmt.Errorf("list nodes: %w", err)
	}
	var totalCPUMillis, totalMemBytes int64
	readyCount := 0
	for _, n := range nodes.Items {
		if nodeStatus(n.Status.Conditions) == "Ready" {
			readyCount++
		}
		totalCPUMillis += n.Status.Allocatable.Cpu().MilliValue()
		totalMemBytes += n.Status.Allocatable.Memory().Value()
	}

	// Pods across all namespaces — count only
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSummary{}, fmt.Errorf("list pods: %w", err)
	}

	// Namespaces — count only
	nsList, err := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ClusterSummary{}, fmt.Errorf("list namespaces: %w", err)
	}

	provider := providerLabel(args.Cluster.Backend)

	summary := ClusterSummary{
		KubernetesVersion: serverVersion.GitVersion,
		Provider:          provider,
		NodeCount:         len(nodes.Items),
		NodeReadyCount:    readyCount,
		PodCount:          len(pods.Items),
		NamespaceCount:    len(nsList.Items),
		CPUAllocatable:    formatCPU(totalCPUMillis),
		MemoryAllocatable: formatMemory(totalMemBytes),
	}

	// Cluster-wide metrics (optional — gracefully degrade if metrics-server absent)
	mc, err := newMetricsClientFn(ctx, p, args.Cluster)
	if err != nil {
		return summary, nil
	}
	allMetrics, err := mc.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		if isMetricsUnavailable(err) {
			summary.MetricsAvailable = false
			return summary, nil
		}
		return summary, nil // any other metrics error: degrade silently
	}

	var usedCPUMillis, usedMemBytes int64
	for _, nm := range allMetrics.Items {
		usedCPUMillis += nm.Usage.Cpu().MilliValue()
		usedMemBytes += nm.Usage.Memory().Value()
	}
	summary.MetricsAvailable = true
	summary.CPUUsed = formatCPU(usedCPUMillis)
	summary.MemoryUsed = formatMemory(usedMemBytes)
	if totalCPUMillis > 0 {
		summary.CPUPercent = pct(usedCPUMillis, totalCPUMillis)
	}
	if totalMemBytes > 0 {
		summary.MemoryPercent = pct(usedMemBytes, totalMemBytes)
	}

	return summary, nil
}

func providerLabel(backend string) string {
	switch strings.ToLower(backend) {
	case clusters.BackendKubeconfig:
		return "Kubeconfig"
	default:
		return "EKS"
	}
}
