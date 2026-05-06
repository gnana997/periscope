// helm_metadata.go — Periscope-specific metadata persisted on helm
// release storage objects (#123 follow-up).
//
// Helm fundamentally does NOT store the install ref — it pulls the
// chart from the operator-supplied URL/OCI ref, embeds the chart's
// contents in the release Secret, and discards the original ref.
// That makes the upgrade dialog tedious: every upgrade requires
// pasting the ref again.
//
// We close that gap by patching the helm release storage Secret /
// ConfigMap with two annotations after a successful Periscope
// install or upgrade:
//
//   periscope.io/install-ref         the (oci|http|https)://… ref
//   periscope.io/install-chart-name  HTTP-repo chart name (empty
//                                    for OCI; the name is implicit
//                                    in the ref's last segment)
//
// On subsequent upgrade-dialog opens we read those annotations from
// the latest revision's storage object and pre-fill the dialog.
//
// Limitation (called out to operators): this only works for releases
// installed or upgraded via Periscope. A release installed via
// `helm install` directly has no Periscope annotations on its
// storage object; the first Periscope upgrade still requires
// pasting the ref. After that single Periscope upgrade lands, the
// annotation is on the new revision and future upgrades pre-fill.
//
// Best-effort writes: a failed annotation patch does NOT fail the
// install/upgrade itself. Worst case the operator pastes the ref
// once more next time.

package k8s

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

const (
	annotationInstallRef       = "periscope.io/install-ref"
	annotationInstallChartName = "periscope.io/install-chart-name"
)

// WritePeriscopeInstallMetadata patches the helm release storage
// Secret/ConfigMap for the given (namespace, releaseName, revision)
// with the install ref + chart name as annotations. Best-effort:
// returns errors for callers to log, but install/upgrade success
// is independent of this call's outcome.
//
// Empty `ref` and `chartName` → no-op return (caller's invariant
// ensures we never write garbage; install/upgrade always have a
// non-empty ref by handler validation).
//
// Storage driver is auto-probed via the existing resolveHelmDriver
// helper, so this works on both default Secret-backed and the
// optional ConfigMap-backed deployments.
func WritePeriscopeInstallMetadata(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace, releaseName string, revision int, ref, chartName string) error {
	if ref == "" && chartName == "" {
		return nil
	}
	cs, err := newClientFn(ctx, p, c)
	if err != nil {
		return fmt.Errorf("metadata write: clientset: %w", err)
	}
	drv, err := resolveHelmDriver(ctx, cs, c)
	if err != nil {
		return fmt.Errorf("metadata write: driver: %w", err)
	}
	objName := storageObjectName(releaseName, revision)

	annotations := map[string]string{}
	if ref != "" {
		annotations[annotationInstallRef] = ref
	}
	if chartName != "" {
		annotations[annotationInstallChartName] = chartName
	}
	patchBytes, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"annotations": annotations},
	})
	if err != nil {
		return fmt.Errorf("metadata write: marshal: %w", err)
	}

	switch drv {
	case "secret":
		_, err = cs.CoreV1().Secrets(namespace).Patch(ctx, objName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
	case "configmap":
		_, err = cs.CoreV1().ConfigMaps(namespace).Patch(ctx, objName, types.StrategicMergePatchType, patchBytes, metav1.PatchOptions{})
	default:
		return fmt.Errorf("metadata write: unknown helm driver %q", drv)
	}
	if err != nil {
		return fmt.Errorf("metadata write: patch %s/%s: %w", drv, objName, err)
	}
	return nil
}

// ReadPeriscopeInstallMetadata reads the install ref + chart name
// annotations from the helm release storage Secret/ConfigMap for the
// given revision. Returns empty strings (no error) when:
//
//   - The annotations are absent (release was installed before this
//     mechanism existed, or via the helm CLI directly, or via any
//     non-Periscope tooling).
//   - The storage object exists but has no metadata.annotations map.
//
// Returns a non-nil error only on infrastructure failures (apiserver
// unreachable, RBAC denial). NotFound is treated as "no metadata"
// rather than an error — the caller wants the dialog to open whether
// the annotations are there or not.
func ReadPeriscopeInstallMetadata(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace, releaseName string, revision int) (ref, chartName string, err error) {
	cs, err := newClientFn(ctx, p, c)
	if err != nil {
		return "", "", fmt.Errorf("metadata read: clientset: %w", err)
	}
	drv, err := resolveHelmDriver(ctx, cs, c)
	if err != nil {
		return "", "", fmt.Errorf("metadata read: driver: %w", err)
	}
	objName := storageObjectName(releaseName, revision)

	var ann map[string]string
	switch drv {
	case "secret":
		s, err := cs.CoreV1().Secrets(namespace).Get(ctx, objName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return "", "", nil
			}
			return "", "", fmt.Errorf("metadata read: get secret %s: %w", objName, err)
		}
		ann = s.Annotations
	case "configmap":
		cm, err := cs.CoreV1().ConfigMaps(namespace).Get(ctx, objName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return "", "", nil
			}
			return "", "", fmt.Errorf("metadata read: get configmap %s: %w", objName, err)
		}
		ann = cm.Annotations
	default:
		return "", "", fmt.Errorf("metadata read: unknown helm driver %q", drv)
	}

	if ann == nil {
		return "", "", nil
	}
	return ann[annotationInstallRef], ann[annotationInstallChartName], nil
}
