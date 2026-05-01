package exec

import (
	"os"
	"strconv"
	"time"
)

// Config holds the lifecycle knobs for a session: how often we ping, how
// long stdin/stdout silence counts as idle, and how much warning the
// browser gets before the server tears the session down.
//
// Loaded once at process start from environment variables. Per-cluster
// overrides will land in PR4 (RFC 0001 §14); the env knobs exist now so
// dev can validate idle/heartbeat behavior in seconds rather than minutes.
type Config struct {
	HeartbeatInterval time.Duration
	IdleTimeout       time.Duration
	IdleWarnLead      time.Duration
}

// Defaults from RFC 0001 §11. Operator-tunable thresholds, not security-
// critical bounds — values outside the conservative range still produce a
// working session, just with less typical behavior.
const (
	defaultHeartbeatInterval = 20 * time.Second
	defaultIdleTimeout       = 10 * time.Minute
	defaultIdleWarnLead      = 30 * time.Second
)

// LoadConfig reads the env-driven knobs and applies defaults for anything
// missing or malformed. Intended to be called once at handler-construction
// time and threaded into session.Run.
func LoadConfig() Config {
	return Config{
		HeartbeatInterval: durationSecondsEnv("PERISCOPE_EXEC_HEARTBEAT_SECONDS", defaultHeartbeatInterval),
		IdleTimeout:       durationSecondsEnv("PERISCOPE_EXEC_IDLE_SECONDS", defaultIdleTimeout),
		IdleWarnLead:      durationSecondsEnv("PERISCOPE_EXEC_IDLE_WARN_SECONDS", defaultIdleWarnLead),
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
