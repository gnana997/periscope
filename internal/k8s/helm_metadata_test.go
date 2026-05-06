package k8s

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/gnana997/periscope/internal/clusters"
)

// metaCluster returns a synthetic cluster used by these tests.
// cacheHelmDriver primes the storage-driver auto-probe so the
// metadata helpers don't need a working LIST reactor for probe.
func metaCluster(t *testing.T, drv string) clusters.Cluster {
	t.Helper()
	c := clusters.Cluster{Name: "meta-test-cluster"}
	cacheHelmDriver(c.Name, drv)
	t.Cleanup(func() {
		helmDriverCacheMu.Lock()
		delete(helmDriverCache, c.Name)
		helmDriverCacheMu.Unlock()
	})
	return c
}

// TestWritePeriscopeInstallMetadata_SecretDriver covers the happy path
// for the default Secret-backed driver. Patches an existing storage
// Secret with the install-ref annotation; verifies the round-trip.
func TestWritePeriscopeInstallMetadata_SecretDriver(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sh.helm.release.v1.web.v1",
			Namespace: "app-ns",
		},
	})
	withFakeClient(t, cs)
	c := metaCluster(t, "secret")

	if err := WritePeriscopeInstallMetadata(
		context.Background(), stubProvider{}, c,
		"app-ns", "web", 1,
		"oci://example.com/charts/web", "",
	); err != nil {
		t.Fatalf("WritePeriscopeInstallMetadata: %v", err)
	}

	got, err := cs.CoreV1().Secrets("app-ns").Get(context.Background(), "sh.helm.release.v1.web.v1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if got.Annotations[annotationInstallRef] != "oci://example.com/charts/web" {
		t.Errorf("install-ref annotation = %q, want %q",
			got.Annotations[annotationInstallRef], "oci://example.com/charts/web")
	}
	if _, hasName := got.Annotations[annotationInstallChartName]; hasName {
		t.Error("chart-name annotation should not be set when chartName arg is empty")
	}
}

// TestWritePeriscopeInstallMetadata_ConfigMapDriver mirrors the
// secret-driver test for the ConfigMap-backed deployment shape. Both
// annotations land when both args are non-empty.
func TestWritePeriscopeInstallMetadata_ConfigMapDriver(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sh.helm.release.v1.web.v2",
			Namespace: "app-ns",
		},
	})
	withFakeClient(t, cs)
	c := metaCluster(t, "configmap")

	if err := WritePeriscopeInstallMetadata(
		context.Background(), stubProvider{}, c,
		"app-ns", "web", 2,
		"https://charts.example.com/", "nginx",
	); err != nil {
		t.Fatalf("WritePeriscopeInstallMetadata: %v", err)
	}

	got, err := cs.CoreV1().ConfigMaps("app-ns").Get(context.Background(), "sh.helm.release.v1.web.v2", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get configmap: %v", err)
	}
	if got.Annotations[annotationInstallChartName] != "nginx" {
		t.Errorf("chart-name annotation = %q, want %q",
			got.Annotations[annotationInstallChartName], "nginx")
	}
	if got.Annotations[annotationInstallRef] != "https://charts.example.com/" {
		t.Errorf("ref annotation = %q, want %q",
			got.Annotations[annotationInstallRef], "https://charts.example.com/")
	}
}

// TestWritePeriscopeInstallMetadata_NoOpOnEmpty confirms the
// short-circuit: no patch fires when both ref and chartName are empty.
func TestWritePeriscopeInstallMetadata_NoOpOnEmpty(t *testing.T) {
	cs := fake.NewSimpleClientset()
	withFakeClient(t, cs)
	c := metaCluster(t, "secret")

	patchCount := 0
	cs.Fake.PrependReactor("patch", "secrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		patchCount++
		return false, nil, nil
	})

	if err := WritePeriscopeInstallMetadata(
		context.Background(), stubProvider{}, c, "ns", "rel", 1, "", "",
	); err != nil {
		t.Fatalf("expected nil error for empty input, got %v", err)
	}
	if patchCount != 0 {
		t.Errorf("expected no patch reactor calls for empty input, got %d", patchCount)
	}
}

// TestReadPeriscopeInstallMetadata_HappyPath plants a Secret with the
// install-ref annotation and verifies the read returns it.
func TestReadPeriscopeInstallMetadata_HappyPath(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sh.helm.release.v1.web.v3",
			Namespace: "app-ns",
			Annotations: map[string]string{
				annotationInstallRef: "oci://ghcr.io/example/web",
			},
		},
	})
	withFakeClient(t, cs)
	c := metaCluster(t, "secret")

	ref, chartName, err := ReadPeriscopeInstallMetadata(
		context.Background(), stubProvider{}, c, "app-ns", "web", 3,
	)
	if err != nil {
		t.Fatalf("ReadPeriscopeInstallMetadata: %v", err)
	}
	if ref != "oci://ghcr.io/example/web" {
		t.Errorf("ref = %q, want %q", ref, "oci://ghcr.io/example/web")
	}
	if chartName != "" {
		t.Errorf("chartName = %q, want empty (OCI ref)", chartName)
	}
}

// TestReadPeriscopeInstallMetadata_NotFoundReturnsEmpty confirms a
// missing storage Secret returns ("", "", nil) — the common case for
// freshly-installed releases that the read happens against before
// any annotation has been written.
func TestReadPeriscopeInstallMetadata_NotFoundReturnsEmpty(t *testing.T) {
	cs := fake.NewSimpleClientset()
	withFakeClient(t, cs)
	c := metaCluster(t, "secret")

	ref, chartName, err := ReadPeriscopeInstallMetadata(
		context.Background(), stubProvider{}, c, "ns", "missing", 1,
	)
	if err != nil {
		t.Fatalf("expected nil error for missing release storage, got %v", err)
	}
	if ref != "" || chartName != "" {
		t.Errorf("expected empty (ref, chartName) on NotFound, got %q + %q", ref, chartName)
	}
}

// TestReadPeriscopeInstallMetadata_NoAnnotationsReturnsEmpty covers
// the common case for releases NOT installed via Periscope: the
// storage Secret exists but has no Periscope annotations on it.
func TestReadPeriscopeInstallMetadata_NoAnnotationsReturnsEmpty(t *testing.T) {
	cs := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sh.helm.release.v1.web.v1",
			Namespace: "app-ns",
		},
	})
	withFakeClient(t, cs)
	c := metaCluster(t, "secret")

	ref, chartName, err := ReadPeriscopeInstallMetadata(
		context.Background(), stubProvider{}, c, "app-ns", "web", 1,
	)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if ref != "" || chartName != "" {
		t.Errorf("expected empty values on no-annotations release, got %q + %q", ref, chartName)
	}
}

// TestReadPeriscopeInstallMetadata_ReturnsRealError ensures non-NotFound
// errors (e.g., 403 RBAC denial) are surfaced rather than swallowed.
// The caller (GetHelmRelease) decides whether to log + continue.
func TestReadPeriscopeInstallMetadata_ReturnsRealError(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.Fake.PrependReactor("get", "secrets", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "", Resource: "secrets"},
			"sh.helm.release.v1.web.v1",
			errors.New("rbac denied"),
		)
	})
	withFakeClient(t, cs)
	c := metaCluster(t, "secret")

	_, _, err := ReadPeriscopeInstallMetadata(
		context.Background(), stubProvider{}, c, "ns", "web", 1,
	)
	if err == nil {
		t.Error("expected non-nil error for forbidden secret read")
	}
	if !apierrors.IsForbidden(err) {
		t.Errorf("expected wrapped IsForbidden, got %v", err)
	}
}
