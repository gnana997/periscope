package k8s

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListConfigMapsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

// ListConfigMaps returns the configmaps in the target cluster. Per
// GROUND_RULES, the list view exposes only the key count — never key
// names or values. Detail view (GetConfigMap) exposes keys and values
// since ConfigMap data is config, not secret.
func ListConfigMaps(ctx context.Context, p credentials.Provider, args ListConfigMapsArgs) (ConfigMapList, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ConfigMapList{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().ConfigMaps(args.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ConfigMapList{}, fmt.Errorf("list configmaps: %w", err)
	}

	out := ConfigMapList{ConfigMaps: make([]ConfigMap, 0, len(raw.Items))}
	for _, c := range raw.Items {
		out.ConfigMaps = append(out.ConfigMaps, configMapSummary(&c))
	}
	return out, nil
}

type GetConfigMapArgs struct {
	Cluster   clusters.Cluster
	Namespace string
	Name      string
}

func GetConfigMap(ctx context.Context, p credentials.Provider, args GetConfigMapArgs) (ConfigMapDetail, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return ConfigMapDetail{}, fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().ConfigMaps(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return ConfigMapDetail{}, fmt.Errorf("get configmap %s/%s: %w", args.Namespace, args.Name, err)
	}

	binaryKeys := make([]string, 0, len(raw.BinaryData))
	for k := range raw.BinaryData {
		binaryKeys = append(binaryKeys, k)
	}
	sort.Strings(binaryKeys)

	return ConfigMapDetail{
		ConfigMap:      configMapSummary(raw),
		Data:           raw.Data,
		BinaryDataKeys: binaryKeys,
		Labels:         raw.Labels,
		Annotations:    raw.Annotations,
	}, nil
}

func GetConfigMapYAML(ctx context.Context, p credentials.Provider, args GetConfigMapArgs) (string, error) {
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return "", fmt.Errorf("build clientset: %w", err)
	}
	raw, err := cs.CoreV1().ConfigMaps(args.Namespace).Get(ctx, args.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get configmap %s/%s: %w", args.Namespace, args.Name, err)
	}
	raw.APIVersion = "v1"
	raw.Kind = "ConfigMap"
	return formatYAML(raw)
}

func configMapSummary(c *corev1.ConfigMap) ConfigMap {
	return ConfigMap{
		Name:      c.Name,
		Namespace: c.Namespace,
		KeyCount:  len(c.Data) + len(c.BinaryData),
		CreatedAt: c.CreationTimestamp.Time,
	}
}
