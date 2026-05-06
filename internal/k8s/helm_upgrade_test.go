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
	"helm.sh/helm/v3/pkg/storage/driver"
	authv1 "k8s.io/api/authorization/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

func fakeUpgradeReleaseSuccess() *release.Release {
	return &release.Release{
		Name:      "web",
		Namespace: "app-ns",
		Version:   2,
		Info: &release.Info{
			Status:       release.StatusDeployed,
			Notes:        "upgraded — new features at /docs",
			LastDeployed: mustParseTime("2026-05-06T13:00:00Z"),
		},
		Chart: &chart.Chart{
			Metadata: &chart.Metadata{Name: "nginx", Version: "1.1.0"},
		},
	}
}

func withUpgradeRun(t *testing.T, fn func(context.Context, *action.Configuration, UpgradeArgs) (*release.Release, error)) {
	t.Helper()
	prev := upgradeRunFn
	upgradeRunFn = fn
	t.Cleanup(func() { upgradeRunFn = prev })
}

// TestUpgradeHelmRelease_HappyPath walks the full orchestration —
// pre-flight via PreviewHelmUpgrade (verb=patch) + buildHelmActionConfig
// + run seam. Asserts the new revision appears on the response.
func TestUpgradeHelmRelease_HappyPath(t *testing.T) {
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) {
		return fixtureChart(t), nil
	})
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, mode previewMode) (string, error) {
		if mode != modeUpgrade {
			t.Fatalf("expected modeUpgrade in upgrade flow, got %d", mode)
		}
		return upgradeToManifestYAML, nil
	})
	withPreviewGetCurrentRelease(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _, _ string) (string, error) {
		return upgradeFromManifestYAML, nil
	})
	withPreviewSAR(t, allowAllSAR)
	withBuildHelmActionConfig(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*action.Configuration, error) {
		return &action.Configuration{}, nil
	})
	withUpgradeRun(t, func(_ context.Context, _ *action.Configuration, _ UpgradeArgs) (*release.Release, error) {
		return fakeUpgradeReleaseSuccess(), nil
	})

	args := UpgradeArgs{
		Ref: "oci://example/c", Version: "1.1.0",
		Namespace: "app-ns", ReleaseName: "web",
		Atomic: true, Wait: true, Timeout: 5 * time.Minute,
	}
	got, err := UpgradeHelmRelease(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err != nil {
		t.Fatalf("UpgradeHelmRelease: %v", err)
	}
	if got.Release.Revision != 2 {
		t.Errorf("Release.Revision = %d, want 2", got.Release.Revision)
	}
	if got.Release.Chart.Version != "1.1.0" {
		t.Errorf("Release.Chart.Version = %q, want %q", got.Release.Chart.Version, "1.1.0")
	}
}

// TestUpgradeHelmRelease_PreflightDeniedBlocksAction confirms upgrade
// path uses verb=patch in pre-flight (not verb=create) and the same
// blocking behavior as install.
func TestUpgradeHelmRelease_PreflightDeniedBlocksAction(t *testing.T) {
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) {
		return fixtureChart(t), nil
	})
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return upgradeToManifestYAML, nil
	})
	withPreviewGetCurrentRelease(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _, _ string) (string, error) {
		return upgradeFromManifestYAML, nil
	})
	// Verify the SAR loop sees verb=patch on upgrade, not verb=create.
	gotVerbs := []string{}
	withPreviewSAR(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error) {
		gotVerbs = append(gotVerbs, attr.Verb)
		// Deny the Deployment specifically.
		if attr.Resource == "deployments" {
			return false, "denied", nil
		}
		return true, "", nil
	})

	upgradeCalled := false
	withUpgradeRun(t, func(_ context.Context, _ *action.Configuration, _ UpgradeArgs) (*release.Release, error) {
		upgradeCalled = true
		return fakeUpgradeReleaseSuccess(), nil
	})

	args := UpgradeArgs{Ref: "oci://example/c", Version: "1.1.0", Namespace: "app-ns", ReleaseName: "web"}
	_, err := UpgradeHelmRelease(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err == nil {
		t.Fatal("expected error for denied pre-flight")
	}
	if _, ok := IsDeniedError(err); !ok {
		t.Errorf("expected DeniedError, got %T", err)
	}
	if upgradeCalled {
		t.Error("upgrade run should NOT fire on pre-flight denial")
	}
	for _, v := range gotVerbs {
		if v != "patch" {
			t.Errorf("expected SAR verb=patch in upgrade pre-flight, got %q", v)
		}
	}
}

// TestUpgradeHelmRelease_ReleaseNotFoundBubbles confirms the helm SDK's
// driver.ErrReleaseNotFound is preserved as an error chain so
// helmErrorToStatus maps it to 404 at the handler.
func TestUpgradeHelmRelease_ReleaseNotFoundBubbles(t *testing.T) {
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) {
		return fixtureChart(t), nil
	})
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return upgradeToManifestYAML, nil
	})
	withPreviewGetCurrentRelease(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _, _ string) (string, error) {
		return "", driver.ErrReleaseNotFound
	})
	withPreviewSAR(t, allowAllSAR)
	withBuildHelmActionConfig(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*action.Configuration, error) {
		return &action.Configuration{}, nil
	})
	withUpgradeRun(t, func(_ context.Context, _ *action.Configuration, _ UpgradeArgs) (*release.Release, error) {
		return nil, driver.ErrReleaseNotFound
	})

	args := UpgradeArgs{Ref: "oci://example/c", Version: "1.1.0", Namespace: "app-ns", ReleaseName: "web"}
	_, err := UpgradeHelmRelease(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, driver.ErrReleaseNotFound) {
		t.Errorf("expected wrapped driver.ErrReleaseNotFound, got %v", err)
	}
}

// TestUpgradeHelmRelease_AtomicRollbackToPreviousRevision asserts the
// upgrade-with-rollback path returns the rolled-back state. helm SDK
// rolls back to the PREVIOUS revision (not zero), so the response's
// Release block reflects revision N-1 with RolledBack=true.
func TestUpgradeHelmRelease_AtomicRollbackToPreviousRevision(t *testing.T) {
	withFetchAndLoadChart(t, func(_ context.Context, _ PreviewArgs) (*chart.Chart, error) {
		return fixtureChart(t), nil
	})
	withPreviewRender(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ *chart.Chart, _ chartutil.Values, _ PreviewArgs, _ previewMode) (string, error) {
		return upgradeToManifestYAML, nil
	})
	withPreviewGetCurrentRelease(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _, _ string) (string, error) {
		return upgradeFromManifestYAML, nil
	})
	withPreviewSAR(t, allowAllSAR)
	withBuildHelmActionConfig(t, func(_ context.Context, _ credentials.Provider, _ clusters.Cluster, _ string) (*action.Configuration, error) {
		return &action.Configuration{}, nil
	})
	withUpgradeRun(t, func(_ context.Context, _ *action.Configuration, _ UpgradeArgs) (*release.Release, error) {
		return nil, errors.New("upgrade failed: pod stuck pending")
	})

	args := UpgradeArgs{Ref: "oci://example/c", Version: "1.1.0", Namespace: "app-ns", ReleaseName: "web", Atomic: true}
	got, err := UpgradeHelmRelease(context.Background(), nil, clusters.Cluster{Name: "kind"}, args)
	if err == nil {
		t.Fatal("expected error from failed upgrade")
	}
	if got == nil || !got.RolledBack {
		t.Errorf("expected RolledBack=true on Atomic+failure; got=%+v", got)
	}
}

// TestUpgradeHelmRelease_ValidationMissingFields covers function-level
// validation (handlers validate first, but the function double-checks).
func TestUpgradeHelmRelease_ValidationMissingFields(t *testing.T) {
	cases := []struct {
		name string
		args UpgradeArgs
	}{
		{"empty ref", UpgradeArgs{Version: "1.0", Namespace: "n", ReleaseName: "r"}},
		{"empty version", UpgradeArgs{Ref: "oci://x/c", Namespace: "n", ReleaseName: "r"}},
		{"empty namespace", UpgradeArgs{Ref: "oci://x/c", Version: "1.0", ReleaseName: "r"}},
		{"empty releaseName", UpgradeArgs{Ref: "oci://x/c", Version: "1.0", Namespace: "n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UpgradeHelmRelease(context.Background(), nil, clusters.Cluster{Name: "k"}, tc.args)
			if err == nil {
				t.Error("expected validation error")
			}
		})
	}
}
