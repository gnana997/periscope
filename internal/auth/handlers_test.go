package auth

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// LogoutHandler must redirect to "/?signedOut=1" so the SPA's
// AuthProvider knows to suppress the silent-SSO redirect that would
// otherwise re-authenticate against Auth0's still-valid session and
// undo the logout. Pin this contract — anyone "cleaning up" the query
// param would silently re-introduce the loop.
func TestLogoutHandler_RedirectsWithSignedOutFlag(t *testing.T) {
	cfg := newOIDCConfigForTest()
	store := NewMemoryStore()

	h := LogoutHandler(store, cfg)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/auth/logout", nil)

	h.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if got := w.Header().Get("Location"); got != "/?signedOut=1" {
		t.Errorf("Location = %q, want %q", got, "/?signedOut=1")
	}
}

func TestLogoutEverywhereHandler_NilClient_RedirectsWithSignedOutFlag(t *testing.T) {
	cfg := newOIDCConfigForTest()
	store := NewMemoryStore()

	// Nil client path: no IdP end_session_endpoint configured, so the
	// handler degrades to local logout. Must still set the flag.
	h := LogoutEverywhereHandler(nil, store, cfg)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/auth/logout/everywhere", nil)

	h.ServeHTTP(w, r)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusFound)
	}
	if got := w.Header().Get("Location"); got != "/?signedOut=1" {
		t.Errorf("Location = %q, want %q", got, "/?signedOut=1")
	}
}

// WhoamiHandler must serialize an empty groups slice as the JSON []
// literal, never null. The SPA's UserMenu treats null as a programming
// error and crashes. A user with zero groups is a legitimate state
// (defaultTier in tier mode covers it), so the wire shape must be a
// stable [].
func TestWhoamiHandler_NilGroups_MarshalsAsEmptyArray(t *testing.T) {
	cfg := newOIDCConfigForTest()
	store := NewMemoryStore()

	h := WhoamiHandler(store, cfg, nil, false)
	r := httptest.NewRequest("GET", "/api/auth/whoami", nil)
	r = r.WithContext(plant(r.Context(), Identity{
		Subject: "alice",
		Email:   "alice@example.com",
		Groups:  nil, // the case under test
	}))
	w := httptest.NewRecorder()

	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%q)", w.Code, http.StatusOK, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, `"groups":null`) {
		t.Errorf("groups serialized as null; body = %s", body)
	}
	if !strings.Contains(body, `"groups":[]`) {
		t.Errorf("groups not serialized as []; body = %s", body)
	}

	// Round-trip parse to confirm the slice survives JSON decoding as
	// a non-nil empty slice (this is what fetch().json() will see).
	var decoded struct {
		Groups []string `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Groups == nil {
		t.Errorf("decoded groups is nil; want non-nil empty slice")
	}
	if len(decoded.Groups) != 0 {
		t.Errorf("decoded groups len = %d, want 0", len(decoded.Groups))
	}
}

func TestResolveGroups(t *testing.T) {
	cases := []struct {
		name        string
		idClaims    map[string]any
		accessToken string
		groupsClaim string
		wantGroups  []string
		wantErr     error
	}{
		{
			// groupsClaim unconfigured: group-based authz is off; never
			// fail, never inspect tokens.
			name:        "no groups claim configured returns empty without error",
			idClaims:    nil,
			accessToken: "",
			groupsClaim: "",
			wantGroups:  []string{},
			wantErr:     nil,
		},
		{
			name:        "claim present in id token returns those groups",
			idClaims:    map[string]any{"groups": []any{"platform", "sre"}},
			accessToken: "",
			groupsClaim: "groups",
			wantGroups:  []string{"platform", "sre"},
			wantErr:     nil,
		},
		{
			// Load-bearing semantics: present-but-empty must NOT fall
			// through to the access-token branch. A user with zero
			// roles is real and should resolve to defaultTier, not
			// trigger ErrGroupsClaimMissing.
			name:        "claim present but empty returns empty without falling through",
			idClaims:    map[string]any{"groups": []any{}},
			accessToken: encodeJWTPayloadForTest(map[string]any{"groups": []any{"should-not-be-read"}}),
			groupsClaim: "groups",
			wantGroups:  []string{},
			wantErr:     nil,
		},
		{
			name:        "claim absent in id token falls through to access token",
			idClaims:    map[string]any{"sub": "alice"},
			accessToken: encodeJWTPayloadForTest(map[string]any{"groups": []any{"platform"}}),
			groupsClaim: "groups",
			wantGroups:  []string{"platform"},
			wantErr:     nil,
		},
		{
			// The change this test pins: configured groupsClaim absent
			// from BOTH tokens is a hard failure, not a silent fallback
			// to defaultTier. Surfacing IdP misconfiguration is the
			// whole point.
			name:        "claim absent from both tokens returns ErrGroupsClaimMissing",
			idClaims:    map[string]any{"sub": "alice"},
			accessToken: encodeJWTPayloadForTest(map[string]any{"sub": "alice"}),
			groupsClaim: "groups",
			wantGroups:  nil,
			wantErr:     ErrGroupsClaimMissing,
		},
		{
			name:        "claim absent and no access token returns ErrGroupsClaimMissing",
			idClaims:    map[string]any{"sub": "alice"},
			accessToken: "",
			groupsClaim: "groups",
			wantGroups:  nil,
			wantErr:     ErrGroupsClaimMissing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveGroups(tc.idClaims, tc.accessToken, tc.groupsClaim)
			if err != tc.wantErr {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if !equalStringSlices(got, tc.wantGroups) {
				t.Errorf("groups = %v, want %v", got, tc.wantGroups)
			}
		})
	}
}

// encodeJWTPayloadForTest builds a minimal JWT — header.payload.sig —
// where only the payload section needs to round-trip through
// decodeJWTPayload. Header and signature are arbitrary; we don't
// verify either inside resolveGroups.
func encodeJWTPayloadForTest(payload map[string]any) string {
	body, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
