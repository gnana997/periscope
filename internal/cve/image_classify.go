package cve

import (
	"regexp"
	"strings"
)

// ScanState classifies a single container image for the /cve/pods
// surface. The three values map 1:1 to the SPA chip states:
//
//   - ScanStateScanned: ECR image, Periscope has the digest, the
//     digest is (or will be) in the cve store.
//   - ScanStateNonECR : image is not in ECR — Inspector v2 doesn't
//     cover it, so the chip surface renders "not scanned" with a
//     docs link rather than zero counts.
//   - ScanStatePending: image is ECR but the pod's containerStatus
//     does not yet expose an imageID (pod is mid-pull, or the digest
//     entry hasn't been populated yet). Subsequent refetches will
//     resolve to scanned.
type ScanState string

const (
	ScanStateScanned ScanState = "scanned"
	ScanStateNonECR  ScanState = "non-ecr"
	ScanStatePending ScanState = "pending"
)

// ecrImagePattern matches the standard ECR image reference shape:
//
//	<acct>.dkr.ecr.<region>.amazonaws.com/<repo>[:tag|@sha256:...]
//
// The leading account id and region are anchored to host-segment
// boundaries so a hostile registry can't masquerade by including the
// substring (e.g. "evil.com/.dkr.ecr.us-east-1.amazonaws.com/foo").
var ecrImagePattern = regexp.MustCompile(`^\d+\.dkr\.ecr\.[a-z0-9-]+\.amazonaws\.com/`)

// IsECRImage reports whether image (the operator-written tag from
// spec.containers[].image) is an ECR reference. Used by the /cve/pods
// surface to bucket containers into scanned vs non-ecr without
// having to consult the digest.
func IsECRImage(image string) bool {
	return ecrImagePattern.MatchString(strings.TrimSpace(image))
}

// ImageScanState classifies a container by joining the operator-
// written image string with the runtime-resolved imageID. The digest
// return value is the bare sha256:... (empty when scanState != scanned).
func ImageScanState(image, imageID string) (digest string, state ScanState) {
	if !IsECRImage(image) {
		return "", ScanStateNonECR
	}
	d := normalizeImageID(imageID)
	if d == "" {
		return "", ScanStatePending
	}
	return d, ScanStateScanned
}
