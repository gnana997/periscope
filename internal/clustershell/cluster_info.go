package clustershell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/gnana997/periscope/internal/clusters"
)

// ClusterInfoNamespace and ClusterInfoName name the well-known
// ConfigMap most Kubernetes distributions expose to publish their
// apiserver URL + CA. kubeadm creates it by default; managed
// services (EKS, GKE, AKS) ship it. We use it for the agent backend
// because the agent doesn't currently transmit the cluster's
// apiserver CA, and adding a one-shot registration-time CA upload
// would require a protocol change every existing agent would need
// to roll forward through.
//
// If a cluster's kube-public/cluster-info ConfigMap is missing the
// CA, the handler returns E_CLUSTER_SHELL_NO_CA and the operator
// has two paths: populate cluster-info, or disable cluster-shell
// for that cluster in the Helm values.
const (
	ClusterInfoNamespace = "kube-public"
	ClusterInfoName      = "cluster-info"
	clusterInfoDataKey   = "kubeconfig"
)

// ErrNoClusterCA is the typed error CAReader returns when it can't
// find the apiserver CA for an agent-backed cluster. The handler
// catches this and surfaces E_CLUSTER_SHELL_NO_CA to the SPA so the
// operator gets a doc-linked error instead of a generic 500.
var ErrNoClusterCA = errors.New("cluster-shell: apiserver CA unavailable")

// CAReader is the shared cache for per-cluster apiserver CAs. One
// instance is held by the WS handler and threaded into every Session
// — same Reader across sessions so the cluster-info read happens
// once per cluster per Periscope-main lifetime.
//
// Lifetime: per-Periscope-main process. The cache is dropped on
// pod restart; the next session for each cluster re-reads. Worth
// upgrading to a persistent cache (a ConfigMap or in-memory store
// that survives rolling deploys) only if operators report perceptible
// session-start latency on the very first session after rollout.
type CAReader struct {
	mu    sync.RWMutex
	cache map[string][]byte
}

// NewCAReader returns an empty CAReader.
func NewCAReader() *CAReader {
	return &CAReader{cache: make(map[string][]byte)}
}

// Read returns the apiserver CA cert for the target cluster,
// dispatching on backend. Result is cached per-cluster-name after
// the first successful read.
//
// Returns ErrNoClusterCA (wrapped) when:
//   - BackendAgent: kube-public/cluster-info is missing or has no
//     CA in its embedded kubeconfig
//   - BackendInCluster: the in-cluster CA file isn't readable
//     (shouldn't happen on a healthy pod, but treat as the same
//     error class)
//   - Backend is anything else (cluster-shell is in-cluster +
//     agent only in v1.2)
func (r *CAReader) Read(ctx context.Context, cs kubernetes.Interface, cluster clusters.Cluster) ([]byte, error) {
	r.mu.RLock()
	cached, ok := r.cache[cluster.Name]
	r.mu.RUnlock()
	if ok {
		return cached, nil
	}

	var (
		ca  []byte
		err error
	)
	switch cluster.Backend {
	case clusters.BackendInCluster:
		// Periscope main's own pod has the in-cluster CA mounted.
		// Reading the file is free; skips a per-cluster apiserver
		// round-trip we'd otherwise make to kube-public/cluster-info.
		ca, err = os.ReadFile(inClusterCAFile)
		if err != nil {
			return nil, fmt.Errorf("%w: read in-cluster CA at %s: %w", ErrNoClusterCA, inClusterCAFile, err)
		}
	case clusters.BackendAgent:
		ca, err = readClusterInfoCA(ctx, cs)
		if err != nil {
			return nil, fmt.Errorf("%w: cluster %s (agent backend): %w", ErrNoClusterCA, cluster.Name, err)
		}
	default:
		return nil, fmt.Errorf("cluster-shell does not support backend %q (in-cluster and agent only in v1.2)", cluster.Backend)
	}

	r.mu.Lock()
	r.cache[cluster.Name] = ca
	r.mu.Unlock()
	return ca, nil
}

// Invalidate drops the cached CA for a cluster. The handler may call
// this on a CA-related kubectl failure to force a re-read on the
// next session — covers rotation events without requiring a Periscope
// restart.
func (r *CAReader) Invalidate(clusterName string) {
	r.mu.Lock()
	delete(r.cache, clusterName)
	r.mu.Unlock()
}

// readClusterInfoCA reads kube-public/cluster-info via the supplied
// clientset (tunnel-routed for agent backends) and parses the
// embedded kubeconfig for the CA. Returns an error if the ConfigMap
// is missing, malformed, or doesn't contain a CA.
//
// The cluster-info ConfigMap's "kubeconfig" key holds a stripped-down
// kubeconfig that kubeadm and other distributions publish so any
// client can bootstrap a TLS-verified connection. Standard format;
// we parse it with the standard library rather than reimplementing
// the YAML walk.
func readClusterInfoCA(ctx context.Context, cs kubernetes.Interface) ([]byte, error) {
	cm, err := cs.CoreV1().ConfigMaps(ClusterInfoNamespace).Get(ctx, ClusterInfoName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get %s/%s ConfigMap: %w", ClusterInfoNamespace, ClusterInfoName, err)
	}
	raw, ok := cm.Data[clusterInfoDataKey]
	if !ok || raw == "" {
		return nil, fmt.Errorf("%s/%s ConfigMap missing %q data key", ClusterInfoNamespace, ClusterInfoName, clusterInfoDataKey)
	}

	kc, err := clientcmd.Load([]byte(raw))
	if err != nil {
		return nil, fmt.Errorf("parse %s/%s kubeconfig payload: %w", ClusterInfoNamespace, ClusterInfoName, err)
	}
	for _, cluster := range kc.Clusters {
		if len(cluster.CertificateAuthorityData) > 0 {
			return cluster.CertificateAuthorityData, nil
		}
	}
	return nil, fmt.Errorf("%s/%s kubeconfig has no clusters carrying CertificateAuthorityData", ClusterInfoNamespace, ClusterInfoName)
}
