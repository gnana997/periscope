package tunnel

import (
	"errors"
	"testing"
	"time"
)

// fixedClock returns a synthetic clock the test can advance
// imperatively. Avoids real sleeps and lets us prove TTL behavior
// without slowing the suite.
type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

func newTestStore(t *testing.T) (*TokenStore, *fixedClock) {
	t.Helper()
	clock := &fixedClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	store := NewTokenStore(TokenStoreOptions{TTL: 15 * time.Minute, ReapInterval: -1})
	store.SetClock(clock.Now)
	return store, clock
}

func TestMintRedeem_HappyPath(t *testing.T) {
	store, _ := newTestStore(t)
	iss, err := store.MintToken("prod-eu")
	if err != nil {
		t.Fatalf("MintToken: %v", err)
	}
	if iss.Token == "" {
		t.Fatal("empty token")
	}
	cluster, err := store.RedeemToken(iss.Token, "prod-eu")
	if err != nil {
		t.Fatalf("RedeemToken: %v", err)
	}
	if cluster != "prod-eu" {
		t.Fatalf("redeemed cluster = %q, want prod-eu", cluster)
	}
}

func TestRedeem_SingleUse(t *testing.T) {
	store, _ := newTestStore(t)
	iss, _ := store.MintToken("c")
	if _, err := store.RedeemToken(iss.Token, "c"); err != nil {
		t.Fatalf("first redeem failed: %v", err)
	}
	if _, err := store.RedeemToken(iss.Token, "c"); !errors.Is(err, ErrTokenConsumed) {
		t.Fatalf("second redeem err = %v, want ErrTokenConsumed", err)
	}
}

func TestRedeem_Expired(t *testing.T) {
	store, clock := newTestStore(t)
	iss, _ := store.MintToken("c")
	clock.now = clock.now.Add(20 * time.Minute) // past 15-min TTL
	if _, err := store.RedeemToken(iss.Token, "c"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
	// Expired-then-tried is also consumed (so subsequent attempts
	// don't pile up).
	if _, err := store.RedeemToken(iss.Token, "c"); !errors.Is(err, ErrTokenConsumed) {
		t.Fatalf("re-redeem after expiry err = %v, want ErrTokenConsumed", err)
	}
}

func TestRedeem_ClusterMismatch_BurnsToken(t *testing.T) {
	store, _ := newTestStore(t)
	iss, _ := store.MintToken("prod-eu")
	if _, err := store.RedeemToken(iss.Token, "prod-us"); !errors.Is(err, ErrTokenClusterMismatch) {
		t.Fatalf("err = %v, want ErrTokenClusterMismatch", err)
	}
	// Now the right name shouldn't redeem either — token burned on
	// first wrong attempt.
	if _, err := store.RedeemToken(iss.Token, "prod-eu"); !errors.Is(err, ErrTokenConsumed) {
		t.Fatalf("after-mismatch redeem err = %v, want ErrTokenConsumed", err)
	}
}

func TestRedeem_Unknown(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.RedeemToken("ghosts", "c"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("err = %v, want ErrTokenInvalid", err)
	}
}

func TestMintToken_RejectsEmptyCluster(t *testing.T) {
	store, _ := newTestStore(t)
	if _, err := store.MintToken(""); err == nil {
		t.Fatal("MintToken accepted empty cluster")
	}
}

func TestReap_RemovesExpiredAndConsumed(t *testing.T) {
	store, clock := newTestStore(t)
	_, _ = store.MintToken("live")
	_, _ = store.MintToken("expired")
	consumed, _ := store.MintToken("consumed")
	_, _ = store.RedeemToken(consumed.Token, "consumed")

	clock.now = clock.now.Add(20 * time.Minute) // expires `expired` and `live`

	store.Reap()
	if got := store.Len(); got != 0 {
		t.Fatalf("after reap Len = %d, want 0 (live also expired now)", got)
	}
}
