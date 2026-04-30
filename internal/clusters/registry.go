package clusters

import (
	"errors"
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
// Errors on missing file, invalid YAML, missing required fields,
// malformed ARN, or duplicate cluster names.
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
		return nil, errors.New("registry contains no clusters")
	}

	byName := make(map[string]Cluster, len(f.Clusters))
	for i, c := range f.Clusters {
		if c.Name == "" {
			return nil, fmt.Errorf("cluster index %d has empty name", i)
		}
		if c.ARN == "" {
			return nil, fmt.Errorf("cluster %q has empty arn", c.Name)
		}
		if c.Region == "" {
			return nil, fmt.Errorf("cluster %q has empty region", c.Name)
		}
		if c.EKSName() == "" {
			return nil, fmt.Errorf("cluster %q has invalid EKS ARN %q (expected ':cluster/<name>')", c.Name, c.ARN)
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
