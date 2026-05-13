package iam

import "testing"

func TestGroupByService_Empty(t *testing.T) {
	got := GroupByService(nil)
	if got == nil {
		t.Fatal("GroupByService(nil) returned nil; want non-nil empty slice for JSON marshalling")
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestGroupByService_SingleService(t *testing.T) {
	perms := []Permission{
		{Service: "s3", Action: "s3:GetObject", Resource: "*"},
		{Service: "s3", Action: "s3:ListBucket", Resource: "*"},
	}
	got := GroupByService(perms)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Service != "s3" || got[0].Count != 2 || got[0].Sensitive {
		t.Errorf("group = %+v, want service=s3 count=2 sensitive=false", got[0])
	}
}

func TestGroupByService_GroupsSortedSensitiveFirst(t *testing.T) {
	perms := []Permission{
		{Service: "s3", Action: "s3:GetObject"},
		{Service: "kms", Action: "kms:Decrypt", Sensitive: true},
		{Service: "iam", Action: "iam:GetRole"},
	}
	got := GroupByService(perms)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	// kms (sensitive) first; iam/s3 alpha after.
	if got[0].Service != "kms" {
		t.Errorf("got[0].Service = %q, want kms (sensitive first)", got[0].Service)
	}
	if got[1].Service != "iam" || got[2].Service != "s3" {
		t.Errorf("non-sensitive order = (%q,%q), want (iam,s3)", got[1].Service, got[2].Service)
	}
}

func TestGroupByService_SensitiveFlagFlagsGroup(t *testing.T) {
	perms := []Permission{
		{Service: "iam", Action: "iam:GetRole"},
		{Service: "iam", Action: "iam:PassRole", Sensitive: true, SensitiveReason: SensitivePrivEsc},
	}
	got := GroupByService(perms)
	if !got[0].Sensitive {
		t.Errorf("group.Sensitive = false, want true (any perm sensitive flags the group)")
	}
	if got[0].Count != 2 {
		t.Errorf("group.Count = %d, want 2", got[0].Count)
	}
}

func TestGroupByService_WildcardActionUsesStarKey(t *testing.T) {
	perms := []Permission{
		{Service: "", Action: "*", Sensitive: true, SensitiveReason: SensitiveWildcard, Wildcard: true},
	}
	got := GroupByService(perms)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Service != "*" {
		t.Errorf("Service = %q, want \"*\"", got[0].Service)
	}
}

func TestGroupByService_PreservesPermissionOrderWithinGroup(t *testing.T) {
	// Engine output is pre-sorted; GroupByService must not reorder
	// inside a group (sortPermissions is the source of truth for
	// within-group ordering).
	perms := []Permission{
		{Service: "s3", Action: "s3:GetObject", Resource: "*"},
		{Service: "s3", Action: "s3:ListBucket", Resource: "arn:aws:s3:::a"},
		{Service: "s3", Action: "s3:ListBucket", Resource: "arn:aws:s3:::z"},
	}
	got := GroupByService(perms)
	if len(got) != 1 || got[0].Count != 3 {
		t.Fatalf("got = %+v", got)
	}
	for i, p := range got[0].Permissions {
		if p.Action != perms[i].Action || p.Resource != perms[i].Resource {
			t.Errorf("permission[%d] reordered: got %+v, want %+v", i, p, perms[i])
		}
	}
}
