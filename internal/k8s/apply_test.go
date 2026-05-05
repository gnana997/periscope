package k8s

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// applyDeploymentBody is a minimal valid Deployment YAML used as the
// SSA payload for ApplyResource tests. Name/namespace are filled in
// via fmt to keep each test self-describing.
const applyDeploymentBody = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
spec:
  replicas: 2
  selector:
    matchLabels:
      app: nginx
  template:
    metadata:
      labels:
        app: nginx
    spec:
      containers:
        - name: nginx
          image: nginx:1.27
`

// deploymentGVR is the dynamic-client GVR for apps/v1 Deployments,
// shared across the apply-pipeline test cases.
var deploymentGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

// applyTestCluster is a stable Cluster value the swap-point reactor
// can ignore — all test cases route through the fake dynamic client.
var applyTestCluster = clusters.Cluster{Name: "test"}

// fakeDynamicWithApplyReactor builds a fake dynamic client wired with a
// PATCH reactor that simulates Server-Side Apply create-or-update
// semantics. client-go's stock fake client returns "PatchType not
// supported" for ApplyPatchType (kubernetes/client-go#1323), so the
// reactor is required for any SSA path. The reactor:
//
//   - Returns bodyOverride if non-nil (used for e.g. Conflict / NotFound
//     simulations).
//   - Inspects the apply body, looks up the GVR + namespace + name in
//     the tracker, and dispatches to Update if present, Create if not.
//   - Honors patchOpts.DryRun by returning the would-be object without
//     persisting to the tracker.
//
// All four Tier-2 audit cases the test exercises (create, update,
// dry-run, namespace-not-found) flow through this single reactor so
// the fake stays close to how the real apiserver routes the patch.
func fakeDynamicWithApplyReactor(t *testing.T, errOverride error, seed ...runtime.Object) *dynamicfake.FakeDynamicClient {
	t.Helper()
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		deploymentGVR: "DeploymentList",
	}
	fake := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, seed...)

	fake.PrependReactor("patch", "deployments", func(action ktesting.Action) (bool, runtime.Object, error) {
		if errOverride != nil {
			return true, nil, errOverride
		}
		// Concrete PatchActionImpl carries PatchOptions (incl. DryRun /
		// FieldManager / Force); the PatchAction interface alone does
		// not. Use the concrete type so the reactor can mirror
		// apiserver dry-run semantics.
		patchAction, ok := action.(ktesting.PatchActionImpl)
		if !ok {
			return false, nil, nil
		}
		if patchAction.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil
		}
		obj := &unstructured.Unstructured{}
		if err := obj.UnmarshalJSON(patchAction.GetPatch()); err != nil {
			return true, nil, err
		}
		// Dry-run: return what we would have written, don't touch the
		// tracker. Mirrors the apiserver's `?dryRun=All` semantics.
		if hasDryRunAll(patchAction.GetPatchOptions().DryRun) {
			return true, obj, nil
		}
		// Look up by name in the tracker; create-or-update.
		ns := patchAction.GetNamespace()
		existing, err := fake.Tracker().Get(deploymentGVR, ns, patchAction.GetName())
		if err != nil || existing == nil {
			if err := fake.Tracker().Create(deploymentGVR, obj, ns); err != nil {
				return true, nil, err
			}
			return true, obj, nil
		}
		if err := fake.Tracker().Update(deploymentGVR, obj, ns); err != nil {
			return true, nil, err
		}
		return true, obj, nil
	})
	return fake
}

// hasDryRunAll reports whether the patch options requested an
// apiserver-side dry-run.
func hasDryRunAll(dr []string) bool {
	for _, v := range dr {
		if v == metav1.DryRunAll {
			return true
		}
	}
	return false
}

// installFakeDynamicClient swaps newDynamicClientForApply for the
// duration of the test. Restores the original on cleanup.
func installFakeDynamicClient(t *testing.T, fake dynamic.Interface) {
	t.Helper()
	orig := newDynamicClientForApply
	newDynamicClientForApply = func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (dynamic.Interface, error) {
		return fake, nil
	}
	t.Cleanup(func() { newDynamicClientForApply = orig })
}

// applyArgs builds an ApplyResourceArgs for a Deployment matching the
// body produced by deploymentBody(name, namespace). Centralising this
// keeps the URL-vs-body identity consistent across cases.
func applyArgs(name, namespace string, dryRun bool) ApplyResourceArgs {
	return ApplyResourceArgs{
		Cluster:   applyTestCluster,
		Group:     "apps",
		Version:   "v1",
		Resource:  "deployments",
		Namespace: namespace,
		Name:      name,
		Body:      deploymentBody(name, namespace),
		DryRun:    dryRun,
	}
}

func deploymentBody(name, namespace string) []byte {
	return []byte(fmt.Sprintf(applyDeploymentBody, name, namespace))
}

func TestApplyResource_CreatesNewObject(t *testing.T) {
	fake := fakeDynamicWithApplyReactor(t, nil)
	installFakeDynamicClient(t, fake)

	got, err := ApplyResource(context.Background(), stubProvider{}, applyArgs("new-app", "prod", false))
	if err != nil {
		t.Fatalf("ApplyResource: %v", err)
	}
	if got.DryRun {
		t.Errorf("DryRun = true, want false on real apply")
	}
	if got.Object["kind"] != "Deployment" {
		t.Errorf("returned kind = %v, want Deployment", got.Object["kind"])
	}
	// Tracker should now hold the created object — proves SSA-create
	// reached the apiserver-equivalent.
	if _, err := fake.Tracker().Get(deploymentGVR, "prod", "new-app"); err != nil {
		t.Fatalf("expected tracker to contain created object, got: %v", err)
	}
}

func TestApplyResource_UpdatesExisting(t *testing.T) {
	existing := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "existing-app",
				"namespace": "prod",
			},
			"spec": map[string]interface{}{"replicas": int64(1)},
		},
	}
	fake := fakeDynamicWithApplyReactor(t, nil, existing)
	installFakeDynamicClient(t, fake)

	got, err := ApplyResource(context.Background(), stubProvider{}, applyArgs("existing-app", "prod", false))
	if err != nil {
		t.Fatalf("ApplyResource: %v", err)
	}
	// Body has replicas=2; reactor's Update should have replaced the
	// tracker entry.
	stored, err := fake.Tracker().Get(deploymentGVR, "prod", "existing-app")
	if err != nil {
		t.Fatalf("tracker Get: %v", err)
	}
	u := stored.(*unstructured.Unstructured)
	if reps, _, _ := unstructured.NestedInt64(u.Object, "spec", "replicas"); reps != 2 {
		t.Errorf("post-update replicas = %d, want 2", reps)
	}
	if got.DryRun {
		t.Errorf("DryRun unexpectedly true")
	}
}

func TestApplyResource_DryRunDoesNotPersist(t *testing.T) {
	fake := fakeDynamicWithApplyReactor(t, nil)
	installFakeDynamicClient(t, fake)

	got, err := ApplyResource(context.Background(), stubProvider{}, applyArgs("preview-app", "staging", true))
	if err != nil {
		t.Fatalf("ApplyResource: %v", err)
	}
	if !got.DryRun {
		t.Errorf("DryRun = false, want true")
	}
	if got.Object["kind"] != "Deployment" {
		t.Errorf("returned kind = %v, want Deployment", got.Object["kind"])
	}
	// Critical: dry-run must not mutate the tracker.
	if _, err := fake.Tracker().Get(deploymentGVR, "staging", "preview-app"); !kerrors.IsNotFound(err) {
		t.Errorf("expected NotFound after dry-run, got err=%v", err)
	}
}

func TestApplyResource_NamespaceNotFoundPropagatesUnwrapped(t *testing.T) {
	// Simulate the apiserver's reply when applying into a namespace that
	// doesn't exist. The handler's httpStatusFor relies on
	// kerrors.IsNotFound matching, so we assert the wrapped status type
	// is preserved end-to-end.
	nsErr := kerrors.NewNotFound(
		schema.GroupResource{Resource: "namespaces"},
		"ghost",
	)
	fake := fakeDynamicWithApplyReactor(t, nsErr)
	installFakeDynamicClient(t, fake)

	_, err := ApplyResource(context.Background(), stubProvider{}, applyArgs("orphan", "ghost", false))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !kerrors.IsNotFound(err) {
		t.Errorf("kerrors.IsNotFound = false, want true; err=%v", err)
	}
}

func TestApplyResource_ConflictPropagatesUnwrapped(t *testing.T) {
	// SSA conflicts return 409 with details.causes[] driving the SPA's
	// per-field conflict resolver (docs/api.md §"apply" pattern). The
	// handler depends on kerrors.IsConflict matching.
	conflictErr := kerrors.NewConflict(
		schema.GroupResource{Group: "apps", Resource: "deployments"},
		"contended",
		errors.New("apply conflict: field-manager kustomize-controller owns spec.replicas"),
	)
	fake := fakeDynamicWithApplyReactor(t, conflictErr)
	installFakeDynamicClient(t, fake)

	_, err := ApplyResource(context.Background(), stubProvider{}, applyArgs("contended", "prod", false))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !kerrors.IsConflict(err) {
		t.Errorf("kerrors.IsConflict = false, want true; err=%v", err)
	}
}

func TestApplyResource_RejectsIdentityMismatch(t *testing.T) {
	// L2: body name doesn't match URL name. Must error before the
	// dynamic client is touched, so the install step is intentionally
	// omitted — if the validation regresses, the call would dial out.
	args := applyArgs("url-name", "prod", false)
	args.Body = deploymentBody("body-name", "prod")
	_, err := ApplyResource(context.Background(), stubProvider{}, args)
	if err == nil {
		t.Fatal("expected identity-mismatch error, got nil")
	}
	if !strings.HasPrefix(err.Error(), "apply: ") {
		t.Errorf("error %q missing stable 'apply: ' prefix the handler maps to 400", err.Error())
	}
}

func TestApplyResource_EmptyBodyRejected(t *testing.T) {
	args := applyArgs("x", "prod", false)
	args.Body = nil
	_, err := ApplyResource(context.Background(), stubProvider{}, args)
	if err == nil || !strings.HasPrefix(err.Error(), "apply: ") {
		t.Fatalf("expected 'apply: ' prefixed error, got %v", err)
	}
}
