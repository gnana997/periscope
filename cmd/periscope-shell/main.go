// periscope-shell — the per-session shell entrypoint baked into the
// cluster-shell pod image (issue #104).
//
// Periscope main creates a short-lived pod from this image for each
// browser-initiated shell session. The pod's container ENTRYPOINT
// runs this binary; we inspect a handful of env vars Periscope set
// on the pod spec and either drop the operator into bash (with
// KUBECONFIG pointing at the session-scoped kubeconfig Secret
// mounted at /etc/periscope/kubeconfig) or — eventually — into a
// kubectl-only REPL.
//
// In this PR only bash mode is wired. The kubectl-only REPL ships in
// a follow-up; calling the mode here returns a clear error rather
// than crash-looping the pod, so operators see a friendly diagnostic
// in the SPA instead of an opaque ImagePullBackOff-shaped failure.
//
// Configuration (env vars, all set by Periscope on the pod spec):
//
//	PERISCOPE_SHELL_SESSION_ID    UUID for this session — surfaced in
//	                              audit lines emitted by the kubectl
//	                              wrapper and joined back into the
//	                              session_close audit row by Periscope.
//	PERISCOPE_SHELL_MODE          "bash" or "kubectl-only".
//	PERISCOPE_SHELL_AUDIT_FILE    Path the kubectl wrapper appends
//	                              JSON audit lines to. Periscope reads
//	                              this on session close via `kubectl
//	                              exec cat`. Defaults to
//	                              /tmp/periscope-shell/audit.jsonl.
//	PERISCOPE_SHELL_KUBECONFIG    Path where Periscope mounted the
//	                              per-session kubeconfig Secret.
//	                              Defaults to /etc/periscope/kubeconfig.
package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	envSessionID  = "PERISCOPE_SHELL_SESSION_ID"
	envMode       = "PERISCOPE_SHELL_MODE"
	envAuditFile  = "PERISCOPE_SHELL_AUDIT_FILE"
	envKubeconfig = "PERISCOPE_SHELL_KUBECONFIG"

	defaultAuditFile  = "/tmp/periscope-shell/audit.jsonl"
	defaultKubeconfig = "/etc/periscope/kubeconfig"

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
	slog.Info("periscope-shell starting",
		"session_id", sessionID,
		"mode", os.Getenv(envMode))

	switch mode := strings.TrimSpace(os.Getenv(envMode)); mode {
	case modeBash:
		return execBash()
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
}

// execBash replaces this process with /bin/bash --login. The login
// shell sources /etc/profile (where the image sets PS1 to a session-
// distinguishing prompt) and the operator's ~/.bashrc if any. We
// pass through the current env after layering on KUBECONFIG and the
// audit-file path so child kubectl invocations route correctly.
//
// syscall.Exec replaces the process — there is no post-exec code
// here and no defer can run. Cleanup is the K8s pod lifecycle's job.
func execBash() error {
	kubeconfig := strings.TrimSpace(os.Getenv(envKubeconfig))
	if kubeconfig == "" {
		kubeconfig = defaultKubeconfig
	}
	auditFile := strings.TrimSpace(os.Getenv(envAuditFile))
	if auditFile == "" {
		auditFile = defaultAuditFile
	}

	// Pre-create the audit dir so the wrapper's first O_APPEND open
	// doesn't have to handle ENOENT. Best-effort; the wrapper itself
	// also creates parents on its first write.
	if err := os.MkdirAll(filepath.Dir(auditFile), 0o755); err != nil {
		slog.Warn("could not ensure audit directory", "path", auditFile, "err", err)
	}

	env := append(os.Environ(),
		"KUBECONFIG="+kubeconfig,
		envAuditFile+"="+auditFile,
	)

	if err := syscall.Exec("/bin/bash", []string{"/bin/bash", "--login"}, env); err != nil {
		return fmt.Errorf("exec /bin/bash: %w", err)
	}
	return nil // unreachable on success
}
