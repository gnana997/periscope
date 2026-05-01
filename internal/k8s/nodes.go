package k8s

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListNodesArgs struct {
	Cluster clusters.Cluster
}

type GetNodeArgs struct {
	Cluster clusters.Cluster
	Name    string
}

func ListNodes(ctx context.Context, p credentials.Provider, args ListNodesArgs) (NodeList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return NodeList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return NodeList{}, fmt.Errorf("list nodes: %w", err)
	}
	out := NodeList{Nodes: make([]Node, 0, len(raw.Items))}
	for _, node := range raw.Items {
		out.Nodes = append(out.Nodes, nodeSummary(&node))
	}
	return out, nil
}

func GetNode(ctx context.Context, p credentials.Provider, args GetNodeArgs) (NodeDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return NodeDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().Nodes().Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return NodeDetail{}, fmt.Errorf("get node %s: %w", args.Name, err)
	}

	conds := make([]NodeCondition, 0, len(raw.Status.Conditions))
	for _, c := range raw.Status.Conditions {
		conds = append(conds, NodeCondition{
			Type:    string(c.Type),
			Status:  string(c.Status),
			Reason:  c.Reason,
			Message: c.Message,
		})
	}

	taints := make([]NodeTaint, 0, len(raw.Spec.Taints))
	for _, t := range raw.Spec.Taints {
		taints = append(taints, NodeTaint{
			Key:    t.Key,
			Value:  t.Value,
			Effect: string(t.Effect),
		})
	}

	ni := raw.Status.NodeInfo
	return NodeDetail{
		Node:        nodeSummary(raw),
		Conditions:  conds,
		Taints:      taints,
		Labels:      raw.Labels,
		Annotations: raw.Annotations,
		NodeInfo: NodeInfo{
			OSImage:          ni.OSImage,
			KernelVersion:    ni.KernelVersion,
			ContainerRuntime: ni.ContainerRuntimeVersion,
			KubeletVersion:   ni.KubeletVersion,
			KubeProxyVersion: ni.KubeProxyVersion,
		},
		CPUAllocatable:    formatCPU(raw.Status.Allocatable.Cpu().MilliValue()),
		MemoryAllocatable: formatMemory(raw.Status.Allocatable.Memory().Value()),
	}, nil
}

func nodeSummary(n *corev1.Node) Node {
	return Node{
		Name:           n.Name,
		Status:         nodeStatus(n.Status.Conditions),
		Roles:          nodeRoles(n.Labels),
		KubeletVersion: n.Status.NodeInfo.KubeletVersion,
		InternalIP:     nodeInternalIP(n.Status.Addresses),
		CPUCapacity:    formatCPU(n.Status.Capacity.Cpu().MilliValue()),
		MemoryCapacity: formatMemory(n.Status.Capacity.Memory().Value()),
		CreatedAt:      n.CreationTimestamp.Time,
	}
}

func nodeStatus(conditions []corev1.NodeCondition) string {
	for _, c := range conditions {
		if c.Type == corev1.NodeReady {
			switch c.Status {
			case corev1.ConditionTrue:
				return "Ready"
			case corev1.ConditionFalse:
				return "NotReady"
			default:
				return "Unknown"
			}
		}
	}
	return "Unknown"
}

// nodeRoles extracts role names from the node-role.kubernetes.io/<role> label prefix.
func nodeRoles(labels map[string]string) []string {
	const prefix = "node-role.kubernetes.io/"
	var roles []string
	for k := range labels {
		if strings.HasPrefix(k, prefix) {
			if role := strings.TrimPrefix(k, prefix); role != "" {
				roles = append(roles, role)
			}
		}
	}
	sort.Strings(roles)
	if len(roles) == 0 {
		return []string{"<none>"}
	}
	return roles
}

func nodeInternalIP(addresses []corev1.NodeAddress) string {
	for _, addr := range addresses {
		if addr.Type == corev1.NodeInternalIP {
			return addr.Address
		}
	}
	return ""
}
