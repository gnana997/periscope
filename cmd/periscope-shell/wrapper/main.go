// periscope-audit-kubectl — kubectl wrapper that ships in the
// cluster-shell pod image (issue #104).
//
// The image symlinks /usr/local/bin/kubectl to this binary so any
// kubectl invocation in a bash session transparently routes through
// it. For each call we:
//
//  1. Append a single JSON line to $PERISCOPE_SHELL_AUDIT_FILE
//     capturing timestamp, pid, and argv. Best-effort: failures to
//     write the audit line are logged to stderr but NEVER block the
//     operator's command. The operator typing "kubectl get pods"
//     should never have their workflow break because Periscope
//     couldn't persist an audit line.
//
//  2. syscall.Exec into the real kubectl at
//     /opt/periscope/bin/kubectl-real with the same argv and env.
//     The wrapper process is replaced, so exit code, stdin, stdout,
//     and stderr all pass through unchanged.
//
// Best-effort attribution means an operator who deliberately bypasses
// the wrapper (e.g. by calling /opt/periscope/bin/kubectl-real
// directly, or by aliasing kubectl=<other>) loses per-command audit
// for those invocations. Documented as the bash-mode contract; the
// kubectl-only REPL in a follow-up PR enforces audit by parsing
// every line before any binary runs.
//
// Periscope main reads the audit file on session close via
// `kubectl exec cat $PERISCOPE_SHELL_AUDIT_FILE`, parses the JSON
// lines, and folds them into the cluster_shell_close audit row's
// Extra.commands array.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	envAuditFile = "PERISCOPE_SHELL_AUDIT_FILE"
	realKubectl  = "/opt/periscope/bin/kubectl-real"
)

// auditLine is the JSON shape appended per invocation. Field names
// stay short — these lines are read by Periscope on every session
// close and a 1KB-per-line ceiling lets the whole audit file fit
// comfortably in the 1MB audit-row body cap.
type auditLine struct {
	Timestamp string   `json:"ts"`
	PID       int      `json:"pid"`
	Argv      []string `json:"argv"`
}

func main() {
	// Step 1: best-effort audit append. Stderr-only logging here —
	// we deliberately do NOT use slog so we don't introduce a
	// startup config dependency that could fail in a way the
	// operator can't bypass. A single fprintf is enough.
	if err := appendAudit(); err != nil {
		fmt.Fprintf(os.Stderr, "periscope-audit-kubectl: audit append failed (continuing): %v\n", err)
	}

	// Step 2: exec the real kubectl. argv[0] is conventionally the
	// program name; we pass the wrapper's argv[0] through so
	// kubectl's own usage strings still print "kubectl" (and not
	// "/opt/periscope/bin/kubectl-real") if the user runs
	// "kubectl --help".
	args := append([]string{os.Args[0]}, os.Args[1:]...)
	if err := syscall.Exec(realKubectl, args, os.Environ()); err != nil {
		// exec failure is fatal — the operator's command must run.
		// Exit 126 follows the POSIX shell convention for "command
		// found but not executable" so any wrapper-aware tooling
		// can tell this apart from a non-zero kubectl exit.
		fmt.Fprintf(os.Stderr, "periscope-audit-kubectl: exec %s failed: %v\n", realKubectl, err)
		os.Exit(126)
	}
}

func appendAudit() (retErr error) {
	path := strings.TrimSpace(os.Getenv(envAuditFile))
	if path == "" {
		// No audit file configured → silently skip. This makes the
		// binary usable as a transparent kubectl wrapper outside of
		// the cluster-shell pod (e.g. in tests, local invocations)
		// without it failing on a missing env var.
		return nil
	}

	// Pre-create the parent dir so the open below succeeds on the
	// first ever invocation. The shell entrypoint also pre-creates
	// it, but doing it here lets this binary stand alone.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}

	line := auditLine{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		PID:       os.Getpid(),
		Argv:      os.Args, // argv[0] preserved for forensic context — usually "/usr/local/bin/kubectl"
	}
	buf, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("marshal audit line: %w", err)
	}

	// O_APPEND on POSIX guarantees atomic writes up to PIPE_BUF
	// (4096 bytes on Linux). A single auditLine — even with a
	// pathological argv — is comfortably under that, so concurrent
	// kubectl invocations from a backgrounded shell pipeline can't
	// interleave bytes.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if cerr := f.Close(); retErr == nil && cerr != nil {
			retErr = fmt.Errorf("close %s: %w", path, cerr)
		}
	}()
	if _, err := f.Write(append(buf, '\n')); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
