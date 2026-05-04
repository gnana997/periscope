package tunnel

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Bootstrap-token store. Tokens are short-lived (15-min default),
// single-use, and bound to a specific cluster name at mint time.
//
// Lifecycle:
//   1. Operator (admin) calls MintToken(cluster=prod-eu) → opaque
//      string handed to the agent installer.
//   2. Agent calls RedeemToken(token, cluster=prod-eu) during
//      registration; on success the cluster's CSR gets signed.
//   3. Token is marked consumed; any subsequent RedeemToken with the
//      same value returns ErrTokenConsumed.
//
// Why server-side state instead of self-contained JWTs:
//   - Single-use needs a side store anyway (a JWT can be replayed
//     unless we maintain a "used" set).
//   - Revoke = delete the entry; no key rollover.
//   - For v1.x.0 single-replica, in-memory is plenty. The Reader
//     interface is shaped so a Postgres-backed v1.x successor can
//     drop in without touching the registration handler.
//
// Cluster-name binding is intentional: a token leaked from
// `kubectl apply` of cluster A's manifest can only register cluster A,
// not impersonate cluster B. (The agent has to present the correct
// matching name when redeeming.)

// ErrTokenInvalid is returned when the token doesn't exist (never
// minted, expired and reaped, or already consumed).
var ErrTokenInvalid = errors.New("tunnel token: invalid")

// ErrTokenConsumed is returned when the token was previously redeemed.
// Distinct from ErrTokenInvalid so the registration handler can log
// the difference.
var ErrTokenConsumed = errors.New("tunnel token: already consumed")

// ErrTokenExpired is returned when the token's TTL has elapsed.
var ErrTokenExpired = errors.New("tunnel token: expired")

// ErrTokenClusterMismatch is returned when the redeeming agent
// claims a different cluster name than the token was minted for.
var ErrTokenClusterMismatch = errors.New("tunnel token: cluster name mismatch")

// TokenIssuance is the payload returned to the operator at mint time.
// The Token is the opaque string the agent presents on registration;
// ExpiresAt lets the UI show a countdown.
type TokenIssuance struct {
	Token     string    `json:"token"`
	Cluster   string    `json:"cluster"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// TokenStoreOptions configures token TTL and reaper cadence.
type TokenStoreOptions struct {
	// TTL is how long a freshly-minted token remains valid before
	// expiring. Default 15 minutes.
	TTL time.Duration

	// ReapInterval is how often the background goroutine scans for
	// expired/consumed tokens to evict from the map. Default 5 min.
	// Setting to 0 disables the reaper (tests use this so the test
	// goroutine doesn't outlive the test).
	ReapInterval time.Duration
}

func (o TokenStoreOptions) withDefaults() TokenStoreOptions {
	if o.TTL == 0 {
		o.TTL = 15 * time.Minute
	}
	if o.ReapInterval < 0 {
		o.ReapInterval = 0
	}
	if o.ReapInterval == 0 {
		o.ReapInterval = 5 * time.Minute
	}
	return o
}

type tokenEntry struct {
	cluster   string
	expiresAt time.Time
	consumed  bool
}

// TokenStore is the in-memory bootstrap-token registry. Safe for
// concurrent use.
type TokenStore struct {
	opts TokenStoreOptions

	mu     sync.Mutex
	tokens map[string]*tokenEntry

	// now is time.Now by default; tests inject a clock to fast-
	// forward expiry without sleeping.
	now func() time.Time
}

// NewTokenStore returns a fresh store. Pass disableReaper=true in
// tests if you don't want the background goroutine.
func NewTokenStore(opts TokenStoreOptions) *TokenStore {
	o := opts.withDefaults()
	s := &TokenStore{
		opts:   o,
		tokens: make(map[string]*tokenEntry),
		now:    time.Now,
	}
	return s
}

// SetClock replaces the time source. Tests only.
func (s *TokenStore) SetClock(now func() time.Time) { s.now = now }

// MintToken creates a fresh single-use token for the named cluster.
// The token is 32 random bytes, base64url-encoded (no padding) so
// it's safe to embed in URLs / `--set token=...` Helm values.
func (s *TokenStore) MintToken(cluster string) (TokenIssuance, error) {
	if cluster == "" {
		return TokenIssuance{}, errors.New("tunnel token: cluster is required")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return TokenIssuance{}, fmt.Errorf("tunnel token: rand: %w", err)
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	exp := s.now().Add(s.opts.TTL)

	s.mu.Lock()
	s.tokens[tok] = &tokenEntry{cluster: cluster, expiresAt: exp}
	s.mu.Unlock()

	return TokenIssuance{Token: tok, Cluster: cluster, ExpiresAt: exp}, nil
}

// RedeemToken atomically validates and consumes the token. Returns
// the cluster name the token was bound to on success.
//
// Returns one of:
//   - ErrTokenInvalid    — token unknown
//   - ErrTokenConsumed   — token previously redeemed
//   - ErrTokenExpired    — TTL elapsed
//   - ErrTokenClusterMismatch — claimed cluster differs from minted
//
// The token is marked consumed even on cluster mismatch so a wrong-
// guess from an attacker burns the token (the operator has to mint
// a new one anyway).
func (s *TokenStore) RedeemToken(token, claimedCluster string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.tokens[token]
	if !ok {
		return "", ErrTokenInvalid
	}
	if entry.consumed {
		return "", ErrTokenConsumed
	}
	if s.now().After(entry.expiresAt) {
		entry.consumed = true
		return "", ErrTokenExpired
	}
	if claimedCluster != entry.cluster {
		entry.consumed = true
		return "", ErrTokenClusterMismatch
	}
	entry.consumed = true
	return entry.cluster, nil
}

// Reap removes expired and consumed entries from the map. Called by
// the background goroutine started by Run; tests call this directly.
func (s *TokenStore) Reap() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for tok, e := range s.tokens {
		if e.consumed || now.After(e.expiresAt) {
			delete(s.tokens, tok)
		}
	}
}

// Run starts the background reaper. Blocks until ctx is cancelled.
// Caller decides whether to start it (production: yes; tests: no).
func (s *TokenStore) Run(stop <-chan struct{}) {
	if s.opts.ReapInterval <= 0 {
		return
	}
	t := time.NewTicker(s.opts.ReapInterval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.Reap()
		}
	}
}

// Len returns the current entry count (live + consumed-but-not-reaped).
// Tests use this; production might use it as a gauge.
func (s *TokenStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tokens)
}
