// helm_uninstall.go — Helm uninstall action (issue #123).
//
// Wraps action.NewUninstall(...).Run(name) with KeepHistory and
// DisableHooks operator-controllable. Pre-flight SAR with verb=delete
// runs against each kind in the CURRENT release's manifest list before
// the SDK call fires. If any kind is denied, we never trigger the
// uninstall — same fail-fast discipline as install/upgrade.
//
// Sync; helm uninstall typically completes in a few seconds (it's
// just a manifest delete + storage prune) but very large releases
// or charts with finalizers can take longer. The handler caps the
// request at the same 10min ceiling as install/upgrade.
//
// Audit: pre/post pair. Intent fires BEFORE the SDK call so a hung
// or partitioned uninstall still leaves a forensic trail.

package k8s

import (
	"context"
	"fmt"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// UninstallArgs is the input to UninstallHelmRelease. Namespace +
// ReleaseName identify the existing release. KeepHistory preserves
// the release in storage marked deleted (operator might want to
// inspect the history later) — defaults to false on the handler.
// DisableHooks skips pre-/post-delete hooks; useful when a hook is
// stuck and the operator wants to force-clean.
type UninstallArgs struct {
	Namespace    string
	ReleaseName  string
	KeepHistory  bool
	DisableHooks bool
}

// UninstallResult is the response shape. The release block is the
// last-known state of the release (from helm's storage right before
// deletion). RevisionsRemoved is the count helm reports — useful in
// audit and for the SPA's success toast.
type UninstallResult struct {
	Release          ActionReleaseInfo `json:"release"`
	RevisionsRemoved int               `json:"revisionsRemoved"`
}

// uninstallRunFn is the test seam for the actual helm SDK call.
var uninstallRunFn = defaultUninstallRun

// defaultUninstallRun runs the actual helm SDK uninstall.
func defaultUninstallRun(ctx context.Context, cfg *action.Configuration, args UninstallArgs) (*release.UninstallReleaseResponse, error) {
	un := action.NewUninstall(cfg)
	un.KeepHistory = args.KeepHistory
	un.DisableHooks = args.DisableHooks
	// Wait — uninstall blocks until resources are gone. Default true
	// here is the safer choice for the smoke-test workflow: operator
	// clicks Uninstall, modal closes, they see the empty release-list
	// page knowing the resources are actually gone (not "deleted in
	// storage but pods still terminating"). DisableHooks does NOT
	// affect Wait — they're independent helm flags.
	un.Wait = true
	return un.Run(args.ReleaseName)
}

// UninstallHelmRelease runs the uninstall action against an existing
// release. Pre-flight SAR (verb=delete) runs first; denial blocks the
// action. On success, returns the last-known release state plus the
// count of revisions helm pruned from storage.
//
// Returns:
//   - (*UninstallResult, nil) on success.
//   - (nil, *DeniedError) on pre-flight RBAC denial.
//   - (nil, err) on any other failure (release not found, helm SDK
//     error, etc.). Handler classifies via classifyHelmActionErr.
func UninstallHelmRelease(ctx context.Context, p credentials.Provider, c clusters.Cluster, args UninstallArgs) (*UninstallResult, error) {
	if args.Namespace == "" || args.ReleaseName == "" {
		return nil, fmt.Errorf("uninstall: namespace and releaseName are required")
	}

	// Pre-flight SAR. Read the CURRENT release's manifest list and
	// SAR `delete` against each kind. Reuses GetHelmRelease (the
	// existing read path) — at rev=0 it returns the latest deployed
	// revision, which is exactly what helm uninstall will tear down.
	current, err := GetHelmRelease(ctx, p, c, args.Namespace, args.ReleaseName, 0, defaultDetailMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("uninstall pre-flight: read current release: %w", err)
	}
	if current == nil {
		return nil, fmt.Errorf("uninstall: release %q not found in namespace %q", args.ReleaseName, args.Namespace)
	}
	denied, err := preflightSARs(ctx, p, c, current.Resources, "delete")
	if err != nil {
		return nil, fmt.Errorf("uninstall pre-flight: %w", err)
	}
	if len(denied) > 0 {
		return nil, &DeniedError{Denials: denied}
	}

	// Pre-flight passed. Build action config + run the uninstall.
	cfg, err := buildHelmActionConfigFn(ctx, p, c, args.Namespace)
	if err != nil {
		return nil, err
	}
	resp, err := uninstallRunFn(ctx, cfg, args)
	if err != nil {
		return nil, err
	}

	out := &UninstallResult{}
	if resp != nil && resp.Release != nil {
		out.Release = releaseToActionInfo(resp.Release)
	} else {
		// Fall back to the pre-uninstall snapshot for the response —
		// helm's UninstallReleaseResponse can omit the release block
		// when KeepHistory=false because the storage entry is gone
		// by the time we reach this branch. Operators still expect
		// the SPA to show "uninstalled release X r5", so the
		// pre-uninstall snapshot is the safer default.
		out.Release = ActionReleaseInfo{
			Name:      current.Name,
			Namespace: current.Namespace,
			Revision:  current.Revision,
			Status:    current.Status,
			Chart: ActionChartRef{
				Name:    current.ChartName,
				Version: current.ChartVersion,
			},
			DeployedAt: current.Updated.UTC().Format("2006-01-02T15:04:05Z07:00"),
		}
	}
	// helm doesn't surface "revisions removed count" directly on the
	// response shape — we approximate from the current release's
	// revision number (which is the highest revision = total count
	// when revisions are 1-indexed contiguous, which they are for a
	// release that hasn't had storage manually pruned). For
	// KeepHistory=true, helm leaves them in storage marked deleted;
	// we still report the count for symmetry.
	out.RevisionsRemoved = current.Revision
	return out, nil
}
