// helm_rollback.go — Helm release rollback action (issue #77).
//
// Wraps action.NewRollback(...).Run(name) with Wait, CleanupOnFail,
// and DisableHooks operator-controllable. Pre-flight SAR with
// verb=patch runs against each kind in the TARGET revision's manifest
// list before the SDK call fires. If any kind is denied, we never
// trigger the rollback — same fail-fast discipline as the rest of
// the helm write surface.
//
// Why patch (not delete + create)? helm internally compares the
// target revision's manifests against the current cluster state and
// issues 3-way patches; "patch" is the apiserver verb the rollback
// will exercise. This also matches what helm upgrade asks for, so
// an operator who can upgrade can rollback.
//
// Sync; rollbacks usually finish in 5-30s but a hook-rich chart
// can run longer. Handler caps at the same 10min ceiling as install
// / upgrade / uninstall.
//
// Audit: pre/post pair. Intent fires BEFORE the SDK call so a
// partition or hung rollback still leaves a forensic trail.
//
// Type names (HelmRollbackArgs / HelmRollbackResult) are prefixed
// to disambiguate from the workload rollback (#71) which owns the
// unprefixed RollbackArgs / RollbackResult symbols in this package.

package k8s

import (
	"context"
	"fmt"
	"time"

	"helm.sh/helm/v3/pkg/action"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// HelmRollbackArgs is the input to RollbackHelmRelease. Namespace +
// ReleaseName identify the existing release; Revision is the target
// revision to roll back to (must be > 0 and != current).
//
// Wait blocks until the rolled-back manifests reach Ready. Defaults
// to true at the handler — operators clicking Rollback expect "rolled
// back" to mean "the cluster is at that revision now," not "we asked
// helm to start rolling back."
//
// CleanupOnFail removes any partially-applied resources if the
// rollback errors mid-flight; defaults to false matching helm's
// default. Operators with hook-heavy charts may want to flip it.
//
// DisableHooks skips pre-/post-rollback hooks; useful when a hook
// is the thing that's stuck and the operator wants to force-roll.
type HelmRollbackArgs struct {
	Namespace     string
	ReleaseName   string
	Revision      int
	Wait          bool
	CleanupOnFail bool
	DisableHooks  bool
	Timeout       time.Duration
}

// HelmRollbackResult is the response shape. The release block is the
// post-rollback state; FromRevision and ToRevision capture the audit-
// relevant transition; NewRevision is helm's freshly-assigned
// revision number (helm rollback always creates a new revision
// rather than mutating the target — handy for the SPA toast and the
// audit row).
type HelmRollbackResult struct {
	Release      ActionReleaseInfo `json:"release"`
	NewRevision  int               `json:"newRevision"`
	FromRevision int               `json:"fromRevision"`
	ToRevision   int               `json:"toRevision"`
}

// rollbackRunFn is the test seam for the actual helm SDK call.
var rollbackRunFn = defaultRollbackRun

// defaultRollbackRun runs the actual helm SDK rollback. action.Rollback
// returns no useful payload of its own; callers re-read storage to
// pick up the new revision.
func defaultRollbackRun(ctx context.Context, cfg *action.Configuration, args HelmRollbackArgs) error {
	rb := action.NewRollback(cfg)
	rb.Version = args.Revision
	rb.Wait = args.Wait
	rb.CleanupOnFail = args.CleanupOnFail
	rb.DisableHooks = args.DisableHooks
	if args.Timeout > 0 {
		rb.Timeout = args.Timeout
	}
	return rb.Run(args.ReleaseName)
}

// RollbackHelmRelease runs the rollback action against an existing
// release. Pre-flight SAR (verb=patch) runs first against the TARGET
// revision's manifest list; denial blocks the action.
//
// Returns:
//   - (*HelmRollbackResult, nil) on success.
//   - (nil, *DeniedError) on pre-flight RBAC denial.
//   - (nil, err) on any other failure (release / target revision not
//     found, no-op rollback to current, helm SDK error, etc.).
//     Handler classifies via helmErrorToStatus.
func RollbackHelmRelease(ctx context.Context, p credentials.Provider, c clusters.Cluster, args HelmRollbackArgs) (*HelmRollbackResult, error) {
	if args.Namespace == "" || args.ReleaseName == "" {
		return nil, fmt.Errorf("rollback: namespace and releaseName are required")
	}
	if args.Revision <= 0 {
		return nil, fmt.Errorf("rollback: revision must be > 0")
	}

	// Read the current release first so we can (1) reject no-op
	// rollback-to-self and (2) capture the pre-rollback revision
	// for the response and audit row.
	current, err := GetHelmRelease(ctx, p, c, args.Namespace, args.ReleaseName, 0, defaultDetailMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("rollback pre-flight: read current release: %w", err)
	}
	if current == nil {
		return nil, fmt.Errorf("rollback: release %q not found in namespace %q", args.ReleaseName, args.Namespace)
	}
	if args.Revision == current.Revision {
		return nil, fmt.Errorf("rollback: target revision %d is already current", args.Revision)
	}

	// Read the target revision's manifests for the SAR pre-flight.
	// helm rollback patches the cluster from current → target and
	// the apiserver enforces RBAC per resource — checking the
	// target manifests gives the operator the fail-fast view of
	// "your role doesn't allow this rollback" before any state
	// mutation.
	target, err := GetHelmRelease(ctx, p, c, args.Namespace, args.ReleaseName, args.Revision, defaultDetailMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("rollback pre-flight: read target revision %d: %w", args.Revision, err)
	}
	if target == nil {
		return nil, fmt.Errorf("rollback: target revision %d of release %q not found", args.Revision, args.ReleaseName)
	}
	denied, err := preflightSARs(ctx, p, c, target.Resources, "patch")
	if err != nil {
		return nil, fmt.Errorf("rollback pre-flight: %w", err)
	}
	if len(denied) > 0 {
		return nil, &DeniedError{Denials: denied}
	}

	// Pre-flight passed. Build action config + run the rollback.
	cfg, err := buildHelmActionConfigFn(ctx, p, c, args.Namespace)
	if err != nil {
		return nil, err
	}
	if err := rollbackRunFn(ctx, cfg, args); err != nil {
		return nil, err
	}

	// helm rollback creates a new revision rather than mutating the
	// target — re-read storage so the response reflects post-rollback
	// state (rev=0 returns latest deployed). If the post-read fails
	// (transient apiserver error after a successful rollback), fall
	// back to a synthesized result so the SPA can still show a toast
	// rather than misleading the operator into thinking the rollback
	// itself failed.
	post, err := GetHelmRelease(ctx, p, c, args.Namespace, args.ReleaseName, 0, defaultDetailMaxBytes)
	if err != nil || post == nil {
		return &HelmRollbackResult{
			Release:      releaseDetailToActionInfo(current),
			NewRevision:  current.Revision + 1,
			FromRevision: current.Revision,
			ToRevision:   args.Revision,
		}, nil
	}
	return &HelmRollbackResult{
		Release:      releaseDetailToActionInfo(post),
		NewRevision:  post.Revision,
		FromRevision: current.Revision,
		ToRevision:   args.Revision,
	}, nil
}

// releaseDetailToActionInfo lifts a HelmReleaseDetail (read-path
// shape) into the ActionReleaseInfo used by the write-path response
// types. Avoids exposing the read-path struct on the write API.
func releaseDetailToActionInfo(d *HelmReleaseDetail) ActionReleaseInfo {
	if d == nil {
		return ActionReleaseInfo{}
	}
	return ActionReleaseInfo{
		Name:      d.Name,
		Namespace: d.Namespace,
		Revision:  d.Revision,
		Status:    d.Status,
		Chart: ActionChartRef{
			Name:    d.ChartName,
			Version: d.ChartVersion,
		},
		DeployedAt: d.Updated.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}
