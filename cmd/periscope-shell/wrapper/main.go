// periscope-audit-exec — generic audit wrapper that ships in the
// cluster-shell pod image (issue #104).
//
// The image symlinks /usr/local/bin/kubectl AND /usr/local/bin/helm
// (and future tools) at this one binary; the symlink name dictates
// which real binary gets exec'd. For each call we:
//
//  1. Resolve which command the operator invoked from os.Args[0]:
//     the symlink name (e.g. "kubectl" or "helm") is allow-listed
//     against the table below and mapped to a real binary path under
//     /opt/periscope/bin/<name>-real. Anything not on the allow-list
//     errors with exit 127.
//
//  2. Append a single JSON line to $PERISCOPE_SHELL_AUDIT_FILE
//     capturing timestamp, pid, and argv. Best-effort: failures to
//     write the audit line are logged to stderr but NEVER block the
//     operator's command.
//
//  3. syscall.Exec into the resolved real binary with the same argv
//     and env. The wrapper process is replaced, so exit code, stdin,
//     stdout, and stderr all pass through unchanged.
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
	realBinDir   = "/opt/periscope/bin"
)

// wrappedCommands is the closed allow-list of commands this binary
// audits. Adding a new wrapped tool is a one-line addition here PLUS
// a matching `/opt/periscope/bin/<name>-real` install in
// Dockerfile.shell PLUS a `/usr/local/bin/<name>` symlink at this
// binary. Allow-list (not lenient-fallback) keeps the surface
// auditable in a review.
var wrappedCommands = map[string]string{
	"kubectl": filepath.Join(realBinDir, "kubectl-real"),
	"helm":    filepath.Join(realBinDir, "helm-real"),
}

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
	// Step 1: resolve the real binary from the symlink name. Exit
	// non-zero before touching the audit file if we can't map the
	// invocation — a writer that doesn't know what to exec shouldn't
	// pretend to audit it.
	realBinary, err := resolveRealBinary(os.Args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "periscope-audit-exec: %v\n", err)
		os.Exit(127) // POSIX "command not found"
	}

	// Step 2: best-effort audit append. Stderr-only logging here —
	// we deliberately do NOT use slog so we don't introduce a
	// startup config dependency that could fail in a way the
	// operator can't bypass.
	if err := appendAudit(); err != nil {
		fmt.Fprintf(os.Stderr, "periscope-audit-exec: audit append failed (continuing): %v\n", err)
	}

	// Step 3: exec the real binary. argv[0] is conventionally the
	// program name; we pass the wrapper's argv[0] through so the
	// real binary's own usage strings still print the symlink name
	// (e.g. "kubectl" and not "/opt/periscope/bin/kubectl-real") if
	// the user runs "kubectl --help".
	args := append([]string{os.Args[0]}, os.Args[1:]...)
	if err := syscall.Exec(realBinary, args, os.Environ()); err != nil {
		// exec failure is fatal — the operator's command must run.
		// Exit 126 follows the POSIX shell convention for "command
		// found but not executable" so any wrapper-aware tooling
		// can tell this apart from a non-zero exit from the real binary.
		fmt.Fprintf(os.Stderr, "periscope-audit-exec: exec %s failed: %v\n", realBinary, err)
		os.Exit(126)
	}
}

// resolveRealBinary maps an invocation path (typically os.Args[0])
// to the absolute path of the real binary to exec. Returns an error
// when the basename isn't on the allow-list — caller handles exit.
// Factored out so it's testable without subprocess plumbing.
func resolveRealBinary(invocation string) (string, error) {
	cmdName := filepath.Base(invocation)
	realBinary, ok := wrappedCommands[cmdName]
	if !ok {
		return "", fmt.Errorf("unknown wrapped command %q (allow-list: %s)",
			cmdName, allowListNames())
	}
	return realBinary, nil
}

// allowListNames returns the wrapped-command names sorted for stable
// error output. Tiny helper used only by the unrecognized-name path.
func allowListNames() string {
	names := make([]string, 0, len(wrappedCommands))
	for name := range wrappedCommands {
		names = append(names, name)
	}
	// 2 entries today — a manual bubble sort would do, but this keeps
	// the helper future-proof.
	for i := 0; i < len(names); i++ {
		for j := i + 1; j < len(names); j++ {
			if names[j] < names[i] {
				names[i], names[j] = names[j], names[i]
			}
		}
	}
	return strings.Join(names, ", ")
}

func appendAudit() (retErr error) {
	path := strings.TrimSpace(os.Getenv(envAuditFile))
	if path == "" {
		// No audit file configured → silently skip. This makes the
		// binary usable as a transparent wrapper outside of the
		// cluster-shell pod (e.g. in tests, local invocations)
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
		Argv:      os.Args, // argv[0] preserved for forensic context — usually "/usr/local/bin/<name>"
	}
	buf, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("marshal audit line: %w", err)
	}

	// O_APPEND on POSIX guarantees atomic writes up to PIPE_BUF
	// (4096 bytes on Linux). A single auditLine — even with a
	// pathological argv — is comfortably under that, so concurrent
	// invocations from a backgrounded shell pipeline can't
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
