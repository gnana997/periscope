// Package clustershell implements the per-session ephemeral-pod
// machinery behind the in-browser cluster-shell feature (issue #104).
//
// One Session per WebSocket connection. Periscope main creates a
// short-lived Pod in the configured namespace (default
// periscope-system), mounts a per-session kubeconfig Secret with
// tier-narrow impersonation strings + an apiserver-audit annotation,
// attaches via existing k8s.ExecPod, and tears the pod down on
// session close. The kubeconfig delivered to the pod uses a
// TokenRequest-minted SA token bound to the pod's UID so the token
// auto-expires when the pod is deleted.
//
// v1.2 ships ModeTier-only and BackendInCluster-only — other authz
// modes and cluster backends are rejected at the handler. Both
// constraints relax in v1.3.
package clustershell

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Mode is the operator-facing shell-mode selector. v1.2 supports only
// ModeBash; the kubectl-only REPL ships in a follow-up PR and is
// rejected at the handler when chosen against this build.
type Mode string

const (
	ModeBash        Mode = "bash"
	ModeKubectlOnly Mode = "kubectl-only"
)

// IsKnown reports whether m is a valid Mode value.
func (m Mode) IsKnown() bool {
	return m == ModeBash || m == ModeKubectlOnly
}

// Config holds the cluster-shell knobs, loaded once at process start
// from env vars rendered by the Helm chart's `clusterShell:` block.
// Per-cluster overrides are not yet supported (single-cluster v1.2);
// add a ResolveForCluster mirror of internal/exec/config.go when the
// agent-tunnel / EKS backends ship.
type Config struct {
	// Enabled mirrors clusterShell.enabled in the Helm chart. Default
	// false — the feature is opt-in for security posture.
	Enabled bool

	// Mode is the default session mode. The handler accepts a ?mode=
	// query that overrides this, subject to IsKnown validation.
	Mode Mode

	// Tiers is the closed set of authz tier names that may open a
	// cluster-shell session. Operators not in one of these tiers get
	// E_FORBIDDEN from the handler.
	Tiers []string

	// Namespace is where the ephemeral shell pods + per-session
	// Secrets land. The chart pre-creates this namespace and the
	// per-tier ServiceAccounts inside it. Must NOT be the release
	// namespace.
	Namespace string

	// IdleTimeout closes sessions after this much stdin+stdout
	// silence. Same definition as pod-exec: heartbeats do NOT reset
	// the timer.
	IdleTimeout time.Duration

	// PodStartTimeout bounds the Pod-Ready poll loop. On expiry the
	// session returns E_SHELL_POD_TIMEOUT and the partially-created
	// pod is torn down.
	PodStartTimeout time.Duration

	// TranscriptMaxBytes caps the transcript blob included in the
	// cluster_shell_close audit row. Oversize transcripts are
	// truncated with a flag.
	TranscriptMaxBytes int64

	// TokenTTL is the requested lifetime of the per-session
	// ServiceAccount token. Matched to IdleTimeout so the token can
	// outlive the longest possible session but no longer.
	TokenTTL time.Duration

	// ShellImage is the pinned image reference for the shell pod
	// (clusterShell.image.repository + ":" + tag).
	ShellImage string

	// ImagePullPolicy is the imagePullPolicy applied to the shell
	// pod's container.
	ImagePullPolicy string

	// MaxSessionsPerUser caps concurrent shell sessions per operator
	// (matches the pod-exec pattern). Default 2.
	MaxSessionsPerUser int

	// MaxSessionsTotal caps concurrent shell sessions per cluster
	// across all operators. Default 10.
	MaxSessionsTotal int
}

// Defaults — match the discussions/periscope-cluster-shell-v1.2-plan.md
// values. Operators tune via env vars; the Helm chart renders these
// from the clusterShell: block in values.yaml.
const (
	defaultMode               = ModeBash
	defaultNamespace          = "periscope-system"
	defaultIdleTimeout        = 20 * time.Minute // longer than pod-exec's 10min — REPL thinking time
	defaultPodStartTimeout    = 30 * time.Second
	defaultTranscriptMaxBytes = int64(1 << 20) // 1 MiB
	defaultImagePullPolicy    = "IfNotPresent"
	defaultMaxSessionsPerUser = 2
	defaultMaxSessionsTotal   = 10
)

// LoadConfig reads the env-driven knobs and applies defaults for any
// missing or malformed values. Called once at handler construction.
func LoadConfig() Config {
	mode := Mode(strings.TrimSpace(os.Getenv("PERISCOPE_CLUSTER_SHELL_MODE")))
	if !mode.IsKnown() {
		mode = defaultMode
	}

	cfg := Config{
		Enabled:            boolEnv("PERISCOPE_CLUSTER_SHELL_ENABLED", false),
		Mode:               mode,
		Tiers:              csvEnv("PERISCOPE_CLUSTER_SHELL_TIERS"),
		Namespace:          stringEnv("PERISCOPE_CLUSTER_SHELL_NAMESPACE", defaultNamespace),
		IdleTimeout:        durationSecondsEnv("PERISCOPE_CLUSTER_SHELL_IDLE_SECONDS", defaultIdleTimeout),
		PodStartTimeout:    durationSecondsEnv("PERISCOPE_CLUSTER_SHELL_POD_START_TIMEOUT_SECONDS", defaultPodStartTimeout),
		TranscriptMaxBytes: int64Env("PERISCOPE_CLUSTER_SHELL_TRANSCRIPT_MAX_BYTES", defaultTranscriptMaxBytes),
		ShellImage:         strings.TrimSpace(os.Getenv("PERISCOPE_CLUSTER_SHELL_IMAGE")),
		ImagePullPolicy:    stringEnv("PERISCOPE_CLUSTER_SHELL_IMAGE_PULL_POLICY", defaultImagePullPolicy),
		MaxSessionsPerUser: intEnv("PERISCOPE_CLUSTER_SHELL_MAX_SESSIONS_PER_USER", defaultMaxSessionsPerUser),
		MaxSessionsTotal:   intEnv("PERISCOPE_CLUSTER_SHELL_MAX_SESSIONS_TOTAL", defaultMaxSessionsTotal),
	}
	// TokenTTL = IdleTimeout, so the SA token outlives the longest
	// possible session but no longer. Apiserver enforces a 10-min
	// floor on TokenRequest TTL, so clamp at that on the low end.
	cfg.TokenTTL = cfg.IdleTimeout
	if cfg.TokenTTL < 10*time.Minute {
		cfg.TokenTTL = 10 * time.Minute
	}
	return cfg
}

// Validate returns an error if the loaded config is internally
// inconsistent enough to refuse handler registration. Disabled
// configs pass validation (the handler simply hides the feature).
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.ShellImage == "" {
		return errors.New("PERISCOPE_CLUSTER_SHELL_IMAGE required when cluster-shell is enabled")
	}
	if len(c.Tiers) == 0 {
		return errors.New("PERISCOPE_CLUSTER_SHELL_TIERS required when cluster-shell is enabled (e.g. \"admin\")")
	}
	if c.Namespace == "" {
		return fmt.Errorf("PERISCOPE_CLUSTER_SHELL_NAMESPACE must be non-empty")
	}
	return nil
}

// TierAllowed reports whether the supplied tier name is in the
// configured allow-list.
func (c Config) TierAllowed(tier string) bool {
	for _, t := range c.Tiers {
		if t == tier {
			return true
		}
	}
	return false
}

// --- env helpers ---

func stringEnv(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func boolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return v
}

func intEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func int64Env(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func durationSecondsEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}

// csvEnv parses a comma-separated env var into a slice, dropping
// empty entries. Returns nil if the env is empty/unset.
func csvEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
