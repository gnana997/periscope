package k8s

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// newClientFn is the function used to construct a Kubernetes clientset
// for a request. Production points it at defaultNewClient. Tests swap
// it for a fake clientset.
var newClientFn = defaultNewClient

func defaultNewClient(ctx context.Context, p credentials.Provider, c clusters.Cluster) (kubernetes.Interface, error) {
	awsCfg := aws.Config{
		Region:      c.Region,
		Credentials: p,
	}
	eksClient := eks.NewFromConfig(awsCfg)

	eksName := c.EKSName()
	out, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: &eksName,
	})
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
