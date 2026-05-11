package cve

import "testing"

func TestIsECRImage(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/repo:tag", true},
		{"123456789012.dkr.ecr.us-east-1.amazonaws.com/repo@sha256:abc", true},
		{"999999999999.dkr.ecr.eu-west-2.amazonaws.com/team/app", true},
		{"nginx:latest", false},
		{"docker.io/library/nginx", false},
		{"ghcr.io/user/app:1.0", false},
		// Anti-spoof: substring elsewhere does not match.
		{"evil.com/.dkr.ecr.us-east-1.amazonaws.com/foo", false},
		{"", false},
		{"   ", false},
	}
	for _, c := range cases {
		if got := IsECRImage(c.in); got != c.want {
			t.Errorf("IsECRImage(%q): want %v, got %v", c.in, c.want, got)
		}
	}
}

func TestImageScanState(t *testing.T) {
	cases := []struct {
		image, imageID string
		wantDigest     string
		wantState      ScanState
	}{
		{
			image:      "111.dkr.ecr.us-east-1.amazonaws.com/app:v1",
			imageID:    "docker-pullable://111.dkr.ecr.us-east-1.amazonaws.com/app@sha256:abcd",
			wantDigest: "sha256:abcd",
			wantState:  ScanStateScanned,
		},
		{
			image:     "111.dkr.ecr.us-east-1.amazonaws.com/app:v1",
			imageID:   "", // ECR but pod has not yet pulled
			wantState: ScanStatePending,
		},
		{
			image:     "docker.io/library/nginx",
			imageID:   "docker-pullable://nginx@sha256:abcd",
			wantState: ScanStateNonECR,
		},
		{
			image:     "",
			imageID:   "",
			wantState: ScanStateNonECR,
		},
	}
	for i, c := range cases {
		d, s := ImageScanState(c.image, c.imageID)
		if d != c.wantDigest || s != c.wantState {
			t.Errorf("case %d (image=%q imageID=%q): want (%q,%q), got (%q,%q)",
				i, c.image, c.imageID, c.wantDigest, c.wantState, d, s)
		}
	}
}
