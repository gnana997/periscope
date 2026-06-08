package nodeshell

import (
	"testing"

	"github.com/gnana997/periscope/internal/clusters"
)

func TestResolve(t *testing.T) {
	global := Config{AWSRoleArn: "arn:global", OIDCAudience: "aud-global", Region: "us-east-1"}

	// No per-cluster override → global defaults; region falls back to cluster.
	r := global.Resolve(clusters.Cluster{Region: "eu-west-1"})
	if r.RoleArn != "arn:global" || r.OIDCAudience != "aud-global" || r.Region != "us-east-1" {
		t.Fatalf("no-override resolve = %+v", r)
	}

	// Per-cluster override wins for set fields.
	r = global.Resolve(clusters.Cluster{
		Region:    "eu-west-1",
		NodeShell: &clusters.NodeShellConfig{AWSRoleArn: "arn:acctB", Region: "eu-central-1"},
	})
	if r.RoleArn != "arn:acctB" || r.Region != "eu-central-1" || r.OIDCAudience != "aud-global" {
		t.Fatalf("override resolve = %+v", r)
	}

	// Empty global region falls back to the cluster's own region.
	r = (Config{}).Resolve(clusters.Cluster{Region: "ap-south-1"})
	if r.Region != "ap-south-1" {
		t.Fatalf("region fallback = %+v", r)
	}
}

func TestTierAllowed(t *testing.T) {
	c := Config{Tiers: []string{"admin", "maintain"}}
	if !c.TierAllowed("admin") || !c.TierAllowed("maintain") {
		t.Error("listed tiers should be allowed")
	}
	if c.TierAllowed("read") {
		t.Error("unlisted tier should be denied")
	}
}

func TestRegistryCaps(t *testing.T) {
	r := NewRegistry()
	if !r.Add(SessionRecord{ID: "1", Actor: "alice", Cluster: "prod"}) {
		t.Fatal("first add should succeed")
	}
	if r.Add(SessionRecord{ID: "1", Actor: "alice", Cluster: "prod"}) {
		t.Fatal("duplicate id should be rejected")
	}
	r.Add(SessionRecord{ID: "2", Actor: "alice", Cluster: "prod"})
	r.Add(SessionRecord{ID: "3", Actor: "bob", Cluster: "prod"})

	if got := r.CountForActor("alice"); got != 2 {
		t.Errorf("CountForActor(alice) = %d, want 2", got)
	}
	if got := r.CountForCluster("prod"); got != 3 {
		t.Errorf("CountForCluster(prod) = %d, want 3", got)
	}
	r.Remove("1")
	if got := r.CountForActor("alice"); got != 1 {
		t.Errorf("after remove, CountForActor(alice) = %d, want 1", got)
	}
}
