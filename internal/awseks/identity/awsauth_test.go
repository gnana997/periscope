package identity

import (
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newConfigMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: AwsAuthConfigMapNamespace,
			Name:      AwsAuthConfigMapName,
		},
		Data: data,
	}
}

func TestParseAwsAuth_NilConfigMap(t *testing.T) {
	got, err := ParseAwsAuth(nil)
	if err != nil {
		t.Fatalf("nil ConfigMap: unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("nil ConfigMap: want empty, got %d entries", len(got))
	}
}

func TestParseAwsAuth_EmptyData(t *testing.T) {
	got, err := ParseAwsAuth(newConfigMap(nil))
	if err != nil {
		t.Fatalf("nil data: unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("nil data: want empty, got %d entries", len(got))
	}

	got, err = ParseAwsAuth(newConfigMap(map[string]string{}))
	if err != nil {
		t.Fatalf("empty data: unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty data: want empty, got %d entries", len(got))
	}
}

func TestParseAwsAuth_RolesOnly(t *testing.T) {
	cm := newConfigMap(map[string]string{
		"mapRoles": `
- rolearn: arn:aws:iam::123456789012:role/eks-node-role
  username: "system:node:{{EC2PrivateDNSName}}"
  groups:
    - system:bootstrappers
    - system:nodes
- rolearn: arn:aws:iam::123456789012:role/admin
  username: admin
  groups:
    - system:masters
`,
	})
	got, err := ParseAwsAuth(cm)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].PrincipalArn != "arn:aws:iam::123456789012:role/eks-node-role" {
		t.Errorf("entry[0].PrincipalArn = %q", got[0].PrincipalArn)
	}
	if got[1].Username != "admin" {
		t.Errorf("entry[1].Username = %q", got[1].Username)
	}
	if !reflect.DeepEqual(got[0].KubernetesGroups, []string{"system:bootstrappers", "system:nodes"}) {
		t.Errorf("entry[0].KubernetesGroups = %v", got[0].KubernetesGroups)
	}
}

func TestParseAwsAuth_RolesAndUsers(t *testing.T) {
	cm := newConfigMap(map[string]string{
		"mapRoles": `
- rolearn: arn:aws:iam::123456789012:role/eks-node-role
  username: system:node
  groups: [system:nodes]
`,
		"mapUsers": `
- userarn: arn:aws:iam::123456789012:user/alice
  username: alice
  groups: [system:masters]
- userarn: arn:aws:iam::123456789012:user/bob
  username: bob
  groups: []
`,
	})
	got, err := ParseAwsAuth(cm)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 entries (1 role + 2 users), got %d", len(got))
	}
	// Roles emitted first, then users — matches read order.
	if got[0].PrincipalArn != "arn:aws:iam::123456789012:role/eks-node-role" {
		t.Errorf("entry[0] = %q", got[0].PrincipalArn)
	}
	if got[1].PrincipalArn != "arn:aws:iam::123456789012:user/alice" {
		t.Errorf("entry[1] = %q", got[1].PrincipalArn)
	}
	if got[2].PrincipalArn != "arn:aws:iam::123456789012:user/bob" {
		t.Errorf("entry[2] = %q", got[2].PrincipalArn)
	}
}

func TestParseAwsAuth_SkipsBlankArn(t *testing.T) {
	// Rows missing rolearn/userarn are silently skipped — operators
	// sometimes leave commented-out skeletons in the blob.
	cm := newConfigMap(map[string]string{
		"mapRoles": `
- rolearn: ""
  username: ghost
  groups: [system:masters]
- rolearn: arn:aws:iam::123456789012:role/real
  username: real
  groups: [g1]
`,
	})
	got, err := ParseAwsAuth(cm)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
}

func TestParseAwsAuth_Malformed(t *testing.T) {
	cm := newConfigMap(map[string]string{
		"mapRoles": "this is not yaml: [unterminated",
	})
	_, err := ParseAwsAuth(cm)
	if err == nil {
		t.Fatalf("want parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse mapRoles") {
		t.Errorf("err = %v, want 'parse mapRoles' wrapping", err)
	}
}

func TestNormalizePrincipalArn(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "iam role lower",
			in:   "arn:aws:iam::123456789012:role/eks-node",
			want: "arn:aws:iam::123456789012:role/eks-node",
		},
		{
			name: "iam role mixed case lowered",
			in:   "arn:aws:iam::123456789012:role/EKS-Node",
			want: "arn:aws:iam::123456789012:role/eks-node",
		},
		{
			name: "iam user lowered",
			in:   "arn:aws:iam::123456789012:user/Alice",
			want: "arn:aws:iam::123456789012:user/alice",
		},
		{
			name: "assumed-role session collapsed to role",
			in:   "arn:aws:sts::123456789012:assumed-role/eks-pod-role/i-abc123",
			want: "arn:aws:iam::123456789012:role/eks-pod-role",
		},
		{
			name: "assumed-role session preserves casing then lowers",
			in:   "arn:aws:sts::123456789012:assumed-role/EKS-Pod-Role/session-1",
			want: "arn:aws:iam::123456789012:role/eks-pod-role",
		},
		{
			name: "gov-cloud partition preserved",
			in:   "arn:aws-us-gov:sts::123456789012:assumed-role/Foo/sess",
			want: "arn:aws-us-gov:iam::123456789012:role/foo",
		},
		{
			name: "federated user passes through lowered",
			in:   "arn:aws:sts::123456789012:federated-user/Joe",
			want: "arn:aws:sts::123456789012:federated-user/joe",
		},
		{
			name: "malformed passes through lowered",
			in:   "not-an-arn",
			want: "not-an-arn",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
		{
			name: "trims whitespace",
			in:   "  arn:aws:iam::123:role/Foo  ",
			want: "arn:aws:iam::123:role/foo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizePrincipalArn(tc.in)
			if got != tc.want {
				t.Errorf("NormalizePrincipalArn(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestDiffAwsAuthVsAccessEntries(t *testing.T) {
	cases := []struct {
		name       string
		auth       []AwsAuthEntry
		entries    []AccessEntry
		wantEntries []AwsAuthDiffEntry
		wantHealth  AwsAuthDiffHealth
	}{
		{
			name:       "both empty",
			wantHealth: AwsAuthDiffHealth{},
		},
		{
			name: "aws-auth only",
			auth: []AwsAuthEntry{
				{PrincipalArn: "arn:aws:iam::123:role/legacy", KubernetesGroups: []string{"system:masters"}},
			},
			wantEntries: []AwsAuthDiffEntry{
				{In: DiffSideAwsAuthOnly, PrincipalArn: "arn:aws:iam::123:role/legacy", KubernetesGroups: []string{"system:masters"}},
			},
			wantHealth: AwsAuthDiffHealth{AwsAuthOnly: 1},
		},
		{
			name: "access-entries only",
			entries: []AccessEntry{
				{PrincipalArn: "arn:aws:iam::123:role/migrated", KubernetesGroups: []string{"viewers"}},
			},
			wantEntries: []AwsAuthDiffEntry{
				{In: DiffSideAccessEntriesOnly, PrincipalArn: "arn:aws:iam::123:role/migrated", KubernetesGroups: []string{"viewers"}},
			},
			wantHealth: AwsAuthDiffHealth{AccessEntriesOnly: 1},
		},
		{
			name: "both same role unified groups",
			auth: []AwsAuthEntry{
				{PrincipalArn: "arn:aws:iam::123:role/admin", KubernetesGroups: []string{"system:masters", "extra"}},
			},
			entries: []AccessEntry{
				{PrincipalArn: "arn:aws:iam::123:role/admin", KubernetesGroups: []string{"admins"}},
			},
			wantEntries: []AwsAuthDiffEntry{
				{In: DiffSideBoth, PrincipalArn: "arn:aws:iam::123:role/admin", KubernetesGroups: []string{"system:masters", "extra", "admins"}},
			},
			wantHealth: AwsAuthDiffHealth{Dual: 1},
		},
		{
			name: "case-only difference reconciles as both",
			auth: []AwsAuthEntry{
				{PrincipalArn: "arn:aws:iam::123:role/Foo"},
			},
			entries: []AccessEntry{
				{PrincipalArn: "arn:aws:iam::123:role/foo"},
			},
			wantEntries: []AwsAuthDiffEntry{
				// First-seen display wins: aws-auth was processed first.
				{In: DiffSideBoth, PrincipalArn: "arn:aws:iam::123:role/Foo"},
			},
			wantHealth: AwsAuthDiffHealth{Dual: 1},
		},
		{
			name: "assumed-role session reconciles to iam role form",
			auth: []AwsAuthEntry{
				{PrincipalArn: "arn:aws:iam::123:role/pod-role"},
			},
			entries: []AccessEntry{
				{PrincipalArn: "arn:aws:sts::123:assumed-role/pod-role/sess-123"},
			},
			wantEntries: []AwsAuthDiffEntry{
				{In: DiffSideBoth, PrincipalArn: "arn:aws:iam::123:role/pod-role"},
			},
			wantHealth: AwsAuthDiffHealth{Dual: 1},
		},
		{
			name: "mixed sort order",
			auth: []AwsAuthEntry{
				{PrincipalArn: "arn:aws:iam::123:role/b-legacy"},
			},
			entries: []AccessEntry{
				{PrincipalArn: "arn:aws:iam::123:role/a-migrated"},
				{PrincipalArn: "arn:aws:iam::123:role/c-migrated"},
			},
			wantEntries: []AwsAuthDiffEntry{
				{In: DiffSideAccessEntriesOnly, PrincipalArn: "arn:aws:iam::123:role/a-migrated"},
				{In: DiffSideAwsAuthOnly, PrincipalArn: "arn:aws:iam::123:role/b-legacy"},
				{In: DiffSideAccessEntriesOnly, PrincipalArn: "arn:aws:iam::123:role/c-migrated"},
			},
			wantHealth: AwsAuthDiffHealth{AwsAuthOnly: 1, AccessEntriesOnly: 2},
		},
		{
			name: "empty arn skipped",
			auth: []AwsAuthEntry{
				{PrincipalArn: ""},
				{PrincipalArn: "arn:aws:iam::123:role/r"},
			},
			wantEntries: []AwsAuthDiffEntry{
				{In: DiffSideAwsAuthOnly, PrincipalArn: "arn:aws:iam::123:role/r"},
			},
			wantHealth: AwsAuthDiffHealth{AwsAuthOnly: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotEntries, gotHealth := DiffAwsAuthVsAccessEntries(tc.auth, tc.entries)
			if !reflect.DeepEqual(gotHealth, tc.wantHealth) {
				t.Errorf("health = %+v, want %+v", gotHealth, tc.wantHealth)
			}
			if len(gotEntries) != len(tc.wantEntries) {
				t.Fatalf("entries len = %d, want %d\ngot:  %+v\nwant: %+v", len(gotEntries), len(tc.wantEntries), gotEntries, tc.wantEntries)
			}
			for i := range gotEntries {
				if gotEntries[i].In != tc.wantEntries[i].In {
					t.Errorf("entry[%d].In = %q, want %q", i, gotEntries[i].In, tc.wantEntries[i].In)
				}
				if gotEntries[i].PrincipalArn != tc.wantEntries[i].PrincipalArn {
					t.Errorf("entry[%d].PrincipalArn = %q, want %q", i, gotEntries[i].PrincipalArn, tc.wantEntries[i].PrincipalArn)
				}
				if !groupsEqual(gotEntries[i].KubernetesGroups, tc.wantEntries[i].KubernetesGroups) {
					t.Errorf("entry[%d].KubernetesGroups = %v, want %v", i, gotEntries[i].KubernetesGroups, tc.wantEntries[i].KubernetesGroups)
				}
			}
		})
	}
}

func groupsEqual(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}
