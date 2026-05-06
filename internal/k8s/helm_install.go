// helm_install.go — Helm install action (issue #76).
//
// Wraps helm.sh/helm/v3/pkg/action.NewInstall(...).Run(...) with
// DryRun=false, Atomic=true (default), Wait=true (forced by Atomic).
// The pre-flight SAR loop from helm_preview.go runs BEFORE the helm
// SDK call fires — if any kind/verb is denied, we never trigger the
// install. This avoids the worst case where Atomic=true catches a
// forbidden resource mid-apply, then helm's rollback also fails
// because the rollback needs the same permissions. Pre-flight
// short-circuits cleanly.
//
// Sync: blocks until helm SDK returns (success / failure / timeout).
// Default 5min timeout; capped at 10min server-side. The HTTP server
// holds the connection. SPA shows a spinner.
//
// Audit: pre/post pair (VerbHelmInstallIntent + VerbHelmInstall).
// Intent fires BEFORE the SDK call so a partition / hung apiserver
// still leaves a forensic trail. Outcome fires AFTER. Same discipline
// as workload-rollback (VerbRollbackIntent + VerbRollback).

package k8s

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// InstallArgs is the input to InstallHelmRelease. Mirrors PreviewArgs
// (preview-then-commit means the SPA passes nearly-identical input
// to both endpoints) plus three SDK-action knobs the operator can
// override per-call.
type InstallArgs struct {
	Ref         string
	ChartName   string // see PreviewArgs
	Version     string
	Namespace   string
	ReleaseName string
	Values      string

	// Atomic, when true, instructs helm to roll back the release
	// automatically if any part of the install fails (e.g. a manifest
	// fails to apply, a wait times out). Default true on the handler
	// side; this struct field defaults to false in Go's zero-value
	// world, so callers that build the struct directly must set it.
	Atomic bool

	// Wait, when true, blocks until all resources are in a ready
	// state before returning. Required when Atomic=true (helm enforces
	// this). Default true on the handler side.
	Wait bool

	// WaitForJobs additionally waits for Job completion. Off by
	// default because migration jobs can be slow; operators opt in.
	WaitForJobs bool

	// IncludeCRDs, when true, applies any CRDs the chart ships under
	// crds/ before the rest of the manifests. Default true on the
	// handler side — charts that ship CRDs need them applied first.
	IncludeCRDs bool

	// Timeout caps the install duration. The helm SDK uses this for
	// resource readiness waits (when Wait=true) AND for the overall
	// action. Handler defaults to 5min; capped at 10min server-side.
	Timeout time.Duration
}

// HelmActionResult is the response shape for both InstallHelmRelease
// and UpgradeHelmRelease. The release block mirrors the SPA's
// existing HelmRelease detail shape (#9) so the SPA can route the
// operator straight to the release detail page on success.
type HelmActionResult struct {
	Release ActionReleaseInfo `json:"release"`

	// RolledBack is true when Atomic=true caught a partial failure
	// AND the rollback succeeded. The `Release` block in this case
	// represents the state AFTER rollback (i.e. the release at its
	// previous revision, not the half-installed state).
	RolledBack bool `json:"rolledBack,omitempty"`

	// RollbackError is non-empty when Atomic=true caught a partial
	// failure AND the rollback ALSO failed. This is catastrophic —
	// the cluster is in an inconsistent state. The handler returns
	// 500 with this populated.
	RollbackError string `json:"rollbackError,omitempty"`
}

// ActionReleaseInfo is the slice of release state the SPA needs
// post-install / upgrade. Notes is the chart's NOTES.txt template-
// rendered output — operators want to see post-install instructions
// (e.g. "run kubectl get svc to find the LB IP").
type ActionReleaseInfo struct {
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Revision   int    `json:"revision"`
	Status     string `json:"status"` // "deployed" | "failed" | etc.
	Chart      ActionChartRef `json:"chart"`
	DeployedAt string `json:"deployedAt"`
	Notes      string `json:"notes,omitempty"`
}

type ActionChartRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// installRunFn is the test seam for the actual helm SDK install
// call. Production wires defaultInstallRun (real action.NewInstall);
// tests substitute a fake that returns canned *release.Release or
// errors so the orchestration logic (pre-flight, audit, response
// shaping) is testable without spinning up a real apiserver.
var installRunFn = defaultInstallRun

// defaultInstallRun runs the actual helm SDK install. Returns the
// resulting *release.Release on success.
func defaultInstallRun(ctx context.Context, cfg *action.Configuration, args InstallArgs) (*release.Release, error) {
	chrt, err := fetchAndLoadChartFn(ctx, PreviewArgs{
		Ref: args.Ref, ChartName: args.ChartName, Version: args.Version,
	})
	if err != nil {
		return nil, err
	}
	vals, err := parseValuesYAML(args.Values)
	if err != nil {
		return nil, fmt.Errorf("parse values: %w", err)
	}

	inst := action.NewInstall(cfg)
	inst.ReleaseName = args.ReleaseName
	inst.Namespace = args.Namespace
	inst.Atomic = args.Atomic
	inst.Wait = args.Wait
	inst.WaitForJobs = args.WaitForJobs
	inst.IncludeCRDs = args.IncludeCRDs
	inst.Timeout = args.Timeout
	// CreateNamespace=true means helm creates the namespace if it
	// doesn't exist. Off by default so installs into a typo'd
	// namespace fail loudly rather than silently creating
	// "deafult"/"defualt"/etc. namespaces.
	inst.CreateNamespace = false
	return inst.RunWithContext(ctx, chrt, vals)
}

// InstallHelmRelease runs the install action against an existing
// cluster. Atomic by default — failed installs auto-rollback and
// leave no half-deployed state. Pre-flight SARs run before the helm
// SDK call so we fail fast on permission gaps rather than mid-apply.
//
// Returns:
//   - (*HelmActionResult, nil) on success (Release.Status == "deployed").
//   - (*HelmActionResult, nil) on atomic-rollback success — Release
//     reflects the post-rollback state, RolledBack=true.
//   - (*HelmActionResult, err) on atomic-rollback failure —
//     Release may be nil, RollbackError populated, err carries the
//     primary failure cause. Handler returns 500.
//   - (nil, err) on any pre-action failure (chart fetch, validation,
//     SAR check, build action config). Handler maps via
//     classifyHelmActionErr.
//
// The InstallDenied error wraps a list of denied (verb, GVR) tuples;
// callers (handlers) detect it via errors.As and return 403 with the
// list inline.
func InstallHelmRelease(ctx context.Context, p credentials.Provider, c clusters.Cluster, args InstallArgs) (*HelmActionResult, error) {
	if args.Namespace == "" || args.ReleaseName == "" || args.Ref == "" || args.Version == "" {
		return nil, fmt.Errorf("install: ref, version, namespace, and releaseName are all required")
	}

	// Pre-flight: render manifests via the existing preview path,
	// then SAR-check each. Reuses every helper from helm_preview.go —
	// same chart fetch + deps validation + render seam + SAR seam.
	preview, err := PreviewHelmInstall(ctx, p, c, PreviewArgs{
		Ref: args.Ref, ChartName: args.ChartName, Version: args.Version,
		Namespace: args.Namespace, ReleaseName: args.ReleaseName, Values: args.Values,
	})
	if err != nil {
		return nil, fmt.Errorf("install pre-flight: %w", err)
	}
	if len(preview.Denied) > 0 {
		return nil, &DeniedError{Denials: preview.Denied}
	}

	// Pre-flight passed. Build action config + run the install.
	cfg, err := buildHelmActionConfigFn(ctx, p, c, args.Namespace)
	if err != nil {
		return nil, err
	}

	rel, runErr := installRunFn(ctx, cfg, args)
	// Atomic + partial failure: helm SDK has already attempted
	// rollback by the time RunWithContext returns. The release in
	// storage reflects whichever state the rollback landed at.
	// We surface the failure cause + RolledBack flag.
	if runErr != nil {
		// Distinguish "install failed and rollback succeeded" from
		// "install failed and rollback also failed" via the helm SDK's
		// own error-wrapping convention. helm wraps the rollback
		// error into the primary error message when it fires.
		result := &HelmActionResult{}
		if rel != nil {
			result.Release = releaseToActionInfo(rel)
		}
		if args.Atomic && isRollbackFailure(runErr) {
			result.RollbackError = runErr.Error()
			return result, runErr
		}
		if args.Atomic {
			result.RolledBack = true
		}
		return result, runErr
	}

	if rel == nil {
		return nil, fmt.Errorf("install: helm SDK returned nil release without error")
	}

	// Best-effort: persist install metadata onto the helm release
	// storage Secret/ConfigMap so the upgrade dialog pre-fills the
	// chart ref + chart name on subsequent visits. Annotation write
	// failures are logged but never fail the install — the operator
	// just pastes the ref one more time on next upgrade.
	if metaErr := WritePeriscopeInstallMetadata(ctx, p, c, args.Namespace, args.ReleaseName, rel.Version, args.Ref, args.ChartName); metaErr != nil {
		slog.WarnContext(ctx, "helm install: metadata write failed (non-fatal)",
			"cluster", c.Name, "namespace", args.Namespace, "releaseName", args.ReleaseName,
			"revision", rel.Version, "err", metaErr)
	}

	return &HelmActionResult{Release: releaseToActionInfo(rel)}, nil
}

// DeniedError carries the pre-flight SAR denials. Handlers detect
// via errors.As and surface the list to the SPA with HTTP 403.
type DeniedError struct {
	Denials []PreviewDenial
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("pre-flight RBAC denied %d resource(s)", len(e.Denials))
}

// IsDeniedError reports whether err wraps a *DeniedError.
func IsDeniedError(err error) (*DeniedError, bool) {
	var de *DeniedError
	if errors.As(err, &de) {
		return de, true
	}
	return nil, false
}

// releaseToActionInfo projects a helm SDK *release.Release onto the
// trimmed wire shape we return to the SPA. Drops chart manifests,
// values, and other large fields the SPA doesn't need post-install
// (it can fetch /helm/releases/{ns}/{name} for full detail).
func releaseToActionInfo(rel *release.Release) ActionReleaseInfo {
	out := ActionReleaseInfo{
		Name:      rel.Name,
		Namespace: rel.Namespace,
		Revision:  rel.Version,
	}
	if rel.Info != nil {
		out.Status = rel.Info.Status.String()
		out.Notes = rel.Info.Notes
		if !rel.Info.LastDeployed.IsZero() {
			out.DeployedAt = rel.Info.LastDeployed.UTC().Format(time.RFC3339)
		}
	}
	if rel.Chart != nil && rel.Chart.Metadata != nil {
		out.Chart = ActionChartRef{
			Name:    rel.Chart.Metadata.Name,
			Version: rel.Chart.Metadata.Version,
		}
	}
	return out
}

// isRollbackFailure detects whether an Atomic install's wrapped
// error indicates the rollback ALSO failed (catastrophic case).
// Helm SDK doesn't expose a sentinel for this — it composes the
// rollback failure into the primary error message via fmt.Errorf.
// The substring is stable across helm v3.10+; we sniff for it
// rather than reach into helm internals.
func isRollbackFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// Helm composes the rollback failure with "an error occurred
	// while rolling back the release". We also match the older
	// "release rollback failed" form used in helm v3.0-3.9 in case
	// upstream reverts the message.
	return strings.Contains(msg, "rolling back the release") ||
		strings.Contains(msg, "release rollback failed")
}
