package main

// eks_ami_catalog.go — "what's the latest AMI for this nodegroup?"
// lookup, used by the drift fields on the nodegroup detail
// response.
//
// Two data sources, in order:
//
//   1. SSM public parameter (primary). AWS publishes recommended
//      AMI IDs and release versions under
//      /aws/service/eks/optimized-ami/<k8sVer>/<family>/recommended/{image_id,release_version}
//      and an analogous Bottlerocket tree. One GetParameter call
//      per (family, k8sVersion) gets us authoritative "latest".
//
//   2. ec2:DescribeImages (fallback). When SSM fails — denied,
//      throttled, or the parameter doesn't exist for the family in
//      this region — we filter EC2 by image name pattern and pick
//      the most recent by CreationDate. Less precise (no release
//      version) but always available.
//
// Out of scope for v1:
//   - Windows AMIs. The SSM path is wholly different (no
//     /recommended/release_version subkey) and Windows EKS is rare
//     enough we'd rather punt than half-implement. Windows
//     nodegroups get drift=uncomputed and the row shows "—".
//   - CUSTOM AMIs. AWS doesn't publish a "latest" for these — the
//     handler skips the catalog lookup entirely.
//   - Pinned-version queries (issue #1843 on awslabs/amazon-eks-ami
//     tracks AWS adding indexed-by-release-version SSM keys; not
//     yet shipped).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// LatestAMI is the result of a catalog lookup. ImageID is always
// populated; ReleaseVersion is only available from the SSM source
// (DescribeImages fallback leaves it empty).
type LatestAMI struct {
	ImageID        string
	ReleaseVersion string
	// LatestSeenAt is a best-effort freshness timestamp from whichever
	// source produced this entry. SSM path: the parameter's
	// LastModifiedDate (when AWS published a new "recommended"
	// pointer). EC2 path: the AMI's own CreationDate. They usually
	// correlate but are not the same clock — treat the value as
	// "approximately when this AMI became the latest" rather than
	// a strict release date. The Source field below distinguishes
	// the two for debugging.
	LatestSeenAt *time.Time
	// Source records which path produced the result so audit /
	// debugging can tell SSM hits from EC2 fallback at a glance.
	Source string // "ssm" | "ec2"
}

// amiCatalogAPI is the SDK seam for testability.
type amiCatalogAPI interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, opts ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error)
}

// realAMICatalogClient is a tiny wrapper over the two real SDK
// clients so the production code can satisfy amiCatalogAPI through
// a single struct.
type realAMICatalogClient struct {
	ssm *ssm.Client
	ec2 *ec2.Client
}

func (r *realAMICatalogClient) GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error) {
	return r.ssm.GetParameter(ctx, in, opts...)
}

func (r *realAMICatalogClient) DescribeImages(ctx context.Context, in *ec2.DescribeImagesInput, opts ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return r.ec2.DescribeImages(ctx, in, opts...)
}

// ec2FallbackOwners is the owner-id list passed to DescribeImages on
// the EC2 fallback path. The "amazon" alias covers the newer AL2023
// family; 602401143452 is the historical EKS-optimized AMI account
// that still owns AL2 AMIs in some commercial partitions. Both have
// to be listed because the alias does NOT include the EKS account.
// Exported for the test seam — same package, package-level so the
// test asserts the exact slice passed to the fake client.
var ec2FallbackOwners = []string{"amazon", "602401143452"}

// newAMICatalogClient is swapped by tests. Default builds the real
// SSM + EC2 clients from the request's Provider.
var newAMICatalogClient = defaultNewAMICatalogClient

func defaultNewAMICatalogClient(p credentials.Provider, c clusters.Cluster) amiCatalogAPI {
	cfg := aws.Config{Region: c.Region, Credentials: p}
	return &realAMICatalogClient{
		ssm: ssm.NewFromConfig(cfg),
		ec2: ec2.NewFromConfig(cfg),
	}
}

// ── Family mapping ──────────────────────────────────────────────────

// amiFamily captures the per-family bits the SSM path needs. The
// Tree field is "eks" or "bottlerocket" because the two top-level
// SSM trees have different child shapes (recommended vs latest,
// release_version vs image_version).
type amiFamily struct {
	// Tree is "eks" or "bottlerocket". Drives the parameter prefix
	// and the version-key choice.
	Tree string
	// Path is the family slug embedded in the SSM key. For the eks
	// tree it sits under /optimized-ami/{k8sVer}/<Path>/recommended/;
	// for bottlerocket it's "aws-k8s-{k8sVer}{Suffix}/<arch>".
	Path string
	// EC2NamePattern is the AMI name filter for the DescribeImages
	// fallback. Concrete substitutions of {k8sVer} happen at lookup
	// time. "" means we don't have a fallback for this family.
	EC2NamePattern string
}

// familyForAMIType maps an EKS AmiType enum value to the catalog
// family. Returns (zero, false) for AMIs we don't (yet) support
// drift detection on — Windows + FIPS today. CUSTOM is callable but
// the handler skips it before reaching this map.
func familyForAMIType(t ekstypes.AMITypes) (amiFamily, bool) {
	switch t {
	// Amazon Linux 2 (legacy)
	case ekstypes.AMITypesAl2X8664:
		return amiFamily{Tree: "eks", Path: "amazon-linux-2", EC2NamePattern: "amazon-eks-node-{v}-*"}, true
	case ekstypes.AMITypesAl2X8664Gpu:
		return amiFamily{Tree: "eks", Path: "amazon-linux-2-gpu", EC2NamePattern: "amazon-eks-gpu-node-{v}-*"}, true
	case ekstypes.AMITypesAl2Arm64:
		return amiFamily{Tree: "eks", Path: "amazon-linux-2-arm64", EC2NamePattern: "amazon-eks-arm64-node-{v}-*"}, true
	// Amazon Linux 2023
	case ekstypes.AMITypesAl2023X8664Standard:
		return amiFamily{Tree: "eks", Path: "amazon-linux-2023/x86_64/standard", EC2NamePattern: "amazon-eks-node-al2023-x86_64-standard-{v}-*"}, true
	case ekstypes.AMITypesAl2023Arm64Standard:
		return amiFamily{Tree: "eks", Path: "amazon-linux-2023/arm64/standard", EC2NamePattern: "amazon-eks-node-al2023-arm64-standard-{v}-*"}, true
	case ekstypes.AMITypesAl2023X8664Neuron:
		return amiFamily{Tree: "eks", Path: "amazon-linux-2023/x86_64/neuron", EC2NamePattern: "amazon-eks-node-al2023-x86_64-neuron-{v}-*"}, true
	case ekstypes.AMITypesAl2023X8664Nvidia:
		return amiFamily{Tree: "eks", Path: "amazon-linux-2023/x86_64/nvidia", EC2NamePattern: "amazon-eks-node-al2023-x86_64-nvidia-{v}-*"}, true
	case ekstypes.AMITypesAl2023Arm64Nvidia:
		return amiFamily{Tree: "eks", Path: "amazon-linux-2023/arm64/nvidia", EC2NamePattern: "amazon-eks-node-al2023-arm64-nvidia-{v}-*"}, true
	// Bottlerocket — the "aws-k8s-{k8sVer}" path; flavor encoded as
	// suffix on the k8s segment ("-nvidia" / "-fips").
	case ekstypes.AMITypesBottlerocketX8664:
		return amiFamily{Tree: "bottlerocket", Path: "x86_64", EC2NamePattern: "bottlerocket-aws-k8s-{v}-x86_64-*"}, true
	case ekstypes.AMITypesBottlerocketArm64:
		return amiFamily{Tree: "bottlerocket", Path: "arm64", EC2NamePattern: "bottlerocket-aws-k8s-{v}-aarch64-*"}, true
	case ekstypes.AMITypesBottlerocketX8664Nvidia:
		return amiFamily{Tree: "bottlerocket", Path: "x86_64", EC2NamePattern: "bottlerocket-aws-k8s-{v}-nvidia-x86_64-*"}, true
	case ekstypes.AMITypesBottlerocketArm64Nvidia:
		return amiFamily{Tree: "bottlerocket", Path: "arm64", EC2NamePattern: "bottlerocket-aws-k8s-{v}-nvidia-aarch64-*"}, true
	// Everything else — Windows variants, FIPS, future families —
	// gets drift=uncomputed in v1.
	default:
		return amiFamily{}, false
	}
}

// bottlerocketFlavorSuffix returns the "-nvidia" / "-fips" suffix
// for the bottlerocket SSM path's k8s-version segment. Pure
// function so the test can pin the matrix.
func bottlerocketFlavorSuffix(t ekstypes.AMITypes) string {
	switch t {
	case ekstypes.AMITypesBottlerocketX8664Nvidia, ekstypes.AMITypesBottlerocketArm64Nvidia:
		return "-nvidia"
	default:
		return ""
	}
}

// ── SSM-first lookup ─────────────────────────────────────────────────

// LatestForNodegroup returns the latest AMI for a nodegroup's
// (AmiType, k8sVersion) coordinates. Returns (nil, nil) when the
// family isn't supported — callers should treat that as "drift not
// computed" without erroring.
func latestForNodegroup(ctx context.Context, client amiCatalogAPI, amiType ekstypes.AMITypes, k8sVersion string) (*LatestAMI, error) {
	if k8sVersion == "" {
		return nil, nil
	}
	fam, ok := familyForAMIType(amiType)
	if !ok {
		return nil, nil
	}

	// Try SSM first. If that fails (NotFound, AccessDenied, throttled),
	// fall through to DescribeImages — both errors are logged so an
	// operator can correlate "no drift on AL2 nodegroup" with "the
	// pod's role lacks ssm:GetParameter".
	out, ssmErr := lookupSSM(ctx, client, fam, amiType, k8sVersion)
	if ssmErr == nil {
		return out, nil
	}
	slog.Warn("eks ami catalog: ssm lookup failed; trying ec2 fallback",
		"ami_type", string(amiType), "k8s", k8sVersion, "err", ssmErr)

	if fam.EC2NamePattern == "" {
		return nil, ssmErr
	}
	out, ec2Err := lookupEC2(ctx, client, fam, k8sVersion)
	if ec2Err != nil {
		return nil, fmt.Errorf("ssm: %w; ec2: %v", ssmErr, ec2Err)
	}
	return out, nil
}

// lookupSSM does two GetParameter calls (image_id + version) for
// the family. Bottlerocket uses "image_version" instead of
// "release_version"; the version-key shape is encoded in the
// family's Tree.
func lookupSSM(ctx context.Context, client amiCatalogAPI, fam amiFamily, amiType ekstypes.AMITypes, k8sVersion string) (*LatestAMI, error) {
	imageIDPath, versionPath := ssmPaths(fam, amiType, k8sVersion)
	if imageIDPath == "" {
		return nil, errors.New("no ssm path for family")
	}

	imageOut, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: &imageIDPath})
	if err != nil {
		return nil, fmt.Errorf("ssm get %s: %w", imageIDPath, err)
	}
	if imageOut.Parameter == nil || imageOut.Parameter.Value == nil {
		return nil, errors.New("ssm returned empty image_id parameter")
	}

	out := &LatestAMI{
		ImageID:     *imageOut.Parameter.Value,
		Source:      "ssm",
		LatestSeenAt: imageOut.Parameter.LastModifiedDate,
	}

	// release_version / image_version is best-effort. A missing
	// version key shouldn't fail the lookup — the image_id is
	// already useful for the SPA's "latest is ami-…" display.
	verOut, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: &versionPath})
	if err != nil {
		slog.Warn("eks ami catalog: version parameter unavailable",
			"path", versionPath, "err", err)
		return out, nil
	}
	if verOut.Parameter != nil && verOut.Parameter.Value != nil {
		out.ReleaseVersion = *verOut.Parameter.Value
	}
	return out, nil
}

// ssmPaths builds the (image_id, version) SSM parameter paths for
// the family. Pure function — exported via lowercase name for the
// test, which exercises the matrix directly.
func ssmPaths(fam amiFamily, amiType ekstypes.AMITypes, k8sVersion string) (string, string) {
	switch fam.Tree {
	case "eks":
		base := fmt.Sprintf("/aws/service/eks/optimized-ami/%s/%s/recommended", k8sVersion, fam.Path)
		return base + "/image_id", base + "/release_version"
	case "bottlerocket":
		// /aws/service/bottlerocket/aws-k8s-{k8s}{flavor}/{arch}/latest/{image_id|image_version}
		flavor := bottlerocketFlavorSuffix(amiType)
		base := fmt.Sprintf("/aws/service/bottlerocket/aws-k8s-%s%s/%s/latest", k8sVersion, flavor, fam.Path)
		return base + "/image_id", base + "/image_version"
	}
	return "", ""
}

// ── DescribeImages fallback ──────────────────────────────────────────

func lookupEC2(ctx context.Context, client amiCatalogAPI, fam amiFamily, k8sVersion string) (*LatestAMI, error) {
	pattern := strings.ReplaceAll(fam.EC2NamePattern, "{v}", k8sVersion)
	// "amazon" is the alias for canonical Amazon AMIs (newer al2023
	// family is published here); 602401143452 is the historical EKS-
	// optimized AMI account (al2 lives there in older AWS partitions).
	// Listing both keeps the fallback usable across every commercial
	// region. GovCloud / China use different account IDs and are out
	// of scope for v1 — see docs/setup/eks-upgrade-readiness.md.
	out, err := client.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: ec2FallbackOwners,
		Filters: []ec2types.Filter{
			{Name: aws.String("name"), Values: []string{pattern}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("ec2 describe-images %s: %w", pattern, err)
	}
	if len(out.Images) == 0 {
		return nil, fmt.Errorf("ec2 describe-images returned no images for %s", pattern)
	}

	// Pick the most recent by CreationDate. AWS returns RFC3339
	// strings; lex-sort works because the format is fixed-width.
	sort.SliceStable(out.Images, func(i, j int) bool {
		return strDeref(out.Images[i].CreationDate) > strDeref(out.Images[j].CreationDate)
	})
	chosen := out.Images[0]

	res := &LatestAMI{
		ImageID: strDeref(chosen.ImageId),
		Source:  "ec2",
	}
	if t, err := time.Parse(time.RFC3339, strDeref(chosen.CreationDate)); err == nil {
		res.LatestSeenAt = &t
	}
	// EKS-optimized AMI names embed the release version, e.g.
	// "amazon-eks-node-1.30-v20240819". Extract the trailing token
	// when it looks like a version-y suffix; otherwise leave empty.
	if name := strDeref(chosen.Name); name != "" {
		if rv := releaseVersionFromAMIName(name); rv != "" {
			res.ReleaseVersion = rv
		}
	}
	return res, nil
}

// releaseVersionFromAMIName parses a release version out of an EKS-
// optimized AMI name. Best-effort — returns "" if no recognizable
// suffix is present. The format varies by family:
//
//   amazon-eks-node-1.30-v20240819                 → 1.30-v20240819
//   amazon-eks-node-al2023-x86_64-standard-1.30-v20240819
//                                                  → 1.30-v20240819
//   bottlerocket-aws-k8s-1.30-x86_64-v1.20.5-…     → v1.20.5
//
// We split on "-" and look for a "vYYYYMMDD" or "vN.N.N" suffix.
func releaseVersionFromAMIName(name string) string {
	parts := strings.Split(name, "-")
	for i := len(parts) - 1; i >= 0; i-- {
		p := parts[i]
		if len(p) > 1 && p[0] == 'v' && (isDigit(p[1])) {
			// For EKS-optimized names the release version is the
			// "1.30-v20240819" pair; include the previous segment if
			// it parses as a k8s minor.
			if i > 0 && looksLikeK8sMinor(parts[i-1]) {
				return parts[i-1] + "-" + p
			}
			return p
		}
	}
	return ""
}

func looksLikeK8sMinor(s string) bool {
	// "1.30" / "1.30.0" — at least one dot, all numeric segments.
	if !strings.Contains(s, ".") {
		return false
	}
	for _, seg := range strings.Split(s, ".") {
		if seg == "" {
			return false
		}
		for _, r := range seg {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// ── Drift computation ────────────────────────────────────────────────

// driftResult is the slice the handler folds onto NodegroupSummary.
type driftResult struct {
	IsBehind             bool
	DaysBehind           int
	LatestReleaseVersion string
	LatestImageID        string
}

// computeDrift compares the nodegroup's current ReleaseVersion to
// the catalog's latest. Three outcomes:
//
//   - equal release_version: not behind. daysBehind=0.
//   - different release_version with parseable date suffixes
//     (-YYYYMMDD): daysBehind = days between dates.
//   - different release_version, dates unparseable (Bottlerocket
//     semver): isBehind=true, daysBehind=0. SPA shows "behind
//     (days unknown)".
func computeDrift(currentReleaseVersion string, latest *LatestAMI) driftResult {
	if latest == nil {
		return driftResult{}
	}
	out := driftResult{
		LatestReleaseVersion: latest.ReleaseVersion,
		LatestImageID:        latest.ImageID,
	}
	if currentReleaseVersion == "" || latest.ReleaseVersion == "" {
		// Can't compare; assume not-behind so the SPA doesn't show a
		// false alarm. Operators see "not tracked" via the
		// driftComputed=false branch instead.
		return out
	}
	if currentReleaseVersion == latest.ReleaseVersion {
		return out // not behind
	}
	out.IsBehind = true
	if d, ok := releaseVersionDateDiffDays(currentReleaseVersion, latest.ReleaseVersion); ok {
		out.DaysBehind = d
	}
	return out
}

// releaseVersionDateDiffDays parses YYYYMMDD suffixes from two EKS
// release versions and returns abs(latest - current) in whole days.
// Returns false when either side doesn't have a parseable suffix
// (Bottlerocket semver, custom names, …).
func releaseVersionDateDiffDays(current, latest string) (int, bool) {
	cd, ok := dateFromReleaseVersion(current)
	if !ok {
		return 0, false
	}
	ld, ok := dateFromReleaseVersion(latest)
	if !ok {
		return 0, false
	}
	hours := ld.Sub(cd).Hours()
	if hours < 0 {
		hours = -hours
	}
	return int(hours / 24), true
}

// dateFromReleaseVersion finds the rightmost 8-digit run preceded
// by a non-digit and parses it as YYYYMMDD. Tolerates the various
// EKS release-version shapes:
//
//   1.30.0-20240819             → 2024-08-19
//   1.30-v20240819              → 2024-08-19
//   v20240819                   → 2024-08-19
func dateFromReleaseVersion(s string) (time.Time, bool) {
	for i := len(s) - 8; i >= 0; i-- {
		seg := s[i : i+8]
		if !allDigits(seg) {
			continue
		}
		if i > 0 && isDigit(s[i-1]) {
			continue
		}
		if t, err := time.Parse("20060102", seg); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

// strDeref is a tiny helper for SDK pointer fields. We don't want
// to depend on the deref helper in eks_insights_handler.go because
// the catalog is logically a separate concern; if we move it to a
// subpackage later the helper goes with it.
func strDeref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
