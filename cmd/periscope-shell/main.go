// periscope-shell — the cluster-shell pod's container entrypoint
// (issue #104).
//
// Architecture note: the operator's interactive bash is NOT this
// binary. Periscope main spawns the operator's bash via a separate
// `kubectl exec` channel after this pod reaches Ready. That bash
// inherits the pod's env (KUBECONFIG, PERISCOPE_SHELL_AUDIT_FILE,
// etc.) and runs as a child of the container runtime's exec
// machinery, distinct from this PID-1 entrypoint.
//
// So this binary's only jobs are:
//
//  1. Validate the env vars Periscope set on the pod spec. Catching
//     misconfiguration here fails fast with an operator-readable
//     message in the pod logs, rather than a confusing "the shell
//     didn't open" with no diagnostic.
//
//  2. Pre-create the audit-file directory so the kubectl wrapper's
//     first O_APPEND open succeeds.
//
//  3. Block until SIGTERM. The kubelet sends SIGTERM when Periscope
//     deletes the pod on session close; we exit 0 promptly so the
//     terminationGracePeriodSeconds window stays tight.
//
// Configuration (env vars, all set by Periscope on the pod spec):
//
//	PERISCOPE_SHELL_SESSION_ID    UUID for this session — surfaced in
//	                              audit lines emitted by the kubectl
//	                              wrapper and joined back into the
//	                              cluster_shell_close audit row.
//	PERISCOPE_SHELL_MODE          "bash" (kubectl-only ships in a
//	                              follow-up PR; rejected at startup
//	                              here so the pod fails fast with a
//	                              clear message).
//	PERISCOPE_SHELL_AUDIT_FILE    Path the kubectl wrapper appends
//	                              JSON audit lines to. Defaults to
//	                              /tmp/periscope-shell/audit.jsonl.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	envSessionID = "PERISCOPE_SHELL_SESSION_ID"
	envMode      = "PERISCOPE_SHELL_MODE"
	envAuditFile = "PERISCOPE_SHELL_AUDIT_FILE"

	defaultAuditFile = "/tmp/periscope-shell/audit.jsonl"

	modeBash        = "bash"
	modeKubectlOnly = "kubectl-only"
)

func main() {
	if err := run(); err != nil {
		slog.Error("periscope-shell exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	sessionID := strings.TrimSpace(os.Getenv(envSessionID))
	if sessionID == "" {
		return fmt.Errorf("%s required", envSessionID)
	}
	mode := strings.TrimSpace(os.Getenv(envMode))
	slog.Info("periscope-shell starting", "session_id", sessionID, "mode", mode)

	switch mode {
	case modeBash:
		// Bash is spawned by Periscope's `kubectl exec` separately;
		// we just hold the pod open.
	case modeKubectlOnly:
		// Defensive — the WS handler should already reject this mode
		// before scheduling the pod. If a pod somehow lands here with
		// mode=kubectl-only, fail with a message an operator reading
		// pod logs can act on rather than crash-loop the pod silently.
		return fmt.Errorf("mode %q is not supported in this image build; the kubectl-only REPL ships in a follow-up PR. set %s=%s on the pod to use this image", mode, envMode, modeBash)
	case "":
		return fmt.Errorf("%s required (one of %q, %q)", envMode, modeBash, modeKubectlOnly)
	default:
		return fmt.Errorf("%s=%q is not a known mode (want %q or %q)", envMode, mode, modeBash, modeKubectlOnly)
	}

	// Pre-create the audit-file directory so the kubectl wrapper's
	// first O_APPEND open doesn't have to handle ENOENT. Best-effort
	// — the wrapper also creates parents on its first write.
	auditFile := strings.TrimSpace(os.Getenv(envAuditFile))
	if auditFile == "" {
		auditFile = defaultAuditFile
	}
	if err := os.MkdirAll(filepath.Dir(auditFile), 0o755); err != nil {
		slog.Warn("could not ensure audit directory", "path", auditFile, "err", err)
	}

	// Block until the kubelet sends SIGTERM on pod delete. signal.
	// NotifyContext returns a context cancelled on the first matching
	// signal; we don't need to do anything on the way out except
	// cooperate with the grace period.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	slog.Info("periscope-shell received shutdown signal", "session_id", sessionID)
	return nil
}
