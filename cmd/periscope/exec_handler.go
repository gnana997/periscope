package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	execsess "github.com/gnana997/periscope/internal/exec"
	"github.com/gnana997/periscope/internal/k8s"
)

// execHandler returns a credentials.Handler that upgrades the request to a
// WebSocket and runs a single pod exec session through it.
//
// Endpoint:
//
//	GET /api/clusters/{cluster}/pods/{ns}/{name}/exec
//	    ?container=<name>            (optional; default-container annotation else first non-init)
//	    &command=<base64-json>       (optional; default shell command)
//	    &tty=true|false              (optional; default true)
//
// Audit: emits exec_open and exec_close events through audit.Emitter.
// session_end Outcome reflects whether the run errored; close_reason
// rides in Reason. See RFC 0001 10 for the historical schema; field
// names are preserved by audit.StdoutSink.
//
// PR4 additions:
//   - Per-cluster Exec config (clusters.Cluster.Exec) overrides global
//     defaults for idle/heartbeat/caps.
//   - Concurrent caps enforced PRE-upgrade (HTTP 429) so we don't pay
//     the WS handshake cost when the user has nothing left to spend.
//   - Error taxonomy via apiErrorJSON: 4xx/5xx responses carry
//     {code, message[, activeSessions]} so the SPA can render purpose-
//     built UX (cap dialog, "exec disabled" tag, etc.).
//   - Audit "transport" field reports which wire (ws_v5 vs spdy)
//     actually carried the stream.
func execHandler(reg *clusters.Registry, sessions *execsess.Registry, policy *k8s.Policy, auditer *audit.Emitter) credentials.Handler {
	cfg := execsess.LoadConfig()
	slog.Info("exec lifecycle config",
		"heartbeat_seconds", int(cfg.HeartbeatInterval.Seconds()),
		"idle_seconds", int(cfg.IdleTimeout.Seconds()),
		"idle_warn_seconds", int(cfg.IdleWarnLead.Seconds()),
		"max_sessions_per_user", cfg.MaxSessionsPerUser,
		"max_sessions_total", cfg.MaxSessionsTotal,
	)
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			apiErrorJSON(w, http.StatusNotFound, "E_NOT_FOUND", "cluster not found", nil)
			return
		}
		ns := chi.URLParam(r, "ns")
		pod := chi.URLParam(r, "name")
		if ns == "" || pod == "" {
			apiErrorJSON(w, http.StatusBadRequest, "E_INVALID_REQUEST", "namespace and pod required", nil)
			return
		}

		// Per-cluster overrides win over global config.
		effectiveCfg := cfg
		if c.Exec != nil {
			effectiveCfg = cfg.ResolveForCluster(execsess.OverridesFromCluster(
				c.Exec.IdleSeconds,
				c.Exec.IdleWarnSeconds,
				c.Exec.HeartbeatSeconds,
				c.Exec.MaxSessionsPerUser,
				c.Exec.MaxSessionsTotal,
			))
		}

		// Cluster-level disable check. Lives here so the same response
		// shape works for both "you can't exec on prod" and "cluster
		// disappeared between page-load and click."
		if !c.ExecEnabled() {
			apiErrorJSON(w, http.StatusForbidden, "E_EXEC_DISABLED",
				"pod exec is disabled on this cluster", nil)
			return
		}

		// Cap checks BEFORE the WS upgrade — saves the handshake cost on
		// rejection and lets us return a JSON body the SPA can read.
		actor := p.Actor()
		userCount := sessions.CountForActor(actor)
		if userCount >= effectiveCfg.MaxSessionsPerUser {
			active := capActiveSessions(sessions.SnapshotForActor(actor))
			apiErrorJSON(w, http.StatusTooManyRequests, "E_CAP_USER",
				"you've hit your concurrent shell cap. close one to open another.",
				map[string]any{
					"limit":          effectiveCfg.MaxSessionsPerUser,
					"activeSessions": active,
				})
			return
		}
		clusterCount := sessions.CountForCluster(c.Name)
		if clusterCount >= effectiveCfg.MaxSessionsTotal {
			apiErrorJSON(w, http.StatusTooManyRequests, "E_CAP_CLUSTER",
				"this cluster has hit its total shell cap. try again shortly.",
				map[string]any{"limit": effectiveCfg.MaxSessionsTotal})
			return
		}

		q := r.URL.Query()
		container := q.Get("container")
		tty := q.Get("tty") != "false" // default true

		command, err := decodeCommand(q.Get("command"))
		if err != nil {
			apiErrorJSON(w, http.StatusBadRequest, "E_INVALID_REQUEST",
				"invalid command parameter", nil)
			return
		}

		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: originPatterns(),
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.WarnContext(r.Context(), "exec.upgrade failed",
				"err", err, "actor", actor, "cluster", c.Name, "ns", ns, "pod", pod)
			return // websocket.Accept already wrote the HTTP error
		}
		// Best-effort close. session.Run writes its own {type:closed} frame
		// before this fires, so a normal close is what we want here.
		defer ws.Close(websocket.StatusNormalClosure, "session ended")

		sessionID := uuid.NewString()
		params := execsess.Params{
			SessionID: sessionID,
			Actor:     actor,
			Cluster:   c,
			Namespace: ns,
			Pod:       pod,
			Container: container,
			Command:   command,
			TTY:       tty,
		}

		started := time.Now().UTC()
		entry := execsess.Session{
			ID:        sessionID,
			Actor:     params.Actor,
			Cluster:   c.Name,
			Namespace: ns,
			Pod:       pod,
			Container: container,
			StartedAt: started,
		}
		if !sessions.Add(entry) {
			apiErrorJSON(w, http.StatusInternalServerError, "E_INTERNAL",
				"session id collision", nil)
			return
		}
		defer sessions.Remove(sessionID)

		// session_start: emitted before the long-lived Run call so
		// an operator can see who opened a shell even if the
		// process never returns (network drop, hung command).
		execActor := actorFromContext(r.Context())
		execResource := audit.ResourceRef{
			Group: "", Version: "v1", Resource: "pods",
			Namespace: ns, Name: pod,
		}
		auditer.Record(r.Context(), audit.Event{
			Actor:    execActor,
			Verb:     audit.VerbExecOpen,
			Outcome:  audit.OutcomeSuccess,
			Cluster:  c.Name,
			Resource: execResource,
			Extra: map[string]any{
				"session_id":   sessionID,
				"container":    container,
				"tty":          tty,
				"command":      command,
				"k8s_identity": k8sIdentityLabel(c),
				"started_at":   started.Format(time.RFC3339Nano),
			},
		})

		result, stats, runErr := execsess.Run(r.Context(), ws, p, params, effectiveCfg, policy)
		ended := time.Now().UTC()

		closeReason := result.Reason
		if runErr != nil {
			switch {
			case errors.Is(runErr, http.ErrAbortHandler):
				closeReason = "abort"
			default:
				closeReason = "server_error"
			}
		}
		if closeReason == "" {
			closeReason = "completed"
		}

		resolvedContainer := result.Resolved.Container
		if resolvedContainer == "" {
			resolvedContainer = container
		}

		// session_end Outcome reflects whether Run returned an
		// error. close_reason is the human-friendly disposition
		// (completed / idle_timeout / abort / server_error) and
		// rides in Reason so the same field carries the answer to
		// "why did this end" across success and failure.
		endOutcome := audit.OutcomeSuccess
		if runErr != nil {
			endOutcome = audit.OutcomeFailure
		}
		auditer.Record(r.Context(), audit.Event{
			Actor:    execActor,
			Verb:     audit.VerbExecClose,
			Outcome:  endOutcome,
			Cluster:  c.Name,
			Resource: execResource,
			Reason:   closeReason,
			Extra: map[string]any{
				"session_id":   sessionID,
				"container":    resolvedContainer,
				"tty":          tty,
				"command":      commandOrResolved(command, result.Resolved.Command),
				"k8s_identity": k8sIdentityLabel(c),
				"transport":    string(result.Transport),
				"started_at":   started.Format(time.RFC3339Nano),
				"ended_at":     ended.Format(time.RFC3339Nano),
				"duration_ms":  ended.Sub(started).Milliseconds(),
				"exit_code":    result.ExitCode,
				"bytes_stdin":  stats.BytesIn,
				"bytes_stdout": stats.BytesOut,
				"err":          errString(runErr),
			},
		})
	}
}

// apiErrorJSON writes a structured JSON error response so the SPA can
// switch on `code` instead of parsing free-text. extras is merged into
// the body so caller-specific fields (activeSessions, limit, etc.) ride
// along on the same envelope.
func apiErrorJSON(w http.ResponseWriter, status int, code, message string, extras map[string]any) {
	body := map[string]any{
		"code":    code,
		"message": message,
	}
	for k, v := range extras {
		body[k] = v
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// capActiveSessions reduces a snapshot of Session records to the slim
// view the cap-reached dialog renders. Avoids leaking unrelated fields
// (actor, etc.) and keeps the response compact.
func capActiveSessions(in []execsess.Session) []map[string]any {
	out := make([]map[string]any, 0, len(in))
	for _, s := range in {
		out = append(out, map[string]any{
			"id":        s.ID,
			"cluster":   s.Cluster,
			"namespace": s.Namespace,
			"pod":       s.Pod,
			"container": s.Container,
			"startedAt": s.StartedAt.Format(time.RFC3339Nano),
		})
	}
	return out
}

// decodeCommand parses the optional `command` query parameter. The wire
// format is base64-encoded JSON of a string array, matching how `kubectl`
// expects argv. Empty input means "use the default shell command" and is
// not an error.
func decodeCommand(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		// Tolerate standard base64 too — devs reaching for `base64` on the
		// command line will produce padded output.
		decoded, err = base64.StdEncoding.DecodeString(raw)
		if err != nil {
			return nil, err
		}
	}
	var cmd []string
	if err := json.Unmarshal(decoded, &cmd); err != nil {
		return nil, err
	}
	if len(cmd) == 0 {
		return nil, nil
	}
	return cmd, nil
}

// originPatterns returns the WS origin allowlist. v1 default is same-origin
// only (empty patterns slice = coder/websocket's strict same-origin check).
// PERISCOPE_DEV_ALLOW_ORIGINS=foo.com,bar.com expands the allowlist for
// local dev and embedded scenarios.
func originPatterns() []string {
	raw := os.Getenv("PERISCOPE_DEV_ALLOW_ORIGINS")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// k8sIdentityLabel returns the audit-friendly identity tag described in
// RFC 0001 10. v1 only knows two shapes; v2 will inject the IDC ARN here.
func k8sIdentityLabel(c clusters.Cluster) string {
	switch c.Backend {
	case clusters.BackendKubeconfig:
		return "kubeconfig:" + c.Name
	case clusters.BackendInCluster:
		return "in-cluster:" + c.Name
	default:
		return "shared-irsa-v1"
	}
}

// commandOrResolved prefers the resolved command (post-defaulting) for
// audit, falling back to the user-supplied command if resolution failed
// before the executor ran.
func commandOrResolved(supplied, resolved []string) []string {
	if len(resolved) > 0 {
		return resolved
	}
	return supplied
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
