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

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// newClientFn is the function used to construct a Kubernetes clientset
// for a request. Production points it at defaultNewClient; tests swap it
// for a fake clientset.
var newClientFn = defaultNewClient

func defaultNewClient(ctx context.Context, p credentials.Provider, c clusters.Cluster) (kubernetes.Interface, error) {
	switch c.Backend {
	case clusters.BackendKubeconfig:
		return newKubeconfigClient(c)
	case clusters.BackendEKS, "":
		return newEKSClient(ctx, p, c)
	default:
		return nil, fmt.Errorf("cluster %q: unknown backend %q", c.Name, c.Backend)
	}
}

// newEKSClient builds a Kubernetes clientset by calling eks:DescribeCluster
// (using the Provider's AWS credentials) for the endpoint and CA, then
// minting a short-lived bearer token via MintEKSToken.
func newEKSClient(ctx context.Context, p credentials.Provider, c clusters.Cluster) (kubernetes.Interface, error) {
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

	cs, err := kubernetes.NewForConfig(&rest.Config{
		Host:        *out.Cluster.Endpoint,
		BearerToken: token,
		TLSClientConfig: rest.TLSClientConfig{
			CAData: caPEM,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	return cs, nil
}

// newKubeconfigClient builds a Kubernetes clientset from a kubeconfig
// file. The Provider's AWS credentials are not used here — the
// kubeconfig itself carries the auth. Useful for local dev (KIND) and
// non-AWS clusters.
func newKubeconfigClient(c clusters.Cluster) (kubernetes.Interface, error) {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: c.KubeconfigPath},
		&clientcmd.ConfigOverrides{CurrentContext: c.KubeconfigContext},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig %q: %w", c.KubeconfigPath, err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}
	return cs, nil
}
