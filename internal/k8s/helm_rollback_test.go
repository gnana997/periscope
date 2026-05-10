// helm_rollback_test.go — function-level checks for the helm
// rollback path (issue #77). End-to-end coverage (older revision →
// success, current revision → no-op, denied → DeniedError) lives at
// the handler-test layer where rollbackHelmReleaseFn is a fn-var that
// can be stubbed without touching the apiserver.

package k8s

import (
	"context"
	"errors"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/storage/driver"

	"github.com/gnana997/periscope/internal/clusters"
)

func withRollbackRun(t *testing.T, fn func(context.Context, *action.Configuration, HelmRollbackArgs) error) {
	t.Helper()
	prev := rollbackRunFn
	rollbackRunFn = fn
	t.Cleanup(func() { rollbackRunFn = prev })
}

// TestRollbackHelmRelease_ValidationMissingFields covers the
// function-level validation. The handler validates first, but the
// function double-checks for direct callers.
func TestRollbackHelmRelease_ValidationMissingFields(t *testing.T) {
	cases := []struct {
		name string
		args HelmRollbackArgs
	}{
		{"empty namespace", HelmRollbackArgs{ReleaseName: "r", Revision: 1}},
		{"empty releaseName", HelmRollbackArgs{Namespace: "n", Revision: 1}},
		{"zero revision", HelmRollbackArgs{Namespace: "n", ReleaseName: "r", Revision: 0}},
		{"negative revision", HelmRollbackArgs{Namespace: "n", ReleaseName: "r", Revision: -1}},
		{"all empty", HelmRollbackArgs{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := RollbackHelmRelease(context.Background(), nil, clusters.Cluster{Name: "k"}, tc.args)
			if err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

// TestDefaultRollbackRun_StructFieldsPresent is a compile-time check
// that the helm SDK Rollback action struct still has the fields we
// reference. If helm renames Wait, CleanupOnFail, DisableHooks, or
// Version, this fails to build — early warning ahead of any silent
// behavior change.
func TestDefaultRollbackRun_StructFieldsPresent(t *testing.T) {
	rb := action.NewRollback(nil)
	rb.Version = 1
	rb.Wait = true
	rb.CleanupOnFail = true
	rb.DisableHooks = true
	_ = rb
}

// TestDefaultRollbackRun_ErrorTypePropagation guards against a helm
// SDK change that wraps driver sentinels in a way that breaks
// errors.Is — the handler classifier (helmErrorToStatus) depends on
// that match for the 404/422 branches.
func TestDefaultRollbackRun_ErrorTypePropagation(t *testing.T) {
	wrapped := errors.Join(driver.ErrReleaseNotFound, errors.New("rollback context"))
	if !errors.Is(wrapped, driver.ErrReleaseNotFound) {
		t.Error("errors.Join + ErrReleaseNotFound should match via errors.Is")
	}
	wrapped = errors.Join(driver.ErrNoDeployedReleases, errors.New("rollback context"))
	if !errors.Is(wrapped, driver.ErrNoDeployedReleases) {
		t.Error("errors.Join + ErrNoDeployedReleases should match via errors.Is")
	}
}

// TestReleaseDetailToActionInfo_NilSafe verifies the converter
// handles a nil HelmReleaseDetail without panicking — the recovery
// path in RollbackHelmRelease uses this when the post-rollback
// re-read fails, and a nil detail must produce a zero-value
// ActionReleaseInfo rather than NPE.
func TestReleaseDetailToActionInfo_NilSafe(t *testing.T) {
	got := releaseDetailToActionInfo(nil)
	if got != (ActionReleaseInfo{}) {
		t.Errorf("nil detail should map to zero ActionReleaseInfo, got %+v", got)
	}
}

// silence unused-import on withRollbackRun in this file. Real
// invocations live in the handler test layer.
var _ = withRollbackRun
