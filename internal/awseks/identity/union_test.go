package identity

import (
	"reflect"
	"testing"
)

func TestUnifySARoles_IRSAOnly(t *testing.T) {
	got := UnifySARoles("c1",
		map[SAKey]string{
			{Namespace: "default", Name: "api"}: "arn:aws:iam::123:role/api-role",
		},
		nil,
		map[string]bool{
			"arn:aws:iam::123:role/api-role": true,
		},
	)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	e := got[0]
	if e.Cluster != "c1" || e.Namespace != "default" || e.SAName != "api" {
		t.Errorf("entry meta = %+v", e)
	}
	if e.DualSource {
		t.Errorf("DualSource = true, want false")
	}
	if len(e.Bindings) != 1 || e.Bindings[0].Source != SourceIRSA {
		t.Errorf("Bindings = %+v", e.Bindings)
	}
	if !e.Bindings[0].RoleExists {
		t.Errorf("RoleExists = false, want true")
	}
	if e.Bindings[0].IRSAAnnotationValue != "arn:aws:iam::123:role/api-role" {
		t.Errorf("IRSAAnnotationValue = %q", e.Bindings[0].IRSAAnnotationValue)
	}
}

func TestUnifySARoles_PodIdentityOnly(t *testing.T) {
	got := UnifySARoles("c1",
		nil,
		[]PodIdentityAssoc{
			{AssociationId: "a-123", RoleArn: "arn:aws:iam::123:role/pi-role", Namespace: "ns1", ServiceAccount: "sa1"},
		},
		map[string]bool{
			"arn:aws:iam::123:role/pi-role": true,
		},
	)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	e := got[0]
	if e.DualSource {
		t.Errorf("DualSource = true, want false")
	}
	if len(e.Bindings) != 1 || e.Bindings[0].Source != SourcePodIdentity {
		t.Errorf("Bindings = %+v", e.Bindings)
	}
	if e.Bindings[0].PodIdentityAssociationId != "a-123" {
		t.Errorf("PodIdentityAssociationId = %q", e.Bindings[0].PodIdentityAssociationId)
	}
}

func TestUnifySARoles_BothSources(t *testing.T) {
	got := UnifySARoles("c1",
		map[SAKey]string{
			{Namespace: "ns1", Name: "sa1"}: "arn:aws:iam::123:role/legacy",
		},
		[]PodIdentityAssoc{
			{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/new", Namespace: "ns1", ServiceAccount: "sa1"},
		},
		map[string]bool{
			"arn:aws:iam::123:role/legacy": true,
			"arn:aws:iam::123:role/new":    true,
		},
	)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	e := got[0]
	if !e.DualSource {
		t.Errorf("DualSource = false, want true (both sources present)")
	}
	if len(e.Bindings) != 2 {
		t.Fatalf("want 2 bindings, got %d", len(e.Bindings))
	}
	if e.Bindings[0].Source != SourceIRSA || e.Bindings[1].Source != SourcePodIdentity {
		t.Errorf("binding order = %s/%s, want IRSA then PodIdentity", e.Bindings[0].Source, e.Bindings[1].Source)
	}
}

func TestUnifySARoles_MissingRoleFlagged(t *testing.T) {
	got := UnifySARoles("c1",
		map[SAKey]string{
			{Namespace: "ns1", Name: "sa1"}: "arn:aws:iam::123:role/deleted",
		},
		nil,
		map[string]bool{
			"arn:aws:iam::123:role/deleted": false,
		},
	)
	if got[0].Bindings[0].RoleExists {
		t.Errorf("RoleExists = true, want false for deleted role")
	}
}

func TestUnifySARoles_RoleExistsMissingFromMapIsFalse(t *testing.T) {
	// If we couldn't probe iam:GetRole for any reason, default to
	// false rather than silently rendering "exists".
	got := UnifySARoles("c1",
		map[SAKey]string{
			{Namespace: "ns1", Name: "sa1"}: "arn:aws:iam::123:role/unknown",
		},
		nil,
		nil,
	)
	if got[0].Bindings[0].RoleExists {
		t.Errorf("RoleExists = true with nil roleExists map, want false")
	}
}

func TestUnifySARoles_EmptyIRSAAnnotationSkipped(t *testing.T) {
	// An SA in the irsa map with empty value (annotation absent)
	// should not produce an entry unless Pod Identity also binds it.
	got := UnifySARoles("c1",
		map[SAKey]string{
			{Namespace: "ns1", Name: "sa1"}: "",
		},
		nil,
		nil,
	)
	if len(got) != 0 {
		t.Errorf("want 0 entries for SA with no bindings, got %d", len(got))
	}
}

func TestUnifySARoles_MultiplePodIdentityPerSA(t *testing.T) {
	got := UnifySARoles("c1",
		nil,
		[]PodIdentityAssoc{
			{AssociationId: "a-1", RoleArn: "arn:aws:iam::123:role/r1", Namespace: "ns", ServiceAccount: "sa"},
			{AssociationId: "a-2", RoleArn: "arn:aws:iam::123:role/r2", Namespace: "ns", ServiceAccount: "sa"},
		},
		nil,
	)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if len(got[0].Bindings) != 2 {
		t.Errorf("want 2 bindings on one SA, got %d", len(got[0].Bindings))
	}
}

func TestUnifySARoles_StableOrder(t *testing.T) {
	got := UnifySARoles("c1",
		map[SAKey]string{
			{Namespace: "z-ns", Name: "sa"}: "arn:aws:iam::123:role/r",
			{Namespace: "a-ns", Name: "b"}:  "arn:aws:iam::123:role/r",
			{Namespace: "a-ns", Name: "a"}:  "arn:aws:iam::123:role/r",
		},
		nil,
		nil,
	)
	want := []SAKey{
		{Namespace: "a-ns", Name: "a"},
		{Namespace: "a-ns", Name: "b"},
		{Namespace: "z-ns", Name: "sa"},
	}
	for i, w := range want {
		if got[i].Namespace != w.Namespace || got[i].SAName != w.Name {
			t.Errorf("entry[%d] = (%s, %s), want (%s, %s)", i, got[i].Namespace, got[i].SAName, w.Namespace, w.Name)
		}
	}
}

func TestUnifySARoles_RoleExistsLookupCaseInsensitive(t *testing.T) {
	// The roleExists map keys are normalized ARNs (lowercase), but
	// the role ARN in the IRSA annotation may have mixed case.
	got := UnifySARoles("c1",
		map[SAKey]string{
			{Namespace: "ns", Name: "sa"}: "arn:aws:iam::123:role/MixedCase",
		},
		nil,
		map[string]bool{
			"arn:aws:iam::123:role/mixedcase": true,
		},
	)
	if !got[0].Bindings[0].RoleExists {
		t.Errorf("RoleExists = false, want true (lookup must normalize)")
	}
}

func TestGroupPodIdentityByRole(t *testing.T) {
	got := GroupPodIdentityByRole([]PodIdentityAssoc{
		{AssociationId: "a", RoleArn: "arn:aws:iam::123:role/r1", Namespace: "z", ServiceAccount: "sa"},
		{AssociationId: "b", RoleArn: "arn:aws:iam::123:role/r1", Namespace: "a", ServiceAccount: "sa1"},
		{AssociationId: "c", RoleArn: "arn:aws:iam::123:role/r1", Namespace: "a", ServiceAccount: "sa2"},
		{AssociationId: "d", RoleArn: "arn:aws:iam::123:role/r2", Namespace: "default", ServiceAccount: "x"},
	})
	if len(got) != 2 {
		t.Fatalf("want 2 role groups, got %d", len(got))
	}
	r1 := got["arn:aws:iam::123:role/r1"]
	if len(r1) != 3 {
		t.Fatalf("r1 group: want 3, got %d", len(r1))
	}
	wantOrder := []string{"a/sa1", "a/sa2", "z/sa"}
	for i, w := range wantOrder {
		key := r1[i].Namespace + "/" + r1[i].ServiceAccount
		if key != w {
			t.Errorf("r1[%d] = %q, want %q", i, key, w)
		}
	}
	r2 := got["arn:aws:iam::123:role/r2"]
	if !reflect.DeepEqual([]string{r2[0].Namespace, r2[0].ServiceAccount}, []string{"default", "x"}) {
		t.Errorf("r2[0] = %+v", r2[0])
	}
}

func TestGroupPodIdentityByRole_Empty(t *testing.T) {
	got := GroupPodIdentityByRole(nil)
	if len(got) != 0 {
		t.Errorf("want empty map, got %v", got)
	}
}
