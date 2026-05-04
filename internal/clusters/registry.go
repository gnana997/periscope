package clusters

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Registry is the in-memory list of clusters loaded once at startup.
type Registry struct {
	clusters []Cluster
	byName   map[string]Cluster
}

type registryFile struct {
	Clusters []Cluster `yaml:"clusters"`
}

// Empty returns a Registry with no clusters. Used when the operator
// hasn't configured a registry file yet — the dashboard runs but has
// nothing to display.
func Empty() *Registry {
	return &Registry{byName: map[string]Cluster{}}
}

// LoadFromFile reads the registry YAML at path and returns a Registry.
// Errors on missing file, invalid YAML, missing required fields per
// backend, malformed ARN, unknown backend, or duplicate cluster names.
//
// Backend defaults to "eks" when omitted, for backward compatibility.
func LoadFromFile(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read registry %q: %w", path, err)
	}

	var f registryFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse registry %q: %w", path, err)
	}

	if len(f.Clusters) == 0 {
		// Empty registry is OK — the dashboard boots, the SPA renders
		// a friendly "no clusters configured" state. Fatal-on-empty
		// was the wrong default; it blocked first-install smoke tests.
		return Empty(), nil
	}

	byName := make(map[string]Cluster, len(f.Clusters))
	for i, c := range f.Clusters {
		if c.Name == "" {
			return nil, fmt.Errorf("cluster index %d has empty name", i)
		}
		if c.Backend == "" {
			c.Backend = BackendEKS
			f.Clusters[i] = c
		}

		switch c.Backend {
		case BackendEKS:
			if c.ARN == "" {
				return nil, fmt.Errorf("cluster %q (eks): empty arn", c.Name)
			}
			if c.Region == "" {
				return nil, fmt.Errorf("cluster %q (eks): empty region", c.Name)
			}
			if c.EKSName() == "" {
				return nil, fmt.Errorf("cluster %q (eks): invalid ARN %q (expected ':cluster/<name>')", c.Name, c.ARN)
			}
		case BackendKubeconfig:
			if c.KubeconfigPath == "" {
				return nil, fmt.Errorf("cluster %q (kubeconfig): empty kubeconfigPath", c.Name)
			}
		case BackendInCluster:
			// No required fields. The credential source is the pod's
			// in-cluster ServiceAccount, mounted by the kubelet at
			// /var/run/secrets/kubernetes.io/serviceaccount/.
		case BackendAgent:
			// No required fields here. The agent registers itself at
			// runtime; the registry just records that this cluster name
			// will be served by a tunnel session. internal/k8s/client.go
			// resolves the live session per-request via the tunnel
			// lookup hook (see #42 / buildAgentRestConfig).
		default:
			return nil, fmt.Errorf("cluster %q: unknown backend %q (must be %q, %q, %q, or %q)",
				c.Name, c.Backend, BackendEKS, BackendKubeconfig, BackendInCluster, BackendAgent)
		}

		if _, dup := byName[c.Name]; dup {
			return nil, fmt.Errorf("duplicate cluster name %q", c.Name)
		}
		byName[c.Name] = c
	}

	return &Registry{
		clusters: f.Clusters,
		byName:   byName,
	}, nil
}

// List returns the clusters in registry order.
func (r *Registry) List() []Cluster {
	out := make([]Cluster, len(r.clusters))
	copy(out, r.clusters)
	return out
}

// ByName returns the cluster with the given Name, or false if not found.
func (r *Registry) ByName(name string) (Cluster, bool) {
	c, ok := r.byName[name]
	return c, ok
}
