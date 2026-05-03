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
