package clustershell

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// CommandLine is one record from the shell pod's audit.jsonl file —
// emitted by the periscope-audit-kubectl wrapper, one per kubectl
// invocation in bash mode. The JSON shape mirrors auditLine in
// cmd/periscope-shell/wrapper/main.go; changing field names there
// requires updating tag-matching here.
type CommandLine struct {
	Timestamp string   `json:"ts"`
	PID       int      `json:"pid"`
	Argv      []string `json:"argv"`
}

// ReadCommandLog opens a fresh exec stream against the shell pod and
// `cat`s the wrapper's audit.jsonl, returning the parsed records.
// Bulk-on-close is the bash-mode attribution strategy for v1.2 — the
// kubectl-only REPL in a follow-up PR replaces this with guaranteed
// real-time per-command emission.
//
// Best-effort: ANY error path returns an empty slice rather than
// bubbling, because the pod is on its way to deletion and a
// failure to read the audit file shouldn't fail-close the session's
// outcome audit row. Errors are slog.Warn-logged for operator
// visibility but never propagate.
//
// The maxBytes ceiling prevents a runaway audit file from ballooning
// memory; the cap is BuildKubeconfig-independent and should be set
// to cfg.TranscriptMaxBytes by the caller.
func ReadCommandLog(
	ctx context.Context,
	p credentials.Provider,
	cluster clusters.Cluster,
	namespace, podName, sessionID string,
	maxBytes int64,
) []CommandLine {
	if maxBytes <= 0 {
		// Sane fallback — caller forgot to set the cap.
		maxBytes = 1 << 20
	}

	pr, pw := io.Pipe()
	done := make(chan error, 1)

	go func() {
		_, err := k8s.ExecPod(ctx, p, k8s.ExecPodArgs{
			Cluster:   cluster,
			Namespace: namespace,
			Pod:       podName,
			Container: ContainerName,
			Command:   []string{"cat", AuditFilePath},
			Stdout:    pw,
			Stderr:    io.Discard,
			SessionID: sessionID,
		})
		// Closing the write end signals EOF to the reader below
		// regardless of whether the cat succeeded.
		_ = pw.CloseWithError(err)
		done <- err
	}()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, io.LimitReader(pr, maxBytes)); err != nil && !errors.Is(err, io.EOF) {
		slog.Warn("clustershell: reading audit file from pod",
			"session_id", sessionID, "pod", podName, "err", err)
	}
	// Drain the executor goroutine so we don't leak it.
	execErr := <-done
	if execErr != nil {
		slog.Warn("clustershell: audit-file readback exec failed (commands not captured)",
			"session_id", sessionID, "pod", podName, "err", execErr)
		// Don't return — partial buffer is still worth parsing, the
		// cat may have written some lines before failing.
	}

	return parseAuditLines(buf.Bytes(), sessionID)
}

// parseAuditLines turns newline-delimited JSON into a slice of
// CommandLine records. Malformed lines are logged at warn and
// skipped — one bad line shouldn't lose the whole transcript.
func parseAuditLines(raw []byte, sessionID string) []CommandLine {
	if len(raw) == 0 {
		return nil
	}
	lines := bytes.Split(raw, []byte("\n"))
	out := make([]CommandLine, 0, len(lines))
	for i, line := range lines {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var cmd CommandLine
		if err := json.Unmarshal(line, &cmd); err != nil {
			slog.Warn("clustershell: malformed audit line skipped",
				"session_id", sessionID, "line_index", i, "err", err,
				"preview", previewBytes(line, 80))
			continue
		}
		out = append(out, cmd)
	}
	return out
}

// previewBytes returns a UTF-8-safe, length-capped preview of a
// byte slice for diagnostic logging. Avoids dumping kilobytes of
// raw bytes into the log when a single malformed line is the only
// problem.
func previewBytes(b []byte, max int) string {
	if len(b) <= max {
		return fmt.Sprintf("%q", b)
	}
	return fmt.Sprintf("%q... [%d more bytes]", b[:max], len(b)-max)
}
