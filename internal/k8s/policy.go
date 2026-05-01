package k8s

import (
	"sync"
	"time"
)

// PolicyTransport is the chosen exec transport for a single attempt. It
// doubles as the audit "transport" field on session_end records.
type PolicyTransport string

const (
	// TransportWS uses the v5.channel.k8s.io WebSocket subprotocol
	// (kubectl's default since 1.31).
	TransportWS PolicyTransport = "ws_v5"
	// TransportSPDY uses the legacy SPDY/3.1 framing. Required for
	// apiserver versions older than the WS-v5 transition (~1.30).
	TransportSPDY PolicyTransport = "spdy"
)

// PolicyMode is what Choose returns to the executor: either "try WS,
// fall back to SPDY on upgrade error" or "go straight to SPDY because
// this cluster has been pinned."
type PolicyMode int

const (
	// ModeWSThenSPDY tries WebSocket first and falls back to SPDY on
	// upgrade failure. Records the result so consecutive WS failures
	// pin the cluster to SPDY-only mode for a TTL.
	ModeWSThenSPDY PolicyMode = iota
	// ModeSPDYOnly skips the WS handshake entirely. Saves the round
	// trip on clusters known to be too old for v5.
	ModeSPDYOnly
)

// PolicyConfig tunes the circuit breaker. Defaults applied when zero.
type PolicyConfig struct {
	// FailThreshold is the number of consecutive WS upgrade failures
	// that trip the breaker for a cluster. Default 3.
	FailThreshold int
	// PinDuration is how long ModeSPDYOnly stays sticky after the
	// breaker trips. Default 30 minutes.
	PinDuration time.Duration
}

func (cfg PolicyConfig) withDefaults() PolicyConfig {
	if cfg.FailThreshold <= 0 {
		cfg.FailThreshold = 3
	}
	if cfg.PinDuration <= 0 {
		cfg.PinDuration = 30 * time.Minute
	}
	return cfg
}

// Policy is the per-cluster circuit breaker registry. One process-wide
// instance keyed by cluster name; concurrent-safe.
//
// State per cluster:
//   - consecutiveWSFails: number of WS upgrade failures since the last
//     successful WS connection (or since process start).
//   - pinnedToSPDYUntil: when set in the future, Choose returns
//     ModeSPDYOnly until this time elapses, then probes WS once.
type Policy struct {
	cfg     PolicyConfig
	mu      sync.Mutex
	state   map[string]*policyState
	nowFunc func() time.Time
}

type policyState struct {
	consecutiveWSFails int
	pinnedToSPDYUntil  time.Time
}

// NewPolicy returns a fresh Policy with the supplied config (defaults
// applied for zero-value fields).
func NewPolicy(cfg PolicyConfig) *Policy {
	return &Policy{
		cfg:     cfg.withDefaults(),
		state:   make(map[string]*policyState),
		nowFunc: time.Now,
	}
}

// Choose returns the transport mode the next attempt should use for the
// given cluster. Caller passes the result back via RecordResult so the
// breaker observes the outcome.
func (p *Policy) Choose(cluster string) PolicyMode {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.getOrInitLocked(cluster)
	if p.nowFunc().Before(st.pinnedToSPDYUntil) {
		return ModeSPDYOnly
	}
	return ModeWSThenSPDY
}

// RecordResult feeds the breaker the outcome of an attempt. wsFailed is
// true when a WS upgrade was attempted and rejected (httpstream upgrade
// error); finalSucceeded reports whether the overall stream succeeded
// (via WS or SPDY fallback).
//
// State machine:
//   - WS failed and finalSucceeded → bump fail counter; if counter hits
//     threshold, pin to SPDY for PinDuration.
//   - WS succeeded (any) → reset fail counter, clear any pin.
//   - Final failure (both transports failed) → leave counter alone, the
//     next attempt will still pay the WS-handshake tax.
func (p *Policy) RecordResult(cluster string, mode PolicyMode, wsFailed, finalSucceeded bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.getOrInitLocked(cluster)
	switch {
	case mode == ModeWSThenSPDY && !wsFailed && finalSucceeded:
		// WS succeeded. Reset everything.
		st.consecutiveWSFails = 0
		st.pinnedToSPDYUntil = time.Time{}
	case mode == ModeWSThenSPDY && wsFailed && finalSucceeded:
		// SPDY fallback worked but WS didn't. Tally and consider pinning.
		st.consecutiveWSFails++
		if st.consecutiveWSFails >= p.cfg.FailThreshold {
			st.pinnedToSPDYUntil = p.nowFunc().Add(p.cfg.PinDuration)
		}
	case mode == ModeSPDYOnly && finalSucceeded:
		// We were pinned and SPDY worked — no signal about WS yet. The
		// pin will probe again when it expires.
	}
}

// State returns a copy of the breaker state for the cluster. Useful for
// observability/debug endpoints. Returns zero values when the cluster
// has not been seen yet.
func (p *Policy) State(cluster string) (consecutiveWSFails int, pinnedUntil time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st, ok := p.state[cluster]
	if !ok {
		return 0, time.Time{}
	}
	return st.consecutiveWSFails, st.pinnedToSPDYUntil
}

// SetNowFunc lets tests inject a deterministic clock.
func (p *Policy) SetNowFunc(fn func() time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if fn == nil {
		fn = time.Now
	}
	p.nowFunc = fn
}

func (p *Policy) getOrInitLocked(cluster string) *policyState {
	st, ok := p.state[cluster]
	if !ok {
		st = &policyState{}
		p.state[cluster] = st
	}
	return st
}
