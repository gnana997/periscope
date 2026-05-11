package cve

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/awsec2"
)

func TestResolve_ManagedNodegroup(t *testing.T) {
	r := &OwnerResolver{}
	kinds, names, err := r.Resolve(context.Background(), []awsec2.InstanceMeta{
		{InstanceID: "i-1", Tags: map[string]string{"eks:nodegroup-name": "ng-prod"}},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if kinds["i-1"] != OwnerManagedNodegroup || names["i-1"] != "ng-prod" {
		t.Errorf("got kind=%q name=%q", kinds["i-1"], names["i-1"])
	}
}

func TestResolve_Karpenter_WithNameLookup(t *testing.T) {
	r := &OwnerResolver{
		getCS: func(_ context.Context) (kubernetes.Interface, error) { return nil, nil },
		listFn: func(_ context.Context, _ kubernetes.Interface) ([]nodeClaimRef, error) {
			return []nodeClaimRef{
				{Name: "my-claim", ProviderID: "aws:///us-east-1a/i-2"},
			}, nil
		},
	}
	kinds, names, err := r.Resolve(context.Background(), []awsec2.InstanceMeta{
		{InstanceID: "i-2", Tags: map[string]string{"karpenter.sh/nodepool": "default"}},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if kinds["i-2"] != OwnerKarpenter || names["i-2"] != "my-claim" {
		t.Errorf("got kind=%q name=%q", kinds["i-2"], names["i-2"])
	}
}

func TestResolve_Karpenter_NameLookupBestEffort(t *testing.T) {
	// Karpenter classification must succeed even when the K8s
	// list fails — the kind is correct, name is empty.
	r := &OwnerResolver{
		getCS: func(_ context.Context) (kubernetes.Interface, error) { return nil, nil },
		listFn: func(_ context.Context, _ kubernetes.Interface) ([]nodeClaimRef, error) {
			return nil, nil
		},
	}
	kinds, names, _ := r.Resolve(context.Background(), []awsec2.InstanceMeta{
		{InstanceID: "i-3", Tags: map[string]string{"karpenter.sh/nodepool": "default"}},
	})
	if kinds["i-3"] != OwnerKarpenter {
		t.Errorf("kind: want %q, got %q", OwnerKarpenter, kinds["i-3"])
	}
	if names["i-3"] != "" {
		t.Errorf("name: want empty, got %q", names["i-3"])
	}
}

func TestResolve_Unmanaged(t *testing.T) {
	r := &OwnerResolver{}
	kinds, _, _ := r.Resolve(context.Background(), []awsec2.InstanceMeta{
		{InstanceID: "i-4", Tags: map[string]string{"Name": "manual"}},
	})
	if kinds["i-4"] != OwnerUnmanaged {
		t.Errorf("kind: want %q, got %q", OwnerUnmanaged, kinds["i-4"])
	}
}

func TestInstanceIDFromProviderID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"aws:///us-east-1a/i-0abcdef0123456789", "i-0abcdef0123456789"},
		{"aws:///us-east-1a/i-abc", "i-abc"},
		{"aws:///i-noaz", "i-noaz"},
		{"bogus", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := instanceIDFromProviderID(c.in); got != c.want {
			t.Errorf("instanceIDFromProviderID(%q): want %q, got %q", c.in, c.want, got)
		}
	}
}

func TestNormalizeImageID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"docker-pullable://acct.dkr.ecr.us-east-1.amazonaws.com/app@sha256:abcd", "sha256:abcd"},
		{"docker://sha256:abcd", "sha256:abcd"},
		{"sha256:abcd", "sha256:abcd"},
		{"", ""},
		{"docker-pullable://nginx:latest", ""}, // no digest, must drop
		{"weird-no-prefix", ""},
	}
	for _, c := range cases {
		if got := normalizeImageID(c.in); got != c.want {
			t.Errorf("normalizeImageID(%q): want %q, got %q", c.in, c.want, got)
		}
	}
}

func TestPodImageDigests_StatusBeatsSpec(t *testing.T) {
	// Even if spec.containers[].image is a tag, we extract from
	// status.containerStatuses[].imageID — the digest. The whole
	// point of the watch predicate is that the tag can be stable
	// while the resolved digest churns.
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "acct/app:latest"},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", Image: "acct/app:latest", ImageID: "docker-pullable://acct/app@sha256:aaaa"},
			},
			InitContainerStatuses: []corev1.ContainerStatus{
				{Name: "init", ImageID: "docker-pullable://acct/init@sha256:bbbb"},
			},
		},
	}
	got := PodImageDigests(pod)
	if len(got) != 2 || got[0] != "sha256:aaaa" || got[1] != "sha256:bbbb" {
		t.Errorf("digests: %v", got)
	}
}

func TestPodImageDigests_NilPod(t *testing.T) {
	if got := PodImageDigests(nil); got != nil {
		t.Errorf("nil pod: want nil, got %v", got)
	}
}
