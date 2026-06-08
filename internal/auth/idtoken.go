package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ErrReauthRequired signals that a live OIDC id_token could not be
// produced for the session without a fresh login — the refresh token is
// absent, the refresh failed, or the IdP did not rotate the id_token on
// refresh. Handlers map it to a clean 401 so the SPA re-authenticates,
// rather than letting a stale token reach an external verifier (e.g. AWS
// STS) and fail with an opaque error.
var ErrReauthRequired = errors.New("auth: re-authentication required")

// idTokenSkew is how far before the id_token's exp we treat it as stale
// and refresh — wide enough to survive the downstream round-trip that
// will present the token (e.g. sts:AssumeRoleWithWebIdentity).
const idTokenSkew = 90 * time.Second

// refresher is the subset of *OIDCClient that IDTokenSource needs.
// Defined as an interface so tests can supply a fake without standing up
// a real OIDC provider.
type refresher interface {
	Refresh(context.Context, Session) (Session, error)
}

// IDTokenSource hands out a guaranteed-fresh OIDC id_token for a
// request's session. It is the ONLY sanctioned way for a handler to
// obtain the raw id_token: Middleware deliberately strips tokens before
// they reach the request context (handlers see the public Identity), so
// a feature that must federate the id_token to an external party — e.g.
// sts:AssumeRoleWithWebIdentity for the SSM node shell — goes through
// here. The token is returned directly to the single caller; it never
// enters the request context.
type IDTokenSource struct {
	refresher refresher
	store     SessionStore
	cfg       Config
	now       func() time.Time // injectable for tests
	locks     shardedMutex     // serialize refresh per session
}

// NewIDTokenSource bundles the same trio Middleware uses. Construct one
// at startup alongside the middleware and hand it to the handlers that
// need raw-token egress.
func NewIDTokenSource(client *OIDCClient, store SessionStore, cfg Config) *IDTokenSource {
	return &IDTokenSource{
		refresher: client,
		store:     store,
		cfg:       cfg,
		now:       time.Now,
	}
}

// FreshIDToken returns a non-expired OIDC id_token for the session named
// by r's cookie, refreshing transparently when it is near expiry.
// Returns ErrReauthRequired when no live token can be produced without a
// new login.
func (s *IDTokenSource) FreshIDToken(r *http.Request) (string, error) {
	c, err := r.Cookie(s.cfg.Session.CookieName)
	if err != nil || c.Value == "" {
		return "", ErrReauthRequired
	}
	sid := c.Value

	// Fast path: token already fresh — no lock, no refresh.
	if sess, ok := s.store.Get(sid); ok && !s.expSoon(sess.IDToken) {
		return sess.IDToken, nil
	}

	// Slow path: serialize refresh per session so concurrent callers do
	// not each spend the (single-use, possibly rotating) refresh token.
	unlock := s.locks.lock(sid)
	defer unlock()

	// Re-read under the lock: a concurrent caller may have refreshed
	// while we waited, in which case we return its fresh token.
	sess, ok := s.store.Get(sid)
	if !ok {
		return "", ErrReauthRequired
	}
	if !s.expSoon(sess.IDToken) {
		return sess.IDToken, nil
	}
	if sess.RefreshToken == "" {
		return "", ErrReauthRequired
	}

	refreshed, err := s.refresher.Refresh(r.Context(), sess)
	if err != nil {
		return "", fmt.Errorf("%w: refresh failed: %v", ErrReauthRequired, err)
	}
	if err := s.store.Update(refreshed); err != nil {
		// Session vanished between Get and Update (e.g. concurrent
		// logout). Don't hand back a token for a session that's gone.
		return "", ErrReauthRequired
	}

	// Many IdPs do not return a new id_token on a refresh grant. If it is
	// still near expiry, only a fresh login will help.
	if s.expSoon(refreshed.IDToken) {
		return "", ErrReauthRequired
	}
	return refreshed.IDToken, nil
}

// expSoon reports whether the id_token is missing, unparseable, or
// within idTokenSkew of expiry. Unparseable counts as stale (fail safe):
// we never hand back a token whose lifetime we cannot reason about.
func (s *IDTokenSource) expSoon(idToken string) bool {
	exp, ok := jwtExp(idToken)
	if !ok {
		return true
	}
	return s.now().Add(idTokenSkew).After(exp)
}

// jwtExp extracts the exp claim from a JWT WITHOUT verifying its
// signature — the token was verified by the OIDC verifier at login and
// the owning session is still valid; here we only read the expiry to
// decide whether to refresh.
func jwtExp(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

// shardedMutex is a fixed bank of mutexes keyed by hash. Bounded (no map
// growth, no cleanup); distinct sessions occasionally share a shard,
// which only briefly serializes two unrelated refreshes — harmless given
// refreshes are rare and fast. Per-process, which matches the
// per-process MemoryStore; a shared session store would need a
// distributed lock instead.
type shardedMutex [256]sync.Mutex

func (m *shardedMutex) lock(key string) func() {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	mu := &m[h.Sum32()%uint32(len(m))]
	mu.Lock()
	return mu.Unlock
}
