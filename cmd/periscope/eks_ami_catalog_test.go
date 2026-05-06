package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// fakeAMICatalogClient implements amiCatalogAPI. Per-test wiring of
// SSM and EC2 fns so each test pins exactly the failure mode it
// cares about.
type fakeAMICatalogClient struct {
	ssmFn func(ctx context.Context, in *ssm.GetParameterInput) (*ssm.GetParameterOutput, error)
	ec2Fn func(ctx context.Context, in *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error)
}

func (f *fakeAMICatalogClient) GetParameter(ctx context.Context, in *ssm.GetParameterInput, _ ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	if f.ssmFn == nil {
		return nil, errors.New("ssmFn not set")
	}
	return f.ssmFn(ctx, in)
}

func (f *fakeAMICatalogClient) DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, _ ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	if f.ec2Fn == nil {
		return nil, errors.New("ec2Fn not set")
	}
	return f.ec2Fn(ctx, in)
}

// ── SSM path mapping ─────────────────────────────────────────────────

func TestSSMPaths(t *testing.T) {
	cases := []struct {
		name        string
		amiType     ekstypes.AMITypes
		k8sVersion  string
		wantImageID string
		wantVersion string
	}{
		{
			name:        "AL2023_x86_64_STANDARD",
			amiType:     ekstypes.AMITypesAl2023X8664Standard,
			k8sVersion:  "1.30",
			wantImageID: "/aws/service/eks/optimized-ami/1.30/amazon-linux-2023/x86_64/standard/recommended/image_id",
			wantVersion: "/aws/service/eks/optimized-ami/1.30/amazon-linux-2023/x86_64/standard/recommended/release_version",
		},
		{
			name:        "AL2_x86_64",
			amiType:     ekstypes.AMITypesAl2X8664,
			k8sVersion:  "1.29",
			wantImageID: "/aws/service/eks/optimized-ami/1.29/amazon-linux-2/recommended/image_id",
			wantVersion: "/aws/service/eks/optimized-ami/1.29/amazon-linux-2/recommended/release_version",
		},
		{
			name:        "Bottlerocket_x86_64",
			amiType:     ekstypes.AMITypesBottlerocketX8664,
			k8sVersion:  "1.30",
			wantImageID: "/aws/service/bottlerocket/aws-k8s-1.30/x86_64/latest/image_id",
			wantVersion: "/aws/service/bottlerocket/aws-k8s-1.30/x86_64/latest/image_version",
		},
		{
			name:        "Bottlerocket_arm64_nvidia",
			amiType:     ekstypes.AMITypesBottlerocketArm64Nvidia,
			k8sVersion:  "1.30",
			wantImageID: "/aws/service/bottlerocket/aws-k8s-1.30-nvidia/arm64/latest/image_id",
			wantVersion: "/aws/service/bottlerocket/aws-k8s-1.30-nvidia/arm64/latest/image_version",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fam, ok := familyForAMIType(tc.amiType)
			if !ok {
				t.Fatalf("expected family for %s", tc.amiType)
			}
			gotImage, gotVersion := ssmPaths(fam, tc.amiType, tc.k8sVersion)
			if gotImage != tc.wantImageID {
				t.Errorf("image_id path = %q, want %q", gotImage, tc.wantImageID)
			}
			if gotVersion != tc.wantVersion {
				t.Errorf("version path = %q, want %q", gotVersion, tc.wantVersion)
			}
		})
	}
}

func TestFamilyForAMIType_UnsupportedReturnsFalse(t *testing.T) {
	if _, ok := familyForAMIType(ekstypes.AMITypesWindowsCore2022X8664); ok {
		t.Errorf("Windows AMIs should be unsupported in v1")
	}
	if _, ok := familyForAMIType(ekstypes.AMITypesCustom); ok {
		t.Errorf("CUSTOM AMI should not have a family entry (handler skips earlier)")
	}
}

// ── SSM lookup happy path ────────────────────────────────────────────

func TestLatestForNodegroup_SSMPathPopulatesAll(t *testing.T) {
	imageID := "ami-0abc"
	releaseVersion := "1.30.0-20240901"
	mod := time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC)
	calls := 0
	fake := &fakeAMICatalogClient{
		ssmFn: func(_ context.Context, in *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			calls++
			val := imageID
			if in.Name != nil && (lastTokenIs(*in.Name, "release_version") || lastTokenIs(*in.Name, "image_version")) {
				val = releaseVersion
			}
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{
					Name:             in.Name,
					Value:            &val,
					LastModifiedDate: &mod,
				},
			}, nil
		},
	}

	got, err := latestForNodegroup(context.Background(), fake, ekstypes.AMITypesAl2023X8664Standard, "1.30")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil {
		t.Fatalf("expected non-nil LatestAMI")
	}
	if got.ImageID != imageID || got.ReleaseVersion != releaseVersion || got.Source != "ssm" {
		t.Errorf("got = %+v", got)
	}
	if calls != 2 {
		t.Errorf("expected 2 SSM calls (image_id + version), got %d", calls)
	}
}

// release_version key missing should NOT fail the lookup — the
// image_id alone is useful, and the SPA degrades gracefully.
func TestLatestForNodegroup_VersionMissingDoesNotFail(t *testing.T) {
	imageID := "ami-0abc"
	fake := &fakeAMICatalogClient{
		ssmFn: func(_ context.Context, in *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			if in.Name != nil && lastTokenIs(*in.Name, "release_version") {
				return nil, errors.New("ParameterNotFound")
			}
			return &ssm.GetParameterOutput{
				Parameter: &ssmtypes.Parameter{Value: &imageID},
			}, nil
		},
	}
	got, err := latestForNodegroup(context.Background(), fake, ekstypes.AMITypesAl2023X8664Standard, "1.30")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil || got.ImageID != imageID || got.ReleaseVersion != "" {
		t.Errorf("got = %+v", got)
	}
}

// ── DescribeImages fallback ──────────────────────────────────────────

func TestLatestForNodegroup_FallsBackToEC2OnSSMFailure(t *testing.T) {
	creation := "2024-09-01T00:00:00.000Z"
	imageID := "ami-fallback"
	imageName := "amazon-eks-node-al2023-x86_64-standard-1.30-v20240901"
	fake := &fakeAMICatalogClient{
		ssmFn: func(_ context.Context, _ *ssm.GetParameterInput) (*ssm.GetParameterOutput, error) {
			return nil, errors.New("AccessDenied: GetParameter")
		},
		ec2Fn: func(_ context.Context, in *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
			if len(in.Owners) != 2 || in.Owners[0] != "amazon" || in.Owners[1] != "602401143452" {
				t.Errorf("expected owners=[amazon 602401143452], got %v", in.Owners)
			}
			id := imageID
			cd := creation
			n := imageName
			return &ec2.DescribeImagesOutput{Images: []ec2types.Image{
				{ImageId: &id, CreationDate: &cd, Name: &n},
			}}, nil
		},
	}

	got, err := latestForNodegroup(context.Background(), fake, ekstypes.AMITypesAl2023X8664Standard, "1.30")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got == nil || got.ImageID != imageID || got.Source != "ec2" {
		t.Errorf("got = %+v", got)
	}
	if got.ReleaseVersion != "1.30-v20240901" {
		t.Errorf("expected release version parsed from name; got %q", got.ReleaseVersion)
	}
}

// EC2 fallback picks the most recent CreationDate.
func TestLookupEC2_PicksMostRecent(t *testing.T) {
	older := "2024-08-01T00:00:00.000Z"
	newer := "2024-09-01T00:00:00.000Z"
	idA, idB := "ami-old", "ami-new"
	nameA := "amazon-eks-node-1.30-v20240801"
	nameB := "amazon-eks-node-1.30-v20240901"

	fam, _ := familyForAMIType(ekstypes.AMITypesAl2X8664)
	var capturedOwners []string
	fake := &fakeAMICatalogClient{
		ec2Fn: func(_ context.Context, in *ec2.DescribeImagesInput) (*ec2.DescribeImagesOutput, error) {
			capturedOwners = in.Owners
			// Return out-of-order to confirm the sort.
			return &ec2.DescribeImagesOutput{Images: []ec2types.Image{
				{ImageId: aws.String(idA), CreationDate: aws.String(older), Name: aws.String(nameA)},
				{ImageId: aws.String(idB), CreationDate: aws.String(newer), Name: aws.String(nameB)},
			}}, nil
		},
	}
	got, err := lookupEC2(context.Background(), fake, fam, "1.30")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.ImageID != idB {
		t.Errorf("expected newer image, got %q", got.ImageID)
	}
	// LatestSeenAt is populated from the AMI's CreationDate on the EC2
	// path; renaming PublishedAt → LatestSeenAt was a duplicate-code
	// cleanup so the field's semantics are explicit.
	if got.LatestSeenAt == nil || got.LatestSeenAt.Year() != 2024 {
		t.Errorf("LatestSeenAt = %v, want 2024-09-01-ish", got.LatestSeenAt)
	}
	// Owners must include both the "amazon" alias (covers AL2023) and
	// the historical EKS-optimized AMI account 602401143452 (covers
	// AL2 in older partitions). Without the second owner the fallback
	// silently returns zero results in those regions.
	if len(capturedOwners) != 2 || capturedOwners[0] != "amazon" || capturedOwners[1] != "602401143452" {
		t.Errorf("Owners = %v, want [amazon 602401143452]", capturedOwners)
	}
}

// Family with no EC2 pattern means an SSM failure surfaces directly.
func TestLatestForNodegroup_UnsupportedFamilyReturnsNil(t *testing.T) {
	got, err := latestForNodegroup(context.Background(), nil, ekstypes.AMITypesWindowsCore2022X8664, "1.30")
	if err != nil {
		t.Errorf("err = %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unsupported family, got %+v", got)
	}
}

// ── Drift computation ────────────────────────────────────────────────

func TestComputeDrift_EqualReleaseNotBehind(t *testing.T) {
	r := computeDrift("1.30.0-20240901", &LatestAMI{ReleaseVersion: "1.30.0-20240901"})
	if r.IsBehind || r.DaysBehind != 0 {
		t.Errorf("got = %+v", r)
	}
}

func TestComputeDrift_DifferentDateSurfacesDays(t *testing.T) {
	r := computeDrift("1.30.0-20240819", &LatestAMI{ReleaseVersion: "1.30.0-20240901"})
	if !r.IsBehind {
		t.Errorf("expected IsBehind=true")
	}
	if r.DaysBehind != 13 {
		t.Errorf("DaysBehind = %d, want 13", r.DaysBehind)
	}
}

func TestComputeDrift_BottlerocketSemverNoDays(t *testing.T) {
	// Bottlerocket image_version is semver — the date diff isn't
	// computable, so daysBehind stays 0 but IsBehind is true.
	r := computeDrift("1.20.4", &LatestAMI{ReleaseVersion: "1.20.5"})
	if !r.IsBehind {
		t.Errorf("expected IsBehind=true")
	}
	if r.DaysBehind != 0 {
		t.Errorf("DaysBehind = %d, want 0", r.DaysBehind)
	}
}

func TestComputeDrift_NilLatestReturnsZero(t *testing.T) {
	r := computeDrift("1.30.0-20240819", nil)
	if r.IsBehind || r.DaysBehind != 0 || r.LatestReleaseVersion != "" {
		t.Errorf("got = %+v", r)
	}
}

func TestComputeDrift_EmptyCurrentBailsOut(t *testing.T) {
	r := computeDrift("", &LatestAMI{ReleaseVersion: "1.30.0-20240901"})
	if r.IsBehind {
		t.Errorf("empty current shouldn't trigger IsBehind")
	}
	if r.LatestReleaseVersion != "1.30.0-20240901" {
		t.Errorf("LatestReleaseVersion = %q", r.LatestReleaseVersion)
	}
}

// ── helpers ──────────────────────────────────────────────────────────

func lastTokenIs(path, want string) bool {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[i+1:] == want
		}
	}
	return path == want
}

func TestReleaseVersionFromAMIName(t *testing.T) {
	cases := map[string]string{
		"amazon-eks-node-1.30-v20240819":                          "1.30-v20240819",
		"amazon-eks-node-al2023-x86_64-standard-1.30-v20240901":   "1.30-v20240901",
		"bottlerocket-aws-k8s-1.30-x86_64-v1.20.5-1234abcd":       "v1.20.5",
		"some-random-name-without-version":                        "",
	}
	for name, want := range cases {
		if got := releaseVersionFromAMIName(name); got != want {
			t.Errorf("%s → %q, want %q", name, got, want)
		}
	}
}
