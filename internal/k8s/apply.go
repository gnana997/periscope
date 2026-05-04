// Package k8s — generic resource mutation helpers (PR-D).
//
// ApplyResource implements the layered validation pipeline that fronts
// every editable surface in the SPA:
//
//	L1 parse        — YAML → unstructured object, size cap, well-formed
//	L2 identity     — apiVersion/kind/name/namespace match the URL ref;
//	                  reject writes to managed metadata fields the user
//	                  shouldn't be authoring (managedFields, status, uid…)
//	L4 dry-run      — forward to apiserver with ?dryRun=All so admission
//	                  webhooks (PSA, Gatekeeper, Kyverno) and apiserver
//	                  schema validation get to weigh in before the user
//	                  commits the change
//	L5 apply        — PATCH with application/apply-patch+yaml,
//	                  FieldManager=periscope-spa, Force=false unless the
//	                  caller passes Force=true (used for the user's
//	                  explicit second-attempt "force conflict resolution")
//
// L3 (per-resource immutables — Pod containers[].name, Service
// spec.clusterIP, etc.) is intentionally deferred. The apiserver rejects
// these via L4/L5 with clear error messages; pre-flight L3 is purely a
// nicer-error optimisation that grows over time as we encounter real
// pain points. Adding it later is a single map registry.
//
// DeleteResource is the symmetric path for the DELETE verb; same dynamic
// client, same impersonation, no validation pipeline because there's
// nothing to validate beyond the URL ref.
//
// Both functions go through the *dynamic* client (not typed clientsets)
// so they work for any resource type the user can list — Pod today,
// CRDs tomorrow, with no per-type code in this file.
package k8s

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// MaxApplyBytes caps incoming YAML payloads. 256 KiB is well above any
// realistic resource (a Deployment with 80 containers and verbose env
// fits easily) and well below memory-pressure territory. Defends
// against accidental or malicious oversized payloads.
const MaxApplyBytes = 256 * 1024

// ApplyResourceArgs is the input for an SSA-style edit. ResourceVersion
// is intentionally NOT here — server-side apply doesn't need it; conflicts
// are resolved per-field via managedFields, not via optimistic locking.
type ApplyResourceArgs struct {
	Cluster   clusters.Cluster
	Group     string
	Version   string
	Resource  string // plural, e.g. "pods", "deployments", "configmaps"
	Namespace string // empty for cluster-scoped
	Name      string
	Body      []byte // raw YAML payload from the SPA
	DryRun    bool   // L4 only — does not commit
	Force     bool   // resolve field-manager conflicts (opt-in)
}

// ApplyResourceResult is what we return on success. Object is the
// server-returned state (post-apply, or post-dry-run when DryRun was true).
type ApplyResourceResult struct {
	Object map[string]interface{} `json:"object"`
	DryRun bool                   `json:"dryRun"`
}

// fieldManager is the SSA field-manager string Periscope uses for every
// mutation. Stable across releases — operators can search managedFields
// for "periscope-spa" to see exactly which fields the SPA has touched.
// Changing this would silently re-own all previously managed fields, so
// don't.
const fieldManager = "periscope-spa"

// disallowedMetadataPaths are fields under metadata.* that callers must
// not write through the SPA editor. Either server-managed (uid,
// creationTimestamp, generation, resourceVersion, managedFields) or
// best-edited via separate workflows (deletionTimestamp, finalizers).
//
// We strip these before sending to the apiserver. Stripping (rather than
// rejecting) is friendlier UX: many users will have just edited the YAML
// view as displayed, which still includes these fields.
var disallowedMetadataPaths = []string{
	"uid",
	"creationTimestamp",
	"generation",
	"resourceVersion",
	"managedFields",
	"deletionTimestamp",
	"deletionGracePeriodSeconds",
	"selfLink",
}

// newDynamicClientForApply is swapped out by tests for a fake dynamic
// client. Production path: build a rest.Config via buildRestConfig
// (shared with meta.go) and a dynamic.Interface. Mirrors
// newDynamicClientForMeta so apply has the same testability shape.
var newDynamicClientForApply = func(ctx context.Context, p credentials.Provider, c clusters.Cluster) (dynamic.Interface, error) {
	cfg, err := buildRestConfig(ctx, p, c)
	if err != nil {
		return nil, err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("apply: build dynamic client: %w", err)
	}
	return dyn, nil
}

// ApplyResource performs the validation pipeline + dynamic-client SSA.
// All errors are wrapped with a stable prefix the handler can use to
// classify (apiserver errors flow through unwrapped so kerrors.IsXxx
// keeps working in cmd/periscope/errors.go).
func ApplyResource(ctx context.Context, p credentials.Provider, args ApplyResourceArgs) (ApplyResourceResult, error) {
	// --- L1 parse -------------------------------------------------------
	if len(args.Body) == 0 {
		return ApplyResourceResult{}, fmt.Errorf("apply: empty body")
	}
	if len(args.Body) > MaxApplyBytes {
		return ApplyResourceResult{}, fmt.Errorf("apply: body exceeds %d bytes", MaxApplyBytes)
	}
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal(args.Body, &obj.Object); err != nil {
		return ApplyResourceResult{}, fmt.Errorf("apply: parse yaml: %w", err)
	}
	if obj.Object == nil {
		return ApplyResourceResult{}, fmt.Errorf("apply: empty document")
	}

	// --- L2 identity ----------------------------------------------------
	if err := validateIdentity(obj, args); err != nil {
		return ApplyResourceResult{}, err
	}
	stripDisallowedMetadata(obj)

	// --- build dynamic client ------------------------------------------
	dyn, err := newDynamicClientForApply(ctx, p, args.Cluster)
	if err != nil {
		return ApplyResourceResult{}, err
	}
	gvr := schema.GroupVersionResource{
		Group:    args.Group,
		Version:  args.Version,
		Resource: args.Resource,
	}
	var ri dynamic.ResourceInterface
	if args.Namespace != "" {
		ri = dyn.Resource(gvr).Namespace(args.Namespace)
	} else {
		ri = dyn.Resource(gvr)
	}

	// --- L4 / L5 apply --------------------------------------------------
	patchBody, err := obj.MarshalJSON()
	if err != nil {
		return ApplyResourceResult{}, fmt.Errorf("apply: marshal patch: %w", err)
	}
	patchOpts := metav1.PatchOptions{
		FieldManager: fieldManager,
		Force:        &args.Force,
	}
	if args.DryRun {
		patchOpts.DryRun = []string{metav1.DryRunAll}
	}

	got, err := ri.Patch(ctx, args.Name, types.ApplyPatchType, patchBody, patchOpts)
	if err != nil {
		// Pass apiserver errors through unwrapped so the HTTP layer's
		// kerrors classification (Forbidden → 403, Conflict → 409,
		// BadRequest → 400, etc.) keeps working.
		return ApplyResourceResult{}, err
	}
	return ApplyResourceResult{Object: got.Object, DryRun: args.DryRun}, nil
}

// DeleteResourceArgs identifies what to delete. PropagationPolicy is
// fixed at "Background" — kubectl's default, and the right call for
// almost everything an operator deletes through a UI. If a future
// caller needs Foreground or Orphan we'll add it then.
type DeleteResourceArgs struct {
	Cluster   clusters.Cluster
	Group     string
	Version   string
	Resource  string
	Namespace string
	Name      string
}

// DeleteResource removes the resource via the dynamic client under the
// caller's impersonated identity. Apiserver-level errors propagate
// unwrapped (Forbidden → 403, NotFound → 404).
func DeleteResource(ctx context.Context, p credentials.Provider, args DeleteResourceArgs) error {
	cfg, err := buildRestConfig(ctx, p, args.Cluster)
	if err != nil {
		return err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("delete: build dynamic client: %w", err)
	}
	gvr := schema.GroupVersionResource{
		Group:    args.Group,
		Version:  args.Version,
		Resource: args.Resource,
	}
	var ri dynamic.ResourceInterface
	if args.Namespace != "" {
		ri = dyn.Resource(gvr).Namespace(args.Namespace)
	} else {
		ri = dyn.Resource(gvr)
	}
	background := metav1.DeletePropagationBackground
	return ri.Delete(ctx, args.Name, metav1.DeleteOptions{
		PropagationPolicy: &background,
	})
}

// --- L2 helpers -----------------------------------------------------------

// validateIdentity ensures the YAML the user submitted matches the URL
// they POSTed it to. Without this check, a write authorised by RBAC for
// "edit pod payments/foo" could silently cross-write any other pod or
// even a different namespace.
func validateIdentity(obj *unstructured.Unstructured, args ApplyResourceArgs) error {
	wantAPIVersion := args.Version
	if args.Group != "" {
		wantAPIVersion = args.Group + "/" + args.Version
	}
	if got := obj.GetAPIVersion(); got != wantAPIVersion {
		return fmt.Errorf("apply: apiVersion mismatch: body has %q, expected %q", got, wantAPIVersion)
	}
	if obj.GetKind() == "" {
		return errors.New("apply: kind missing")
	}
	if got := obj.GetName(); got != args.Name {
		return fmt.Errorf("apply: metadata.name mismatch: body has %q, expected %q", got, args.Name)
	}
	if got := obj.GetNamespace(); got != args.Namespace {
		return fmt.Errorf("apply: metadata.namespace mismatch: body has %q, expected %q", got, args.Namespace)
	}
	return nil
}

// stripDisallowedMetadata removes server-managed fields that the user
// has no business writing. status is also stripped — it's a server
// projection, never input.
func stripDisallowedMetadata(obj *unstructured.Unstructured) {
	meta, ok := obj.Object["metadata"].(map[string]interface{})
	if ok {
		for _, k := range disallowedMetadataPaths {
			delete(meta, k)
		}
	}
	delete(obj.Object, "status")
}
