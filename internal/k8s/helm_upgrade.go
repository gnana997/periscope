// helm_upgrade.go — Helm upgrade action (issue #76).
//
// Sibling to helm_install.go. Wraps action.NewUpgrade(...).Run(...)
// with DryRun=false, Atomic=true (default). Same pre-flight + audit +
// response shaping as install; the action-side differences are:
//
//   - Verb for SAR pre-flight is "patch" (matches preview's upgrade
//     mode), not "create".
//   - Release must already exist; helm SDK returns
//     driver.ErrReleaseNotFound otherwise (caught by helmErrorToStatus).
//   - On Atomic=true rollback, the rollback restores the PREVIOUS
//     revision, not the post-install zero state. The SPA can navigate
//     to that revision after the response.

package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// UpgradeArgs is the input to UpgradeHelmRelease. ReleaseName +
// Namespace identify the existing release; Ref / Version / Values
// are the proposed update.
type UpgradeArgs struct {
	Ref         string
	ChartName   string
	Version     string
	Namespace   string
	ReleaseName string
	Values      string

	Atomic       bool
	Wait         bool
	WaitForJobs  bool
	Timeout      time.Duration

	// CleanupOnFail removes new resources created during the upgrade
	// if the upgrade fails. Off by default — Atomic=true already
	// handles the failure case via rollback. CleanupOnFail is for
	// non-atomic upgrades where the operator wants partial cleanup
	// without a full rollback. Power-user knob.
	CleanupOnFail bool

	// MaxHistory caps how many revisions helm keeps in storage.
	// Helm's default is 10. Operators on chart-heavy clusters often
	// set this to 5 or 3 to bound storage. Default 10; body field
	// honored.
	MaxHistory int
}

// upgradeRunFn is the test seam for the actual helm SDK upgrade
// call. Same shape as installRunFn.
var upgradeRunFn = defaultUpgradeRun

// defaultUpgradeRun runs the actual helm SDK upgrade.
func defaultUpgradeRun(ctx context.Context, cfg *action.Configuration, args UpgradeArgs) (*release.Release, error) {
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

	upg := action.NewUpgrade(cfg)
	upg.Namespace = args.Namespace
	upg.Atomic = args.Atomic
	upg.Wait = args.Wait
	upg.WaitForJobs = args.WaitForJobs
	upg.Timeout = args.Timeout
	upg.CleanupOnFail = args.CleanupOnFail
	if args.MaxHistory > 0 {
		upg.MaxHistory = args.MaxHistory
	}
	return upg.RunWithContext(ctx, args.ReleaseName, chrt, vals)
}

// UpgradeHelmRelease runs the upgrade action against an existing
// release. Atomic by default — failed upgrades auto-rollback to the
// previous revision. Pre-flight SARs run before the helm SDK call.
//
// Same return semantics as InstallHelmRelease: success returns the
// new release info; atomic-rollback success returns the rolled-back
// state with RolledBack=true; atomic-rollback failure returns 500
// with RollbackError populated.
func UpgradeHelmRelease(ctx context.Context, p credentials.Provider, c clusters.Cluster, args UpgradeArgs) (*HelmActionResult, error) {
	if args.Namespace == "" || args.ReleaseName == "" || args.Ref == "" || args.Version == "" {
		return nil, fmt.Errorf("upgrade: ref, version, namespace, and releaseName are all required")
	}

	// Pre-flight via the upgrade-preview path (SAR verb=patch).
	preview, err := PreviewHelmUpgrade(ctx, p, c, PreviewArgs{
		Ref: args.Ref, ChartName: args.ChartName, Version: args.Version,
		Namespace: args.Namespace, ReleaseName: args.ReleaseName, Values: args.Values,
	})
	if err != nil {
		return nil, fmt.Errorf("upgrade pre-flight: %w", err)
	}
	if len(preview.Denied) > 0 {
		return nil, &DeniedError{Denials: preview.Denied}
	}

	cfg, err := buildHelmActionConfigFn(ctx, p, c, args.Namespace)
	if err != nil {
		return nil, err
	}

	rel, runErr := upgradeRunFn(ctx, cfg, args)
	if runErr != nil {
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
		return nil, fmt.Errorf("upgrade: helm SDK returned nil release without error")
	}

	// Best-effort metadata write — same pattern as install. The new
	// revision's storage object gets the install-ref + chart-name
	// annotations so future upgrade dialogs continue pre-filling.
	// Helm doesn't auto-carry annotations from one revision to the
	// next, hence the per-action write.
	if metaErr := WritePeriscopeInstallMetadata(ctx, p, c, args.Namespace, args.ReleaseName, rel.Version, args.Ref, args.ChartName); metaErr != nil {
		slog.WarnContext(ctx, "helm upgrade: metadata write failed (non-fatal)",
			"cluster", c.Name, "namespace", args.Namespace, "releaseName", args.ReleaseName,
			"revision", rel.Version, "err", metaErr)
	}

	return &HelmActionResult{Release: releaseToActionInfo(rel)}, nil
}
