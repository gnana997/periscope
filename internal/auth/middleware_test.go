package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newOIDCConfigForTest() Config {
	return Config{
		Mode: ModeOIDC,
		Session: SessionConfig{
			CookieName:      "periscope_session",
			IdleTimeout:     30 * time.Minute,
			AbsoluteTimeout: 8 * time.Hour,
		},
	}
}

func reqWithCookie(name, value string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	if value != "" {
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	return r
}

func TestSessionValid_DevModeAlwaysTrue(t *testing.T) {
	cfg := Config{Mode: ModeDev}
	store := NewMemoryStore()
	if !SessionValid(reqWithCookie("", ""), store, cfg) {
		t.Fatal("dev mode should always validate")
	}
}

func TestSessionValid_NoCookie(t *testing.T) {
	cfg := newOIDCConfigForTest()
	store := NewMemoryStore()
	if SessionValid(httptest.NewRequest("GET", "/", nil), store, cfg) {
		t.Fatal("no cookie should fail validation")
	}
}

func TestSessionValid_EmptyCookieValue(t *testing.T) {
	cfg := newOIDCConfigForTest()
	store := NewMemoryStore()
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: cfg.Session.CookieName, Value: ""})
	if SessionValid(r, store, cfg) {
		t.Fatal("empty cookie should fail validation")
	}
}

func TestSessionValid_NotInStore(t *testing.T) {
	cfg := newOIDCConfigForTest()
	store := NewMemoryStore()
	if SessionValid(reqWithCookie(cfg.Session.CookieName, "missing"), store, cfg) {
		t.Fatal("unknown session id should fail validation")
	}
}

func TestSessionValid_Fresh(t *testing.T) {
	cfg := newOIDCConfigForTest()
	store := NewMemoryStore()
	now := time.Now()
	_ = store.Create(Session{
		ID:             "sid",
		Subject:        "alice",
		AbsoluteExpiry: now.Add(1 * time.Hour),
		LastActivity:   now,
	})
	if !SessionValid(reqWithCookie(cfg.Session.CookieName, "sid"), store, cfg) {
		t.Fatal("fresh session should validate")
	}
}

func TestSessionValid_AbsoluteExpired(t *testing.T) {
	cfg := newOIDCConfigForTest()
	store := NewMemoryStore()
	now := time.Now()
	_ = store.Create(Session{
		ID:             "sid",
		Subject:        "alice",
		AbsoluteExpiry: now.Add(-1 * time.Minute),
		LastActivity:   now,
	})
	if SessionValid(reqWithCookie(cfg.Session.CookieName, "sid"), store, cfg) {
		t.Fatal("absolute-expired session should fail validation")
	}
}

func TestSessionValid_IdleExceeded(t *testing.T) {
	cfg := newOIDCConfigForTest()
	store := NewMemoryStore()
	now := time.Now()
	_ = store.Create(Session{
		ID:             "sid",
		Subject:        "alice",
		AbsoluteExpiry: now.Add(1 * time.Hour),
		LastActivity:   now.Add(-1 * time.Hour), // idleTimeout=30m, last activity 1h ago
	})
	if SessionValid(reqWithCookie(cfg.Session.CookieName, "sid"), store, cfg) {
		t.Fatal("idle-exceeded session should fail validation")
	}
}

func TestSessionValid_IdleDisabled(t *testing.T) {
	cfg := newOIDCConfigForTest()
	cfg.Session.IdleTimeout = 0 // disabled
	store := NewMemoryStore()
	now := time.Now()
	_ = store.Create(Session{
		ID:             "sid",
		Subject:        "alice",
		AbsoluteExpiry: now.Add(1 * time.Hour),
		LastActivity:   now.Add(-24 * time.Hour),
	})
	if !SessionValid(reqWithCookie(cfg.Session.CookieName, "sid"), store, cfg) {
		t.Fatal("idle disabled should ignore LastActivity")
	}
}

func TestUnauthorized_BrowserNavigationRedirectsToLogin(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	w := httptest.NewRecorder()
	unauthorized(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/api/auth/login" {
		t.Fatalf("Location = %q, want /api/auth/login", loc)
	}
}

func TestUnauthorized_XHRReturnsPlain401(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/me", nil)
	r.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	unauthorized(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "" {
		t.Fatalf("Location = %q, want empty (no redirect for XHR)", loc)
	}
	if got := w.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("WWW-Authenticate header missing on 401")
	}
}

func TestUnauthorized_NonGETReturns401(t *testing.T) {
	// POST with text/html accept (curious case) shouldn't redirect —
	// redirecting a state-changing request is wrong.
	r := httptest.NewRequest("POST", "/api/clusters/x/pods", nil)
	r.Header.Set("Accept", "text/html")
	w := httptest.NewRecorder()
	unauthorized(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestUnauthorized_NoAcceptHeaderReturns401(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/foo", nil)
	w := httptest.NewRecorder()
	unauthorized(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestAcceptsHTML(t *testing.T) {
	cases := []struct {
		accept string
		want   bool
	}{
		{"text/html,application/xhtml+xml,application/xml;q=0.9", true},
		{"text/html;q=0.9", true},
		{"application/json", false},
		{"*/*", false},
		{"", false},
		{"  text/html  ", true},
	}
	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		if tc.accept != "" {
			r.Header.Set("Accept", tc.accept)
		}
		if got := acceptsHTML(r); got != tc.want {
			t.Errorf("acceptsHTML(%q) = %v, want %v", tc.accept, got, tc.want)
		}
	}
}

func TestIsPublic_AgentRegister(t *testing.T) {
	// /api/agents/register is unauth-by-design (the bootstrap token in
	// the request body IS the auth). If this regresses, agents fail
	// first-boot registration with 401 from the auth middleware long
	// before tunnel.RegisterHandler ever sees the request — see #48.
	if !isPublic("/api/agents/register") {
		t.Fatal("/api/agents/register should be in the isPublic allowlist")
	}
}

func TestIsPublic_AgentTokensRequiresAuth(t *testing.T) {
	// /api/agents/tokens is admin-tier-only — must NOT be in the
	// isPublic allowlist. The session-cookie check runs first; the
	// adminOnlyMiddleware in cmd/periscope enforces tier on top.
	if isPublic("/api/agents/tokens") {
		t.Fatal("/api/agents/tokens must NOT be public — it's admin-tier-only")
	}
}

func TestIsPublic_KnownPathsCovered(t *testing.T) {
	// Lock the full set so future additions to isPublic show up as a
	// test diff, prompting a security review of the new entry.
	wantPublic := []string{
		"/healthz",
		"/api/auth/config",
		"/api/auth/login",
		"/api/auth/callback",
		"/api/auth/loggedout",
		"/api/auth/logout",
		"/api/auth/logout/everywhere",
		"/api/agents/register",
	}
	for _, p := range wantPublic {
		if !isPublic(p) {
			t.Errorf("isPublic(%q) = false, want true", p)
		}
	}

	// Negative: a sample of paths that MUST require auth.
	wantPrivate := []string{
		"/api/whoami",
		"/api/clusters",
		"/api/fleet",
		"/api/audit",
		"/api/agents/tokens",                 // admin-tier-only
		"/api/clusters/prod-eu/pods",         // per-cluster handler
		"/",                                  // SPA root — auth-gated for HTML
		"/some/random/path",                  // unknown path — fail closed
	}
	for _, p := range wantPrivate {
		if isPublic(p) {
			t.Errorf("isPublic(%q) = true, want false (would bypass auth)", p)
		}
	}
}
