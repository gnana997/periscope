// EKS-side cluster metadata that the K8s clientset doesn't surface —
// specifically the support-window dates from DescribeClusterVersions.
// Lives in package k8s next to summary.go so cluster-overview + fleet
// share one collection path; both call FetchEKSClusterMetadata to get
// the EoSS / EoExt dates.
//
// AWS exposes EoSS per K8s minor version (not per cluster), so we
// extract "1.34" from the K8s server's GitVersion and look it up in
// the catalog.
package k8s

import (
	"context"
	"regexp"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// EKSClusterMetadata is the AWS-side lifecycle data for a managed
// EKS cluster. Both fields are pointer-typed: nil = AWS didn't return
// the field for this version (e.g. extended support not yet announced
// for newer minors).
type EKSClusterMetadata struct {
	EndOfStandardSupportDate *time.Time
	EndOfExtendedSupportDate *time.Time
}

// FetchEKSClusterMetadata calls eks:DescribeClusterVersions for the
// cluster's K8s minor version and returns the support-window dates.
//
// Soft-fails on any AWS error (returns zero value, nil err) so a
// missing IAM permission doesn't poison the rest of the summary —
// the EoSS chip just won't render. Required IAM action:
// eks:DescribeClusterVersions on Resource: "*".
//
// kubernetesVersion is the GitVersion from the K8s server
// (e.g. "v1.34.7-eks-abc1234"); we strip down to the "1.34" portion
// EKS uses as a version selector.
func FetchEKSClusterMetadata(ctx context.Context, p credentials.Provider, c clusters.Cluster, kubernetesVersion string) (EKSClusterMetadata, error) {
	if !c.EKSCapable() {
		return EKSClusterMetadata{}, nil
	}
	minor := extractMinorVersion(kubernetesVersion)
	if minor == "" {
		return EKSClusterMetadata{}, nil
	}

	awsCfg := aws.Config{
		Region:      c.Region,
		Credentials: p,
	}
	eksClient := eks.NewFromConfig(awsCfg)
	out, err := eksClient.DescribeClusterVersions(ctx, &eks.DescribeClusterVersionsInput{
		ClusterVersions: []string{minor},
	})
	if err != nil {
		return EKSClusterMetadata{}, nil
	}
	if len(out.ClusterVersions) == 0 {
		return EKSClusterMetadata{}, nil
	}

	info := out.ClusterVersions[0]
	return EKSClusterMetadata{
		EndOfStandardSupportDate: info.EndOfStandardSupportDate,
		EndOfExtendedSupportDate: info.EndOfExtendedSupportDate,
	}, nil
}

// minorVersionRe extracts "MAJOR.MINOR" from a K8s GitVersion.
// Examples:
//
//	"v1.34.7-eks-abc1234" → "1.34"
//	"v1.33.11"            → "1.33"
//	"1.32"                → "1.32"
var minorVersionRe = regexp.MustCompile(`^v?(\d+\.\d+)`)

func extractMinorVersion(gitVersion string) string {
	m := minorVersionRe.FindStringSubmatch(gitVersion)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}
