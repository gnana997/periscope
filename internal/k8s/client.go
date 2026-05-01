package k8s

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsversioned "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// newClientFn is swapped out by tests for a fake clientset.
var newClientFn = defaultNewClient

// newMetricsClientFn is swapped out by tests for a fake metrics clientset.
var newMetricsClientFn = defaultNewMetricsClient

func defaultNewClient(ctx context.Context, p credentials.Provider, c clusters.Cluster) (kubernetes.Interface, error) {
	cfg, err := buildRestConfig(ctx, p, c)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	return cs, nil
}

// NewClientset is the public, test-swappable entry point for callers
// outside this package that need a clientset. Wraps newClientFn so the
// existing test fakes flow through unchanged.
func NewClientset(ctx context.Context, p credentials.Provider, c clusters.Cluster) (kubernetes.Interface, error) {
	return newClientFn(ctx, p, c)
}

func defaultNewMetricsClient(ctx context.Context, p credentials.Provider, c clusters.Cluster) (metricsversioned.Interface, error) {
	cfg, err := buildRestConfig(ctx, p, c)
	if err != nil {
		return nil, err
	}
	mc, err := metricsversioned.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build metrics clientset: %w", err)
	}
	return mc, nil
}

// buildRestConfig produces a *rest.Config for the cluster using either the
// kubeconfig or EKS backend. Both newClientFn and newMetricsClientFn use it
// so auth logic lives in exactly one place.
func buildRestConfig(ctx context.Context, p credentials.Provider, c clusters.Cluster) (*rest.Config, error) {
	switch c.Backend {
	case clusters.BackendKubeconfig:
		return buildKubeconfigRestConfig(c)
	case clusters.BackendEKS, "":
		return buildEKSRestConfig(ctx, p, c)
	default:
		return nil, fmt.Errorf("cluster %q: unknown backend %q", c.Name, c.Backend)
	}
}

func buildEKSRestConfig(ctx context.Context, p credentials.Provider, c clusters.Cluster) (*rest.Config, error) {
	awsCfg := aws.Config{
		Region:      c.Region,
		Credentials: p,
	}
	eksClient := eks.NewFromConfig(awsCfg)

	eksName := c.EKSName()
	out, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: &eksName})
	if err != nil {
		return nil, fmt.Errorf("describe cluster %q: %w", c.Name, err)
	}
	if out.Cluster == nil || out.Cluster.Endpoint == nil ||
		out.Cluster.CertificateAuthority == nil || out.Cluster.CertificateAuthority.Data == nil {
		return nil, fmt.Errorf("cluster %q: DescribeCluster response missing endpoint or CA", c.Name)
	}

	caPEM, err := base64.StdEncoding.DecodeString(*out.Cluster.CertificateAuthority.Data)
	if err != nil {
		return nil, fmt.Errorf("decode cluster CA: %w", err)
	}

	token, err := MintEKSToken(ctx, p, c)
	if err != nil {
		return nil, err
	}

	return &rest.Config{
		Host:        *out.Cluster.Endpoint,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caPEM,
		},
	}, nil
}

func buildKubeconfigRestConfig(c clusters.Cluster) (*rest.Config, error) {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: c.KubeconfigPath},
		&clientcmd.ConfigOverrides{CurrentContext: c.KubeconfigContext},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %q: %w", c.KubeconfigPath, err)
	}
	return cfg, nil
}
