package k8s

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

type ListConfigMapsArgs struct {
	Cluster   clusters.Cluster
	Namespace string
}

// ListConfigMaps returns the configmaps in the target cluster. Per the
// secrets-redaction principle in GROUND_RULES, the list view exposes
// only the key count — never the key names or values. Key names land in
// the read (Get) view (v1.1+); values never appear in v1.
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
		out.ConfigMaps = append(out.ConfigMaps, ConfigMap{
			Name:      c.Name,
			Namespace: c.Namespace,
			KeyCount:  len(c.Data) + len(c.BinaryData),
			CreatedAt: c.CreationTimestamp.Time,
		})
	}
	return out, nil
}
