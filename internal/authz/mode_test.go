package authz

import (
	"reflect"
	"testing"
)

func TestResolverShared(t *testing.T) {
	r, err := NewResolver(Config{Mode: ModeShared})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	got := r.Resolve(Identity{Subject: "alice", Groups: []string{"admins"}})
	if !got.IsZero() {
		t.Errorf("shared mode should produce zero ImpersonationConfig, got %+v", got)
	}
}

func TestResolverTierBasic(t *testing.T) {
	r, err := NewResolver(Config{
		Mode: ModeTier,
		GroupTiers: map[string]string{
			"engineers": TierWrite,
			"sres":      TierAdmin,
		},
		DefaultTier: TierRead,
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}

	tests := []struct {
		name   string
		id     Identity
		want   ImpersonationConfig
	}{
		{
			"engineer maps to write",
			Identity{Subject: "alice", Groups: []string{"engineers"}},
			ImpersonationConfig{UserName: "alice", Groups: []string{"periscope-tier:write"}},
		},
		{
			"sre maps to admin",
			Identity{Subject: "bob", Groups: []string{"sres"}},
			ImpersonationConfig{UserName: "bob", Groups: []string{"periscope-tier:admin"}},
		},
		{
			"both groups → highest privilege wins",
			Identity{Subject: "carol", Groups: []string{"engineers", "sres"}},
			ImpersonationConfig{UserName: "carol", Groups: []string{"periscope-tier:admin"}},
		},
		{
			"no matching group → defaultTier",
			Identity{Subject: "dave", Groups: []string{"interns"}},
			ImpersonationConfig{UserName: "dave", Groups: []string{"periscope-tier:read"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := r.Resolve(tc.id)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolverTierDefaultTierEmptyDeny(t *testing.T) {
	r, err := NewResolver(Config{
		Mode:        ModeTier,
		GroupTiers:  map[string]string{"sres": TierAdmin},
		DefaultTier: "",
	})
	if err != nil {
		// "" defaultTier is allowed — it's the deny signal.
		// Constructor should NOT fail for empty defaultTier.
		t.Fatalf("NewResolver should accept empty defaultTier as deny: %v", err)
	}
	if got := r.Resolve(Identity{Subject: "interloper", Groups: []string{"strangers"}}); got.UserName != "interloper" || len(got.Groups) != 0 {
		t.Errorf("user with no matching tier and empty default should impersonate as themselves with no groups (deny-via-RBAC), got %+v", got)
	}
}

func TestResolverRaw(t *testing.T) {
	r, err := NewResolver(Config{
		Mode:        ModeRaw,
		GroupPrefix: "periscope:",
	})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	got := r.Resolve(Identity{
		Subject: "alice",
		Groups:  []string{"engineers", "oncall", ""},
	})
	want := ImpersonationConfig{
		UserName: "alice",
		Groups:   []string{"periscope:engineers", "periscope:oncall"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestResolverRawDefaultsPrefix(t *testing.T) {
	r, _ := NewResolver(Config{Mode: ModeRaw}) // no GroupPrefix set
	got := r.Resolve(Identity{Subject: "alice", Groups: []string{"x"}})
	if got.Groups[0] != "periscope:x" {
		t.Errorf("expected default prefix periscope:, got %q", got.Groups[0])
	}
}

func TestResolverDefaultsToShared(t *testing.T) {
	r, err := NewResolver(Config{}) // empty config
	if err != nil {
		t.Fatalf("NewResolver empty: %v", err)
	}
	if r.Mode() != ModeShared {
		t.Errorf("expected ModeShared default, got %q", r.Mode())
	}
}

func TestResolverInvalidMode(t *testing.T) {
	_, err := NewResolver(Config{Mode: "rampage"})
	if err == nil {
		t.Errorf("expected error for invalid mode")
	}
}

func TestResolverInvalidTier(t *testing.T) {
	_, err := NewResolver(Config{
		Mode:       ModeTier,
		GroupTiers: map[string]string{"sres": "supreme"},
	})
	if err == nil {
		t.Errorf("expected error for invalid tier")
	}
}

func TestResolverEmptySubjectIsShared(t *testing.T) {
	r, _ := NewResolver(Config{Mode: ModeTier, GroupTiers: map[string]string{"sres": TierAdmin}})
	got := r.Resolve(Identity{Subject: "", Groups: []string{"sres"}})
	if !got.IsZero() {
		t.Errorf("anonymous identity should fail safe to zero ImpersonationConfig, got %+v", got)
	}
}

func TestResolvedTierAndAllowedTier(t *testing.T) {
	r, _ := NewResolver(Config{
		Mode:        ModeTier,
		GroupTiers:  map[string]string{"sres": TierAdmin},
		DefaultTier: "",
	})
	if got := r.ResolvedTier(Identity{Groups: []string{"sres"}}); got != TierAdmin {
		t.Errorf("ResolvedTier sre = %q, want admin", got)
	}
	if got := r.ResolvedTier(Identity{Groups: []string{"strangers"}}); got != "" {
		t.Errorf("ResolvedTier stranger = %q, want empty (deny)", got)
	}
	if !r.AllowedTier(Identity{Groups: []string{"sres"}}) {
		t.Errorf("AllowedTier sre should be true")
	}
	if r.AllowedTier(Identity{Groups: []string{"strangers"}}) {
		t.Errorf("AllowedTier stranger with empty defaultTier should be false")
	}
}

func TestResolver_IsAuditAdmin(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		id   Identity
		want bool
	}{
		// --- explicit AuditAdminGroups always wins ---
		{
			name: "explicit override matches",
			cfg:  Config{Mode: ModeShared, AuditAdminGroups: []string{"sec-team"}},
			id:   Identity{Subject: "alice", Groups: []string{"sec-team", "engineers"}},
			want: true,
		},
		{
			name: "explicit override no match",
			cfg:  Config{Mode: ModeShared, AuditAdminGroups: []string{"sec-team"}},
			id:   Identity{Subject: "bob", Groups: []string{"engineers"}},
			want: false,
		},
		{
			name: "explicit override beats tier=admin denial",
			// User has admin tier but is NOT in AuditAdminGroups → denied.
			// Explicit override means tier admin is NOT auto-granted.
			cfg: Config{
				Mode:             ModeTier,
				GroupTiers:       map[string]string{"admins": "admin"},
				AuditAdminGroups: []string{"sec-team"},
			},
			id:   Identity{Subject: "carol", Groups: []string{"admins"}},
			want: false,
		},

		// --- tier mode fallback ---
		{
			name: "tier mode + tier=admin",
			cfg:  Config{Mode: ModeTier, GroupTiers: map[string]string{"admins": "admin"}},
			id:   Identity{Subject: "dave", Groups: []string{"admins"}},
			want: true,
		},
		{
			name: "tier mode + tier=triage",
			cfg:  Config{Mode: ModeTier, GroupTiers: map[string]string{"oncall": "triage"}},
			id:   Identity{Subject: "eve", Groups: []string{"oncall"}},
			want: false,
		},
		{
			name: "tier mode + no tier match",
			cfg:  Config{Mode: ModeTier, GroupTiers: map[string]string{"admins": "admin"}},
			id:   Identity{Subject: "frank", Groups: []string{"contractors"}},
			want: false,
		},

		// --- shared mode fallback ---
		{
			name: "shared mode + non-empty AllowedGroups + match",
			cfg:  Config{Mode: ModeShared, AllowedGroups: []string{"engineers"}},
			id:   Identity{Subject: "grace", Groups: []string{"engineers"}},
			want: true,
		},
		{
			name: "shared mode + non-empty AllowedGroups + no match",
			cfg:  Config{Mode: ModeShared, AllowedGroups: []string{"engineers"}},
			id:   Identity{Subject: "henry", Groups: []string{"contractors"}},
			want: false,
		},
		{
			name: "shared mode + empty AllowedGroups → false (safety default)",
			cfg:  Config{Mode: ModeShared},
			id:   Identity{Subject: "ivy", Groups: []string{"anyone"}},
			want: false,
		},

		// --- raw mode always false without explicit override ---
		{
			name: "raw mode + no override → always false",
			cfg:  Config{Mode: ModeRaw},
			id:   Identity{Subject: "jack", Groups: []string{"sres", "admins"}},
			want: false,
		},
		{
			name: "raw mode + AuditAdminGroups override grants",
			cfg:  Config{Mode: ModeRaw, AuditAdminGroups: []string{"sres"}},
			id:   Identity{Subject: "kate", Groups: []string{"sres"}},
			want: true,
		},

		// --- edge cases ---
		{
			name: "empty groups → never admin",
			cfg:  Config{Mode: ModeShared, AllowedGroups: []string{"engineers"}},
			id:   Identity{Subject: "leo", Groups: nil},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewResolver(tt.cfg)
			if err != nil {
				t.Fatalf("NewResolver: %v", err)
			}
			if got := r.IsAuditAdmin(tt.id); got != tt.want {
				t.Errorf("IsAuditAdmin(%+v) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
