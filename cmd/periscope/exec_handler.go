package main

import (
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

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	execsess "github.com/gnana997/periscope/internal/exec"
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
// Audit: emits structured slog records on session_start and session_end with
// category=audit. See RFC 0001 §10 for the schema.
func execHandler(reg *clusters.Registry, sessions *execsess.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := chi.URLParam(r, "ns")
		pod := chi.URLParam(r, "name")
		if ns == "" || pod == "" {
			http.Error(w, "namespace and pod required", http.StatusBadRequest)
			return
		}

		q := r.URL.Query()
		container := q.Get("container")
		tty := q.Get("tty") != "false" // default true

		command, err := decodeCommand(q.Get("command"))
		if err != nil {
			http.Error(w, "invalid command parameter", http.StatusBadRequest)
			return
		}

		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: originPatterns(),
			// stdout chunks come in 4–32 KiB; 1 MiB is plenty of headroom.
			// Mainly bounds stdin frames the browser is allowed to send.
		})
		if err != nil {
			slog.WarnContext(r.Context(), "exec.upgrade failed",
				"err", err, "actor", p.Actor(), "cluster", c.Name, "ns", ns, "pod", pod)
			return // websocket.Accept already wrote the HTTP error
		}
		// Best-effort close. session.Run writes its own {type:closed} frame
		// before this fires, so a normal close is what we want here.
		defer ws.Close(websocket.StatusNormalClosure, "session ended")

		sessionID := uuid.NewString()
		params := execsess.Params{
			SessionID: sessionID,
			Actor:     p.Actor(),
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
			// Astronomically unlikely with UUIDv4, but handle it explicitly
			// so we never silently overwrite an audit-tracked session.
			http.Error(w, "session id collision", http.StatusInternalServerError)
			return
		}
		defer sessions.Remove(sessionID)

		slog.InfoContext(r.Context(), "pod_exec",
			"category", "audit",
			"event", "session_start",
			"session_id", sessionID,
			"actor.sub", params.Actor,
			"cluster", c.Name,
			"namespace", ns,
			"pod", pod,
			"container", container,
			"tty", tty,
			"command", command,
			"k8s_identity", k8sIdentityLabel(c),
			"started_at", started.Format(time.RFC3339Nano),
		)

		result, runErr := execsess.Run(r.Context(), ws, p, params)
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

		slog.InfoContext(r.Context(), "pod_exec",
			"category", "audit",
			"event", "session_end",
			"session_id", sessionID,
			"actor.sub", params.Actor,
			"cluster", c.Name,
			"namespace", ns,
			"pod", pod,
			"container", resolvedContainer,
			"tty", tty,
			"command", commandOrResolved(command, result.Resolved.Command),
			"k8s_identity", k8sIdentityLabel(c),
			"started_at", started.Format(time.RFC3339Nano),
			"ended_at", ended.Format(time.RFC3339Nano),
			"duration_ms", ended.Sub(started).Milliseconds(),
			"close_reason", closeReason,
			"exit_code", result.ExitCode,
			"err", errString(runErr),
		)
	}
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
// RFC 0001 §10. v1 only knows two shapes; v2 will inject the IDC ARN here.
func k8sIdentityLabel(c clusters.Cluster) string {
	switch c.Backend {
	case clusters.BackendKubeconfig:
		return "kubeconfig:" + c.Name
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
