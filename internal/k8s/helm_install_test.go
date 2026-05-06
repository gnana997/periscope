package k8s

import (
	"context"
	"errors"
	"testing"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/release"
	authv1 "k8s.io/api/authorization/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// fakeReleaseSuccess is the canned successful release returned by
// the install/upgrade run seam. Captures the minimum fields the
// handler shapes into ActionReleaseInfo.
func fakeReleaseSuccess() *release.Release {
	return &release.Release{
		Name:      "web",
		Namespace: "app-ns",
		Version:   1,
		Info: &release.Info{
			Status:       release.StatusDeployed,
			Notes:        "thanks for installing nginx",
			LastDeployed: mustParseTime("2026-05-06T12:00:00Z"),
		},
		Chart: &chart.Chart{
			Metadata: &chart.Metadata{Name: "nginx", Version: "1.0.0"},
		},
	}
}

func mustParseTime(s string) helmTime {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return helmTime{Time: t}
}

// helmTime is a tiny adapter — helm SDK uses its own time type
// (helm.sh/helm/v3/pkg/time) for release timestamps. Importing the
// helm time package is cleaner than reaching for it everywhere; this
// alias keeps the test file uncluttered.
type helmTime = struct {
	time.Time
}

// withInstallRun substitutes the install run seam for the duration
// of the test. Cleanup restores the original.
func withInstallRun(t *testing.T, fn func(context.Context, *action.Configuration, InstallArgs) (*release.Release, error)) {
	t.Helper()
	prev := installRunFn
	installRunFn = fn
	t.Cleanup(func() { installRunFn = prev })
}

// withBuildHelmActionConfig substitutes the action.Configuration
// builder seam (declared in helm_action.go). Tests use this to
// short-circuit the real K8s discovery + driver probe.
func withBuildHelmActionConfig(t *testing.T, fn func(context.Context, credentials.Provider, clusters.Cluster, string) (*action.Configuration, error)) {
	t.Helper()
	prev := buildHelmActionConfigFn
	buildHelmActionConfigFn = fn
	t.Cleanup(func() { buildHelmActionConfigFn = prev })
}

// TestInstallHelmRelease_HappyPath walks the full orchestration:
//   - chart fetch (substituted) returns a fixture chart
//   - preview render (substituted) returns canned manifest YAML
//   - SAR (substituted) allows everything
//   - buildHelmActionConfig (substituted) returns a stub config
//   - install run (substituted) returns a successful release
// Asserts the response carries revision + status + notes + chart info.
func TestInstallHelmRelease_HappyPath(t *testing.T) {
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) {
		return fixtureChart(t), nil
	})
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return installManifestYAML, nil
	})
	withPreviewSAR(t, allowAllSAR)
	withBuildHelmActionConfig(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*action.Configuration, error) {
		return &action.Configuration{}, nil
	})
	withInstallRun(t, func(_ context.Context, _ *action.Configuration, _ InstallArgs) (*release.Release, error) {
		return fakeReleaseSuccess(), nil
	})

	args := InstallArgs{
		Ref: "oci://example/c", Version: "1.0.0",
		Namespace: "app-ns", ReleaseName: "web",
		Atomic: true, Wait: true, IncludeCRDs: true,
		Timeout: 5 * time.Minute,
	}
	got, err := InstallHelmRelease(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err != nil {
		t.Fatalf("InstallHelmRelease: %v", err)
	}
	if got == nil {
		t.Fatal("nil result")
	}
	if got.Release.Name != "web" {
		t.Errorf("Release.Name = %q, want %q", got.Release.Name, "web")
	}
	if got.Release.Revision != 1 {
		t.Errorf("Release.Revision = %d, want 1", got.Release.Revision)
	}
	if got.Release.Status != "deployed" {
		t.Errorf("Release.Status = %q, want \"deployed\"", got.Release.Status)
	}
	if got.Release.Notes == "" {
		t.Error("expected non-empty Notes from chart NOTES.txt")
	}
	if got.Release.Chart.Name != "nginx" || got.Release.Chart.Version != "1.0.0" {
		t.Errorf("chart info = %+v", got.Release.Chart)
	}
	if got.RolledBack {
		t.Error("RolledBack should be false on success")
	}
}

// TestInstallHelmRelease_PreflightDeniedBlocksAction confirms that
// when the SAR loop returns any denial, we never invoke the install
// run seam — the install never fires.
func TestInstallHelmRelease_PreflightDeniedBlocksAction(t *testing.T) {
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) {
		return fixtureChart(t), nil
	})
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return installManifestYAML, nil
	})
	withPreviewSAR(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error) {
		// Deny networking.k8s.io (the Ingress in installManifestYAML).
		if attr.Group == "networking.k8s.io" {
			return false, "denied", nil
		}
		return true, "", nil
	})

	installCalled := false
	withInstallRun(t, func(_ context.Context, _ *action.Configuration, _ InstallArgs) (*release.Release, error) {
		installCalled = true
		return fakeReleaseSuccess(), nil
	})

	args := InstallArgs{Ref: "oci://example/c", Version: "1.0.0", Namespace: "app-ns", ReleaseName: "web"}
	_, err := InstallHelmRelease(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err == nil {
		t.Fatal("expected error for denied pre-flight")
	}
	denied, ok := IsDeniedError(err)
	if !ok {
		t.Fatalf("expected DeniedError, got %T: %v", err, err)
	}
	if len(denied.Denials) != 1 || denied.Denials[0].Resource != "ingresses" {
		t.Errorf("unexpected denial list: %+v", denied.Denials)
	}
	if installCalled {
		t.Error("install run should NOT fire when pre-flight denies")
	}
}

// TestInstallHelmRelease_AtomicRollbackSuccessSetsFlag confirms the
// RolledBack=true flag flips when Atomic=true catches a partial
// failure and rolls back successfully.
func TestInstallHelmRelease_AtomicRollbackSuccessSetsFlag(t *testing.T) {
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) {
		return fixtureChart(t), nil
	})
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return installManifestYAML, nil
	})
	withPreviewSAR(t, allowAllSAR)
	withBuildHelmActionConfig(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*action.Configuration, error) {
		return &action.Configuration{}, nil
	})
	// Helm SDK returns a generic install error AND has performed
	// rollback successfully. Our isRollbackFailure sniffer returns
	// false for this message, so we surface RolledBack=true.
	withInstallRun(t, func(_ context.Context, _ *action.Configuration, _ InstallArgs) (*release.Release, error) {
		return nil, errors.New("install failed: deployment ready check timed out")
	})

	args := InstallArgs{
		Ref: "oci://example/c", Version: "1.0.0",
		Namespace: "app-ns", ReleaseName: "web",
		Atomic: true,
	}
	got, err := InstallHelmRelease(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err == nil {
		t.Fatal("expected error from failed install")
	}
	if got == nil {
		t.Fatal("expected partial result with RolledBack flag, got nil")
	}
	if !got.RolledBack {
		t.Errorf("RolledBack = false, want true (Atomic=true should set this on partial failure)")
	}
	if got.RollbackError != "" {
		t.Errorf("RollbackError populated when rollback should have succeeded: %q", got.RollbackError)
	}
}

// TestInstallHelmRelease_AtomicRollbackFailureSetsRollbackError covers
// the catastrophic case — install failed, rollback ALSO failed.
func TestInstallHelmRelease_AtomicRollbackFailureSetsRollbackError(t *testing.T) {
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) {
		return fixtureChart(t), nil
	})
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return installManifestYAML, nil
	})
	withPreviewSAR(t, allowAllSAR)
	withBuildHelmActionConfig(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*action.Configuration, error) {
		return &action.Configuration{}, nil
	})
	withInstallRun(t, func(_ context.Context, _ *action.Configuration, _ InstallArgs) (*release.Release, error) {
		return nil, errors.New("install failed: an error occurred while rolling back the release: rollback also failed")
	})

	args := InstallArgs{
		Ref: "oci://example/c", Version: "1.0.0",
		Namespace: "app-ns", ReleaseName: "web",
		Atomic: true,
	}
	got, err := InstallHelmRelease(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err == nil {
		t.Fatal("expected error")
	}
	if got == nil {
		t.Fatal("expected partial result with RollbackError")
	}
	if got.RollbackError == "" {
		t.Error("RollbackError should be populated when rollback failed")
	}
	if got.RolledBack {
		t.Error("RolledBack should be false when rollback ALSO failed (catastrophic case)")
	}
}

// TestInstallHelmRelease_ValidationMissingFields covers handler-side
// validation that escaped through to the function (handler validates
// first, but the function double-checks for direct callers).
func TestInstallHelmRelease_ValidationMissingFields(t *testing.T) {
	cases := []struct {
		name string
		args InstallArgs
	}{
		{"empty ref", InstallArgs{Version: "1.0", Namespace: "n", ReleaseName: "r"}},
		{"empty version", InstallArgs{Ref: "oci://x/c", Namespace: "n", ReleaseName: "r"}},
		{"empty namespace", InstallArgs{Ref: "oci://x/c", Version: "1.0", ReleaseName: "r"}},
		{"empty releaseName", InstallArgs{Ref: "oci://x/c", Version: "1.0", Namespace: "n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := InstallHelmRelease(context.Background(), nil, clusters.Cluster{Name: "k"}, tc.args)
			if err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

// TestIsRollbackFailure covers the helm-internal-message sniffer.
// Both stable substrings (current and historical) should match;
// arbitrary errors should not.
func TestIsRollbackFailure(t *testing.T) {
	cases := map[string]bool{
		"":                                                                                false,
		"unrelated error":                                                                 false,
		"install failed: an error occurred while rolling back the release: x":             true,
		"install failed: release rollback failed: y":                                      true,
		"install failed: deployment ready check timed out":                                false,
		"context deadline exceeded":                                                       false,
	}
	for msg, want := range cases {
		t.Run(msg, func(t *testing.T) {
			err := errors.New(msg)
			if msg == "" {
				err = nil
			}
			if got := isRollbackFailure(err); got != want {
				t.Errorf("isRollbackFailure(%q) = %v, want %v", msg, got, want)
			}
		})
	}
}

// TestDeniedError_ErrorMessage confirms the formatted error string
// includes the count for log readability.
func TestDeniedError_ErrorMessage(t *testing.T) {
	e := &DeniedError{Denials: []PreviewDenial{{}, {}, {}}}
	if e.Error() != "pre-flight RBAC denied 3 resource(s)" {
		t.Errorf("Error() = %q", e.Error())
	}
}

// TestIsDeniedError_DetectsViaErrorsAs covers the public detector
// used by handlers.
func TestIsDeniedError_DetectsViaErrorsAs(t *testing.T) {
	e := &DeniedError{Denials: []PreviewDenial{{Verb: "create", Resource: "pods"}}}
	wrapped := errors.New("wrapper: " + e.Error())
	if _, ok := IsDeniedError(wrapped); ok {
		t.Error("plain wrapped string error should NOT match (no errors.As chain)")
	}
	if _, ok := IsDeniedError(e); !ok {
		t.Error("direct DeniedError should match")
	}
}
