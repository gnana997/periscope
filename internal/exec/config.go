package exec

import (
	"os"
	"strconv"
	"time"
)

// Config holds the lifecycle knobs for a session: how often we ping,
// how long stdin/stdout silence counts as idle, how much warning the
// browser gets before the server tears the session down, and the
// concurrent-session caps that gate WebSocket upgrades.
//
// Loaded once at process start from environment variables. Per-cluster
// overrides for these fields are layered in by the HTTP handler: it
// reads the cluster's Exec block (clusters.Exec) and merges it on top
// of these globals before threading the resolved values into Run.
type Config struct {
	HeartbeatInterval  time.Duration
	IdleTimeout        time.Duration
	IdleWarnLead       time.Duration
	MaxSessionsPerUser int
	MaxSessionsTotal   int
}

// Defaults from RFC 0001 §11. Operator-tunable thresholds, not security-
// critical bounds — values outside the conservative range still produce a
// working session, just with less typical behavior.
const (
	defaultHeartbeatInterval  = 20 * time.Second
	defaultIdleTimeout        = 10 * time.Minute
	defaultIdleWarnLead       = 30 * time.Second
	defaultMaxSessionsPerUser = 5
	defaultMaxSessionsTotal   = 50
)

// LoadConfig reads the env-driven knobs and applies defaults for anything
// missing or malformed. Intended to be called once at handler-construction
// time and threaded into session.Run.
func LoadConfig() Config {
	return Config{
		HeartbeatInterval:  durationSecondsEnv("PERISCOPE_EXEC_HEARTBEAT_SECONDS", defaultHeartbeatInterval),
		IdleTimeout:        durationSecondsEnv("PERISCOPE_EXEC_IDLE_SECONDS", defaultIdleTimeout),
		IdleWarnLead:       durationSecondsEnv("PERISCOPE_EXEC_IDLE_WARN_SECONDS", defaultIdleWarnLead),
		MaxSessionsPerUser: intEnv("PERISCOPE_EXEC_MAX_SESSIONS_PER_USER", defaultMaxSessionsPerUser),
		MaxSessionsTotal:   intEnv("PERISCOPE_EXEC_MAX_SESSIONS_TOTAL", defaultMaxSessionsTotal),
	}
}

// ResolveForCluster applies any per-cluster overrides on top of the
// global config. nil overrides leave the global as-is; a non-nil
// pointer with a positive value wins. Negative or zero pointers are
// treated as "operator typo, ignore" — the global default stays.
func (c Config) ResolveForCluster(o *clusterExecOverrides) Config {
	if o == nil {
		return c
	}
	if o.IdleSeconds != nil && *o.IdleSeconds > 0 {
		c.IdleTimeout = time.Duration(*o.IdleSeconds) * time.Second
	}
	if o.IdleWarnSeconds != nil && *o.IdleWarnSeconds > 0 {
		c.IdleWarnLead = time.Duration(*o.IdleWarnSeconds) * time.Second
	}
	if o.HeartbeatSeconds != nil && *o.HeartbeatSeconds > 0 {
		c.HeartbeatInterval = time.Duration(*o.HeartbeatSeconds) * time.Second
	}
	if o.MaxSessionsPerUser != nil && *o.MaxSessionsPerUser > 0 {
		c.MaxSessionsPerUser = *o.MaxSessionsPerUser
	}
	if o.MaxSessionsTotal != nil && *o.MaxSessionsTotal > 0 {
		c.MaxSessionsTotal = *o.MaxSessionsTotal
	}
	return c
}

// clusterExecOverrides is the structural projection of clusters.ExecConfig
// that ResolveForCluster needs. Defined here so the exec package
// doesn't import the clusters package directly (the handler does the
// translation), keeping the dependency graph honest.
type clusterExecOverrides struct {
	IdleSeconds        *int
	IdleWarnSeconds    *int
	HeartbeatSeconds   *int
	MaxSessionsPerUser *int
	MaxSessionsTotal   *int
}

// OverridesFromCluster is the conversion helper used by the handler. It
// is the only place the exec package and clusters package "meet" at the
// type level.
func OverridesFromCluster(idleSeconds, idleWarnSeconds, heartbeatSeconds, maxPerUser, maxTotal *int) *clusterExecOverrides {
	if idleSeconds == nil && idleWarnSeconds == nil && heartbeatSeconds == nil &&
		maxPerUser == nil && maxTotal == nil {
		return nil
	}
	return &clusterExecOverrides{
		IdleSeconds:        idleSeconds,
		IdleWarnSeconds:    idleWarnSeconds,
		HeartbeatSeconds:   heartbeatSeconds,
		MaxSessionsPerUser: maxPerUser,
		MaxSessionsTotal:   maxTotal,
	}
}

func durationSecondsEnv(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}

func intEnv(key string, fallback int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
