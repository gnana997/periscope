package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixedNow anchors the clock so token expiries are deterministic.
var fixedNow = time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

// makeJWT builds a syntactically valid (unsigned) JWT carrying just the
// exp claim — all jwtExp reads. Signature segment is a placeholder.
func makeJWT(exp time.Time) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	pl := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp.Unix())))
	return hdr + "." + pl + ".sig"
}

type fakeRefresher struct {
	mu    sync.Mutex
	calls int32
	out   Session // returned on success
	err   error
}

func (f *fakeRefresher) Refresh(_ context.Context, in Session) (Session, error) {
	atomic.AddInt32(&f.calls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Session{}, f.err
	}
	out := f.out
	out.ID = in.ID // preserve identity so store.Update finds the row
	return out, nil
}

func newTestSource(store SessionStore, fr refresher) *IDTokenSource {
	return &IDTokenSource{
		refresher: fr,
		store:     store,
		cfg:       Config{Session: SessionConfig{CookieName: "periscope_session"}},
		now:       func() time.Time { return fixedNow },
	}
}

func seedSession(t *testing.T, store SessionStore, id string, s Session) {
	t.Helper()
	s.ID = id
	if err := store.Create(s); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func TestFreshIDToken_AlreadyFresh(t *testing.T) {
	store := NewMemoryStore()
	fresh := makeJWT(fixedNow.Add(time.Hour))
	seedSession(t, store, "sid", Session{IDToken: fresh, RefreshToken: "rt"})
	fr := &fakeRefresher{}
	src := newTestSource(store, fr)

	got, err := src.FreshIDToken(reqWithCookie("periscope_session", "sid"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != fresh {
		t.Fatalf("got %q, want the existing fresh token", got)
	}
	if c := atomic.LoadInt32(&fr.calls); c != 0 {
		t.Fatalf("refresher called %d times; should not refresh a fresh token", c)
	}
}

func TestFreshIDToken_RefreshRotates(t *testing.T) {
	store := NewMemoryStore()
	stale := makeJWT(fixedNow.Add(30 * time.Second)) // within skew
	rotated := makeJWT(fixedNow.Add(time.Hour))
	seedSession(t, store, "sid", Session{IDToken: stale, RefreshToken: "rt"})
	fr := &fakeRefresher{out: Session{IDToken: rotated, RefreshToken: "rt2"}}
	src := newTestSource(store, fr)

	got, err := src.FreshIDToken(reqWithCookie("periscope_session", "sid"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != rotated {
		t.Fatalf("got %q, want the rotated token", got)
	}
	if c := atomic.LoadInt32(&fr.calls); c != 1 {
		t.Fatalf("refresher called %d times, want 1", c)
	}
	if persisted, _ := store.Get("sid"); persisted.IDToken != rotated {
		t.Fatalf("store not updated with rotated token")
	}
}

func TestFreshIDToken_RefreshDoesNotRotate(t *testing.T) {
	store := NewMemoryStore()
	stale := makeJWT(fixedNow.Add(30 * time.Second))
	seedSession(t, store, "sid", Session{IDToken: stale, RefreshToken: "rt"})
	// IdP returns a still-stale id_token (didn't rotate it on refresh).
	fr := &fakeRefresher{out: Session{IDToken: stale, RefreshToken: "rt2"}}
	src := newTestSource(store, fr)

	_, err := src.FreshIDToken(reqWithCookie("periscope_session", "sid"))
	if !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("got %v, want ErrReauthRequired", err)
	}
}

func TestFreshIDToken_NoRefreshToken(t *testing.T) {
	store := NewMemoryStore()
	stale := makeJWT(fixedNow.Add(30 * time.Second))
	seedSession(t, store, "sid", Session{IDToken: stale, RefreshToken: ""})
	fr := &fakeRefresher{}
	src := newTestSource(store, fr)

	_, err := src.FreshIDToken(reqWithCookie("periscope_session", "sid"))
	if !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("got %v, want ErrReauthRequired", err)
	}
	if c := atomic.LoadInt32(&fr.calls); c != 0 {
		t.Fatalf("refresher called %d times; should not refresh without a refresh token", c)
	}
}

func TestFreshIDToken_RefreshErrors(t *testing.T) {
	store := NewMemoryStore()
	stale := makeJWT(fixedNow.Add(30 * time.Second))
	seedSession(t, store, "sid", Session{IDToken: stale, RefreshToken: "rt"})
	fr := &fakeRefresher{err: errors.New("idp down")}
	src := newTestSource(store, fr)

	_, err := src.FreshIDToken(reqWithCookie("periscope_session", "sid"))
	if !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("got %v, want ErrReauthRequired", err)
	}
}

func TestFreshIDToken_NoCookieOrUnknownSession(t *testing.T) {
	store := NewMemoryStore()
	src := newTestSource(store, &fakeRefresher{})

	if _, err := src.FreshIDToken(reqWithCookie("periscope_session", "")); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("missing cookie: got %v, want ErrReauthRequired", err)
	}
	if _, err := src.FreshIDToken(reqWithCookie("periscope_session", "nope")); !errors.Is(err, ErrReauthRequired) {
		t.Fatalf("unknown session: got %v, want ErrReauthRequired", err)
	}
}

func TestFreshIDToken_UnparseableTokenRefreshes(t *testing.T) {
	store := NewMemoryStore()
	rotated := makeJWT(fixedNow.Add(time.Hour))
	seedSession(t, store, "sid", Session{IDToken: "not-a-jwt", RefreshToken: "rt"})
	fr := &fakeRefresher{out: Session{IDToken: rotated, RefreshToken: "rt2"}}
	src := newTestSource(store, fr)

	got, err := src.FreshIDToken(reqWithCookie("periscope_session", "sid"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != rotated {
		t.Fatalf("got %q, want rotated token after treating garbage as stale", got)
	}
}

// TestFreshIDToken_ConcurrentRefreshOnce verifies the per-session lock +
// double-checked re-read: N concurrent callers for one stale session
// trigger exactly one refresh, and all observe the rotated token.
// Run with -race.
func TestFreshIDToken_ConcurrentRefreshOnce(t *testing.T) {
	store := NewMemoryStore()
	stale := makeJWT(fixedNow.Add(30 * time.Second))
	rotated := makeJWT(fixedNow.Add(time.Hour))
	seedSession(t, store, "sid", Session{IDToken: stale, RefreshToken: "rt"})
	fr := &fakeRefresher{out: Session{IDToken: rotated, RefreshToken: "rt2"}}
	src := newTestSource(store, fr)

	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := src.FreshIDToken(reqWithCookie("periscope_session", "sid"))
			if err != nil {
				errs <- err
				return
			}
			if got != rotated {
				errs <- fmt.Errorf("got %q, want rotated", got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if c := atomic.LoadInt32(&fr.calls); c != 1 {
		t.Fatalf("refresher called %d times, want exactly 1", c)
	}
}

func TestJWTExp(t *testing.T) {
	exp := fixedNow.Add(time.Hour)
	got, ok := jwtExp(makeJWT(exp))
	if !ok || !got.Equal(time.Unix(exp.Unix(), 0)) {
		t.Fatalf("jwtExp = (%v, %v), want (%v, true)", got, ok, exp)
	}
	for _, bad := range []string{"", "onlyonepart", "a.!!!notbase64.c"} {
		if _, ok := jwtExp(bad); ok {
			t.Fatalf("jwtExp(%q) ok=true, want false", bad)
		}
	}
}
