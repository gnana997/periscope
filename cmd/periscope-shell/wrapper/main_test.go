package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRealBinary(t *testing.T) {
	tests := []struct {
		name        string
		invocation  string
		wantPath    string
		wantErrFrag string
	}{
		{
			name:       "kubectl via symlink in /usr/local/bin",
			invocation: "/usr/local/bin/kubectl",
			wantPath:   "/opt/periscope/bin/kubectl-real",
		},
		{
			name:       "helm via symlink in /usr/local/bin",
			invocation: "/usr/local/bin/helm",
			wantPath:   "/opt/periscope/bin/helm-real",
		},
		{
			name:       "kubectl via bare name (PATH lookup)",
			invocation: "kubectl",
			wantPath:   "/opt/periscope/bin/kubectl-real",
		},
		{
			name:        "unknown basename returns error with allow-list",
			invocation:  "/usr/local/bin/kustomize",
			wantErrFrag: `unknown wrapped command "kustomize"`,
		},
		{
			name:        "direct invocation by binary basename errors",
			invocation:  "/opt/periscope/bin/periscope-audit-exec",
			wantErrFrag: `unknown wrapped command "periscope-audit-exec"`,
		},
		{
			name:        "empty invocation errors",
			invocation:  "",
			wantErrFrag: `unknown wrapped command "."`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRealBinary(tt.invocation)
			if tt.wantErrFrag != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil (path=%s)", tt.wantErrFrag, got)
				}
				if !strings.Contains(err.Error(), tt.wantErrFrag) {
					t.Fatalf("error %q does not contain expected fragment %q", err.Error(), tt.wantErrFrag)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantPath {
				t.Errorf("path mismatch: got %s, want %s", got, tt.wantPath)
			}
		})
	}
}

func TestAllowListNamesSorted(t *testing.T) {
	got := allowListNames()
	// Both expected entries must be present and ordered alphabetically.
	// This is what the unrecognized-name error surface shows operators,
	// so stable ordering matters for grep-by-error-text.
	want := "helm, kubectl"
	if got != want {
		t.Errorf("allowListNames() = %q, want %q", got, want)
	}
}

func TestAppendAudit_WritesNDJSON(t *testing.T) {
	dir := t.TempDir()
	auditPath := filepath.Join(dir, "subdir", "audit.jsonl")
	t.Setenv(envAuditFile, auditPath)

	// First invocation should create the file + parent directory.
	if err := appendAudit(); err != nil {
		t.Fatalf("first appendAudit: %v", err)
	}
	// Second invocation should append, not truncate.
	if err := appendAudit(); err != nil {
		t.Fatalf("second appendAudit: %v", err)
	}

	f, err := os.Open(auditPath)
	if err != nil {
		t.Fatalf("open audit file: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	var lines []auditLine
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var line auditLine
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil {
			t.Fatalf("parse line %q: %v", scanner.Text(), err)
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 audit lines, got %d", len(lines))
	}
	for i, line := range lines {
		if line.Timestamp == "" {
			t.Errorf("line %d: empty timestamp", i)
		}
		if line.PID == 0 {
			t.Errorf("line %d: zero PID", i)
		}
		if len(line.Argv) == 0 {
			t.Errorf("line %d: empty argv", i)
		}
	}
}

func TestAppendAudit_NoEnvIsSilentNoop(t *testing.T) {
	// Explicitly unset (Setenv to "") simulates the binary being run
	// outside of a cluster-shell pod (e.g. from a developer's laptop).
	t.Setenv(envAuditFile, "")
	if err := appendAudit(); err != nil {
		t.Errorf("expected nil error when env var unset, got %v", err)
	}
}

func TestAppendAudit_WhitespaceEnvTreatedAsUnset(t *testing.T) {
	t.Setenv(envAuditFile, "   ")
	if err := appendAudit(); err != nil {
		t.Errorf("expected nil error when env var whitespace-only, got %v", err)
	}
}
