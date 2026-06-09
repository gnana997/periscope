// Package nodeshell holds the configuration and concurrency accounting
// for the in-browser SSM node shell (#105). The session mechanics live
// in internal/awsssm; the HTTP handler in cmd/periscope wires the two
// together. Kept separate so config + caps are unit-testable without the
// AWS surface.
package nodeshell

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gnana997/periscope/internal/clusters"
)

const (
	defaultIdleSeconds   = 600
	defaultTranscriptMax = 1 << 20 // 1 MiB
	defaultMaxPerUser    = 2
	defaultMaxTotal      = 10
)

// Config is the global node-shell configuration, loaded from the
// Helm-set environment. Per-cluster overrides (role ARN / audience /
// region) live on clusters.Cluster.NodeShell and are merged by Resolve.
type Config struct {
	Enabled            bool
	AWSRoleArn         string   // global default role ARN (single-account fleets)
	OIDCAudience       string   // global default expected aud (the OIDC client_id)
	Region             string   // global default region
	Tiers              []string // SPA-side tier gate; the backend re-checks it
	IdleTimeout        time.Duration
	TranscriptMaxBytes int64
	PluginPath         string
	MaxSessionsPerUser int
	MaxSessionsTotal   int
}

// LoadConfig reads the global node-shell config from the environment.
// Off by default — it's an opt-in security feature.
func LoadConfig() Config {
	return Config{
		Enabled:            envBool("PERISCOPE_NODE_SHELL_ENABLED", false),
		AWSRoleArn:         os.Getenv("PERISCOPE_NODE_SHELL_ROLE_ARN"),
		OIDCAudience:       os.Getenv("PERISCOPE_NODE_SHELL_OIDC_AUDIENCE"),
		Region:             os.Getenv("PERISCOPE_NODE_SHELL_REGION"),
		Tiers:              envList("PERISCOPE_NODE_SHELL_TIERS", []string{"admin"}),
		IdleTimeout:        time.Duration(envInt("PERISCOPE_NODE_SHELL_IDLE_SECONDS", defaultIdleSeconds)) * time.Second,
		TranscriptMaxBytes: int64(envInt("PERISCOPE_NODE_SHELL_TRANSCRIPT_MAX_BYTES", defaultTranscriptMax)),
		PluginPath:         os.Getenv("PERISCOPE_NODE_SHELL_PLUGIN_PATH"),
		MaxSessionsPerUser: envInt("PERISCOPE_NODE_SHELL_MAX_SESSIONS_PER_USER", defaultMaxPerUser),
		MaxSessionsTotal:   envInt("PERISCOPE_NODE_SHELL_MAX_SESSIONS_TOTAL", defaultMaxTotal),
	}
}

// Resolved is the effective per-cluster AWS config after merging the
// global defaults with a cluster's NodeShell override.
type Resolved struct {
	RoleArn      string
	OIDCAudience string
	Region       string
}

// Resolve merges the global config with a cluster's NodeShell override.
// Region additionally falls back to the cluster's own Region (the
// handler falls back further to the region label on the Node).
func (c Config) Resolve(cl clusters.Cluster) Resolved {
	r := Resolved{RoleArn: c.AWSRoleArn, OIDCAudience: c.OIDCAudience, Region: c.Region}
	if cl.NodeShell != nil {
		if cl.NodeShell.AWSRoleArn != "" {
			r.RoleArn = cl.NodeShell.AWSRoleArn
		}
		if cl.NodeShell.OIDCAudience != "" {
			r.OIDCAudience = cl.NodeShell.OIDCAudience
		}
		if cl.NodeShell.Region != "" {
			r.Region = cl.NodeShell.Region
		}
	}
	if r.Region == "" {
		r.Region = cl.Region
	}
	return r
}

// TierAllowed reports whether tier is in the configured allow-list.
func (c Config) TierAllowed(tier string) bool {
	for _, t := range c.Tiers {
		if t == tier {
			return true
		}
	}
	return false
}

func envBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func envList(key string, def []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}
