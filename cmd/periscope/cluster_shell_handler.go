package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/auth"
	"github.com/gnana997/periscope/internal/authz"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/clustershell"
	"github.com/gnana997/periscope/internal/credentials"
	execsess "github.com/gnana997/periscope/internal/exec"
)

// clusterShellHandler returns a credentials.Handler that upgrades the
// request to a WebSocket and runs one cluster-shell session
// end-to-end. Mirrors exec_handler.go's structure — same cap-checks-
// before-upgrade discipline, same audit open/close pair, same
// apiErrorJSON shape — but provisions and tears down an ephemeral
// shell pod around the exec stream.
//
// Endpoint:
//
//	GET /api/clusters/{cluster}/shell
//	    ?mode=bash|kubectl-only       (optional; defaults to clusterShell.mode)
//
// Audit: emits cluster_shell_open and cluster_shell_close events
// through audit.Emitter. close Outcome reflects whether the session
// errored; close_reason rides in Reason.
//
// v1.2 constraints enforced here:
//   - clusterShell.enabled must be true on the dashboard
//   - cluster.Backend must be in-cluster or agent (other backends
//     return E_CLUSTER_SHELL_UNSUPPORTED; agent-backend was added
//     alongside in-cluster — see clustershell.CAReader for the
//     per-backend CA dispatch)
//   - authz.Resolver.Mode() must be tier — shared/raw return
//     E_CLUSTER_SHELL_REQUIRES_TIER
//   - operator's resolved tier must be in clusterShell.tiers
//   - kubectl-only mode is rejected with E_NOT_IMPLEMENTED (the REPL
//     ships in a follow-up PR; the image entrypoint refuses it too)
func clusterShellHandler(
	reg *clusters.Registry,
	sessions *clustershell.Registry,
	auditer *audit.Emitter,
	cfg clustershell.Config,
	resolver *authz.Resolver,
	caReader *clustershell.CAReader,
) credentials.Handler {
	slog.Info("cluster_shell lifecycle config",
		"enabled", cfg.Enabled,
		"default_mode", string(cfg.Mode),
		"tiers", cfg.Tiers,
		"namespace", cfg.Namespace,
		"idle_seconds", int(cfg.IdleTimeout.Seconds()),
		"pod_start_timeout_seconds", int(cfg.PodStartTimeout.Seconds()),
		"max_sessions_per_user", cfg.MaxSessionsPerUser,
		"max_sessions_total", cfg.MaxSessionsTotal,
		"image", cfg.ShellImage,
	)
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		// --- 1. Cluster lookup ---
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			apiErrorJSON(w, http.StatusNotFound, "E_NOT_FOUND", "cluster not found", nil)
			return
		}

		// --- 2. Feature gate ---
		if !cfg.Enabled {
			apiErrorJSON(w, http.StatusForbidden, "E_CLUSTER_SHELL_DISABLED",
				"cluster shell is not enabled on this dashboard", nil)
			return
		}

		// --- 3. Backend gate ---
		// v1.2 supports the in-cluster and agent backends. EKS-direct
		// and kubeconfig backends need shell-pod-placement design that
		// ships in v1.3.
		if c.Backend != clusters.BackendInCluster && c.Backend != clusters.BackendAgent {
			apiErrorJSON(w, http.StatusForbidden, "E_CLUSTER_SHELL_UNSUPPORTED",
				"cluster shell only supports in-cluster and agent backends in this release",
				map[string]any{"backend": c.Backend})
			return
		}

		// --- 4. Authz mode gate ---
		if resolver == nil || resolver.Mode() != authz.ModeTier {
			apiErrorJSON(w, http.StatusForbidden, "E_CLUSTER_SHELL_REQUIRES_TIER",
				"cluster shell requires auth.authorization.mode=tier",
				map[string]any{"current_mode": modeString(resolver)})
			return
		}

		// --- 5. Tier gate ---
		s, ok := auth.SessionFromContext(r.Context())
		if !ok {
			apiErrorJSON(w, http.StatusUnauthorized, "E_AUTH", "unauthenticated", nil)
			return
		}
		tier := resolver.ResolvedTier(authz.Identity{Subject: s.Subject, Groups: s.Groups})
		if !cfg.TierAllowed(tier) {
			apiErrorJSON(w, http.StatusForbidden, "E_FORBIDDEN",
				"your tier is not allowed to open a cluster shell",
				map[string]any{"tier": tier, "allowed_tiers": cfg.Tiers})
			return
		}

		// --- 6. Mode parse ---
		mode := clustershell.Mode(r.URL.Query().Get("mode"))
		if mode == "" {
			mode = cfg.Mode
		}
		switch mode {
		case clustershell.ModeBash:
			// OK
		case clustershell.ModeKubectlOnly:
			apiErrorJSON(w, http.StatusBadRequest, "E_NOT_IMPLEMENTED",
				"the kubectl-only REPL ships in a follow-up PR; set ?mode=bash or use the dashboard default", nil)
			return
		default:
			apiErrorJSON(w, http.StatusBadRequest, "E_INVALID_REQUEST",
				"unknown shell mode", map[string]any{"mode": string(mode), "want": []string{"bash"}})
			return
		}

		// --- 7. Concurrent caps ---
		actor := p.Actor()
		userCount := sessions.CountForActor(actor)
		if userCount >= cfg.MaxSessionsPerUser {
			active := capActiveClusterShellSessions(sessions.SnapshotForActor(actor))
			apiErrorJSON(w, http.StatusTooManyRequests, "E_CAP_USER",
				"you've hit your concurrent cluster-shell cap. close one to open another.",
				map[string]any{
					"limit":          cfg.MaxSessionsPerUser,
					"activeSessions": active,
				})
			return
		}
		clusterCount := sessions.CountForCluster(c.Name)
		if clusterCount >= cfg.MaxSessionsTotal {
			apiErrorJSON(w, http.StatusTooManyRequests, "E_CAP_CLUSTER",
				"this cluster has hit its total cluster-shell cap. try again shortly.",
				map[string]any{"limit": cfg.MaxSessionsTotal})
			return
		}

		// --- 8. WebSocket upgrade ---
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: originPatterns(),
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.WarnContext(r.Context(), "cluster_shell.upgrade failed",
				"err", err, "actor", actor, "cluster", c.Name)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "session ended")

		// --- 9. Construct session + register for cap accounting ---
		session := clustershell.New(c, p, tier, mode, cfg, caReader)
		entry := clustershell.SessionRecord{
			ID:        session.ID,
			Actor:     actor,
			Cluster:   c.Name,
			Tier:      tier,
			Mode:      mode,
			StartedAt: session.StartedAt,
		}
		if !sessions.Add(entry) {
			apiErrorJSON(w, http.StatusInternalServerError, "E_INTERNAL", "session id collision", nil)
			return
		}
		defer sessions.Remove(session.ID)

		// --- 10. Audit open ---
		actorRec := actorFromContext(r.Context())
		started := time.Now().UTC()
		auditer.Record(r.Context(), audit.Event{
			Actor:   actorRec,
			Verb:    audit.VerbClusterShellOpen,
			Outcome: audit.OutcomeSuccess,
			Cluster: c.Name,
			Extra: map[string]any{
				"session_id": session.ID,
				"mode":       string(mode),
				"tier":       tier,
				"namespace":  cfg.Namespace,
				"backend":    c.Backend,
				"started_at": started.Format(time.RFC3339Nano),
			},
		})

		// --- 11. Provision pod + Secret ---
		if err := session.Start(r.Context()); err != nil {
			// Distinguish well-known startup-failure classes so the
			// SPA can render a doc-linked banner instead of a generic
			// "couldn't open shell" message.
			code := "E_SHELL_POD_FAILED"
			reason := "pod_start_failed"
			if errors.Is(err, clustershell.ErrNoClusterCA) {
				code = "E_CLUSTER_SHELL_NO_CA"
				reason = "ca_unavailable"
			}
			slog.WarnContext(r.Context(), "cluster_shell start failed",
				"session_id", session.ID, "cluster", c.Name, "backend", c.Backend, "code", code, "err", err)
			emitClusterShellClose(r.Context(), auditer, clusterShellCloseArgs{
				Actor:       actorRec,
				Cluster:     c.Name,
				SessionID:   session.ID,
				Mode:        mode,
				Tier:        tier,
				StartedAt:   started,
				EndedAt:     time.Now().UTC(),
				ExitCode:    0,
				CloseReason: reason,
				Outcome:     audit.OutcomeFailure,
				ErrCode:     code,
				RunErr:      err,
			})
			_ = ws.Close(websocket.StatusInternalError, code)
			return
		}

		// --- 12. Bridge WS ↔ exec stream (blocks for session) ---
		result, stats, runErr := session.Attach(r.Context(), ws)
		ended := time.Now().UTC()

		// --- 13. Teardown: read audit file + delete Pod/Secret ---
		// Use a fresh background context — the request context may
		// already be cancelled (WS close path), but pod cleanup needs
		// its own budget to finish.
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmds, closeErr := session.Close(closeCtx)
		closeCancel()
		if closeErr != nil {
			slog.WarnContext(r.Context(), "cluster_shell teardown (best-effort)",
				"session_id", session.ID, "err", closeErr)
		}

		// --- 14. Audit close ---
		closeReason := result.Reason
		if runErr != nil {
			closeReason = "server_error"
		}
		if closeReason == "" {
			closeReason = "completed"
		}
		endOutcome := audit.OutcomeSuccess
		if runErr != nil {
			endOutcome = audit.OutcomeFailure
		}
		emitClusterShellClose(r.Context(), auditer, clusterShellCloseArgs{
			Actor:       actorRec,
			Cluster:     c.Name,
			SessionID:   session.ID,
			Mode:        mode,
			Tier:        tier,
			StartedAt:   started,
			EndedAt:     ended,
			ExitCode:    result.ExitCode,
			CloseReason: closeReason,
			Outcome:     endOutcome,
			Commands:    cmds,
			Stats:       stats,
			RunErr:      runErr,
		})
	}
}

// clusterShellCloseArgs is the typed arg bundle for the close-audit
// emit. Keeps the call site readable without a 12-arg function.
type clusterShellCloseArgs struct {
	Actor       audit.Actor
	Cluster     string
	SessionID   string
	Mode        clustershell.Mode
	Tier        string
	StartedAt   time.Time
	EndedAt     time.Time
	ExitCode    int
	CloseReason string
	Outcome     audit.Outcome
	Commands    []clustershell.CommandLine
	Stats       execsess.Stats
	ErrCode     string // populated only on startup-failure path
	RunErr      error
}

func emitClusterShellClose(ctx context.Context, auditer *audit.Emitter, a clusterShellCloseArgs) {
	extra := map[string]any{
		"session_id":     a.SessionID,
		"mode":           string(a.Mode),
		"tier":           a.Tier,
		"started_at":     a.StartedAt.Format(time.RFC3339Nano),
		"ended_at":       a.EndedAt.Format(time.RFC3339Nano),
		"duration_ms":    a.EndedAt.Sub(a.StartedAt).Milliseconds(),
		"exit_code":      a.ExitCode,
		"bytes_stdin":    a.Stats.BytesIn,
		"bytes_stdout":   a.Stats.BytesOut,
		"commands_count": len(a.Commands),
	}
	if len(a.Commands) > 0 {
		extra["commands"] = a.Commands
	}
	if a.RunErr != nil {
		extra["err"] = errString(a.RunErr)
	}
	if a.ErrCode != "" {
		extra["err_code"] = a.ErrCode
	}
	auditer.Record(ctx, audit.Event{
		Actor:   a.Actor,
		Verb:    audit.VerbClusterShellClose,
		Outcome: a.Outcome,
		Cluster: a.Cluster,
		Reason:  a.CloseReason,
		Extra:   extra,
	})
}

// capActiveClusterShellSessions reduces a SessionRecord snapshot to
// the slim view the cap-reached dialog renders. Mirrors
// capActiveSessions in exec_handler.go but carries the cluster-shell-
// specific fields (tier, mode) so the SPA can show what the operator
// already has open.
func capActiveClusterShellSessions(in []clustershell.SessionRecord) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, s := range in {
		out = append(out, map[string]any{
			"id":        s.ID,
			"cluster":   s.Cluster,
			"tier":      s.Tier,
			"mode":      string(s.Mode),
			"startedAt": s.StartedAt.Format(time.RFC3339Nano),
		})
	}
	return out
}

// modeString returns a printable form of the resolver's mode for
// error bodies. Handles the nil-resolver case so the
// E_CLUSTER_SHELL_REQUIRES_TIER body always carries a useful
// "current_mode" field.
func modeString(r *authz.Resolver) string {
	if r == nil {
		return "unconfigured"
	}
	return string(r.Mode())
}
