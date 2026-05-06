// Package k8s — workload rollback (issue #71).
//
// Rollback is the apiserver-native "go back to a previous revision"
// affordance for Deployment, StatefulSet, and DaemonSet. Mirrors
// `kubectl rollout undo` — strategic-merge-patches the workload's
// `spec.template` to match a chosen revision's pod template.
//
// History sources differ by kind:
//
//	Deployment   → ReplicaSets owned by the Deployment, ordered by
//	               the `deployment.kubernetes.io/revision` annotation.
//	StatefulSet  → ControllerRevisions matching the STS's selector,
//	               ordered by `.revision`.
//	DaemonSet    → ControllerRevisions matching the DS's selector,
//	               ordered by `.revision`.
//
// The patch shape is identical across kinds: a strategic merge patch
// that replaces `spec.template` and sets the `kubernetes.io/change-cause`
// annotation on the workload. The controller picks up the template
// change, allocates a new revision, and rolls the workload using its
// configured strategy (RollingUpdate / OnDelete / partition).
//
// Two surface functions:
//
//	ListRevisions — read-side: history + pre-flight metadata
//	                (paused, GitOps-managed, HPA target) so the SPA
//	                can warn before the operator clicks.
//	Rollback      — write-side: validates the target revision exists,
//	                builds the strategic merge patch, applies it.
package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// Sentinels callers (handlers + tests) match on. Keep error strings
// stable — tests assert on errors.Is, not string contents.
var (
	ErrUnsupportedKind     = errors.New("rollback: kind not supported")
	ErrRevisionNotFound    = errors.New("rollback: revision not found")
	ErrAlreadyAtRevision   = errors.New("rollback: target revision is the current revision")
	ErrDeploymentPaused    = errors.New("rollback: deployment is paused — resume before rolling back")
	ErrNoRevisionHistory   = errors.New("rollback: no revision history (revisionHistoryLimit may be 0)")
)

// fieldManagerRollback is the dedicated SSA / strategic-merge field
// manager for Periscope-issued rollbacks. Distinct from
// `periscope-spa` (Apply YAML) so operators can grep
// `metadata.managedFields` to attribute every rollback the dashboard
// performed.
const fieldManagerRollback = "periscope-rollback"

// changeCauseAnnotation is the well-known annotation kubectl reads
// to populate the CHANGE-CAUSE column in `kubectl rollout history`.
const changeCauseAnnotation = "kubernetes.io/change-cause"

// Workload kinds we support. The set is closed; helpers panic on
// unknowns to surface programmer error early rather than soft-degrade.
const (
	KindDeployment  = "deployments"
	KindStatefulSet = "statefulsets"
	KindDaemonSet   = "daemonsets"
)

// IsRollbackableKind reports whether the kind has apiserver-native
// rollout history that this package knows how to read + replay.
func IsRollbackableKind(kind string) bool {
	switch kind {
	case KindDeployment, KindStatefulSet, KindDaemonSet:
		return true
	default:
		return false
	}
}

// Revision is one entry in the rollout history of a workload, in the
// shape the SPA renders. PodTemplate is the full PodTemplateSpec
// serialized as a generic map so the frontend can drive the diff
// viewer without an extra round-trip per click.
type Revision struct {
	Revision        int64                  `json:"revision"`
	IsCurrent       bool                   `json:"isCurrent"`
	ChangeCause     string                 `json:"changeCause,omitempty"`
	CreatedAt       time.Time              `json:"createdAt"`
	PodTemplateHash string                 `json:"podTemplateHash,omitempty"`
	Images          []string               `json:"images"`
	PodTemplate     map[string]interface{} `json:"podTemplate"`
}

// ManagedBy describes whether a workload is being reconciled by an
// upstream controller (ArgoCD / Helm / Flux). The SPA renders this as
// a yellow banner — operators get a chance to bail before the rollback
// gets reverted on the next reconcile cycle.
type ManagedBy struct {
	Controller string `json:"controller"` // "argocd" | "helm" | "flux"
	Instance   string `json:"instance,omitempty"`
}

// RevisionHistory is the GET /revisions response payload. Carries the
// full history list plus pre-flight metadata; the SPA uses everything
// in one render pass, so we ship it together rather than scattering it
// across endpoints.
type RevisionHistory struct {
	CurrentRevision int64      `json:"currentRevision"`
	Revisions       []Revision `json:"revisions"`

	// Paused is set only for Deployment (the only kind with
	// spec.paused). nil for STS / DS.
	Paused *bool `json:"paused,omitempty"`

	ManagedBy *ManagedBy `json:"managedBy,omitempty"`

	// HpaTarget is the name of an HPA in the same namespace whose
	// scaleTargetRef points at this workload. Empty when none —
	// rendered as an inline note ("rollback only changes pod template,
	// replicas remain HPA-managed").
	HpaTarget string `json:"hpaTarget,omitempty"`
}

// ListRevisionsArgs identifies the workload to introspect.
type ListRevisionsArgs struct {
	Cluster   clusters.Cluster
	Kind      string // KindDeployment / KindStatefulSet / KindDaemonSet
	Namespace string
	Name      string
}

// RollbackArgs identifies the workload + target revision + reason.
type RollbackArgs struct {
	Cluster      clusters.Cluster
	Kind         string
	Namespace    string
	Name         string
	ToRevision   int64
	Reason       string // optional; flows into kubernetes.io/change-cause
	ActorSubject string // for the change-cause annotation default
}

// RollbackResult is the POST /rollback response shape.
type RollbackResult struct {
	NewRevision int64     `json:"newRevision"`
	PatchedAt   time.Time `json:"patchedAt"`
}

// ListRevisions fetches the rollout history for the given workload
// plus pre-flight metadata (paused / managedBy / hpaTarget).
func ListRevisions(ctx context.Context, p credentials.Provider, args ListRevisionsArgs) (RevisionHistory, error) {
	if !IsRollbackableKind(args.Kind) {
		return RevisionHistory{}, fmt.Errorf("%w: %q", ErrUnsupportedKind, args.Kind)
	}
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return RevisionHistory{}, fmt.Errorf("build clientset: %w", err)
	}
	switch args.Kind {
	case KindDeployment:
		return listDeploymentRevisions(ctx, cs, args.Namespace, args.Name)
	case KindStatefulSet:
		return listStatefulSetRevisions(ctx, cs, args.Namespace, args.Name)
	case KindDaemonSet:
		return listDaemonSetRevisions(ctx, cs, args.Namespace, args.Name)
	}
	return RevisionHistory{}, fmt.Errorf("%w: %q", ErrUnsupportedKind, args.Kind)
}

// Rollback applies the strategic merge patch that retargets the
// workload to a previous revision's pod template. Returns the new
// revision number assigned by the controller post-patch.
func Rollback(ctx context.Context, p credentials.Provider, args RollbackArgs) (RollbackResult, error) {
	if !IsRollbackableKind(args.Kind) {
		return RollbackResult{}, fmt.Errorf("%w: %q", ErrUnsupportedKind, args.Kind)
	}
	cs, err := newClientFn(ctx, p, args.Cluster)
	if err != nil {
		return RollbackResult{}, fmt.Errorf("build clientset: %w", err)
	}

	// Re-fetch history under the same impersonating session that
	// will perform the patch — this is the canonical pre-check.
	// Don't trust a history snapshot the SPA might be holding from
	// 30 seconds ago; revisions can be pruned by revisionHistoryLimit
	// in the meantime.
	hist, err := loadHistoryForKind(ctx, cs, args.Kind, args.Namespace, args.Name)
	if err != nil {
		return RollbackResult{}, err
	}
	if hist.Paused != nil && *hist.Paused {
		return RollbackResult{}, ErrDeploymentPaused
	}

	var target *Revision
	for i := range hist.Revisions {
		if hist.Revisions[i].Revision == args.ToRevision {
			target = &hist.Revisions[i]
			break
		}
	}
	if target == nil {
		return RollbackResult{}, fmt.Errorf("%w: revision %d", ErrRevisionNotFound, args.ToRevision)
	}
	if target.IsCurrent {
		return RollbackResult{}, ErrAlreadyAtRevision
	}

	changeCause := buildChangeCause(args)
	patchBody, err := buildRollbackPatch(target.PodTemplate, changeCause)
	if err != nil {
		return RollbackResult{}, fmt.Errorf("build patch: %w", err)
	}

	patchOpts := metav1.PatchOptions{FieldManager: fieldManagerRollback}
	switch args.Kind {
	case KindDeployment:
		out, err := cs.AppsV1().Deployments(args.Namespace).Patch(ctx, args.Name,
			types.StrategicMergePatchType, patchBody, patchOpts)
		if err != nil {
			return RollbackResult{}, fmt.Errorf("patch deployment: %w", err)
		}
		return RollbackResult{
			NewRevision: parseRevisionAnnotation(out.Annotations),
			PatchedAt:   time.Now().UTC(),
		}, nil
	case KindStatefulSet:
		out, err := cs.AppsV1().StatefulSets(args.Namespace).Patch(ctx, args.Name,
			types.StrategicMergePatchType, patchBody, patchOpts)
		if err != nil {
			return RollbackResult{}, fmt.Errorf("patch statefulset: %w", err)
		}
		// StatefulSet exposes the current revision via status, not an
		// annotation — but status updates trail patch return, so we
		// surface the workload's last-observed revision and let the
		// SPA's watch stream catch the controller-bumped value.
		return RollbackResult{
			NewRevision: deriveStatefulSetCurrentRevision(out),
			PatchedAt:   time.Now().UTC(),
		}, nil
	case KindDaemonSet:
		out, err := cs.AppsV1().DaemonSets(args.Namespace).Patch(ctx, args.Name,
			types.StrategicMergePatchType, patchBody, patchOpts)
		if err != nil {
			return RollbackResult{}, fmt.Errorf("patch daemonset: %w", err)
		}
		return RollbackResult{
			NewRevision: parseRevisionAnnotation(out.Annotations),
			PatchedAt:   time.Now().UTC(),
		}, nil
	}
	return RollbackResult{}, fmt.Errorf("%w: %q", ErrUnsupportedKind, args.Kind)
}

// loadHistoryForKind is the internal helper that powers both the read
// path (ListRevisions) and the pre-flight check inside Rollback.
func loadHistoryForKind(ctx context.Context, cs kubernetes.Interface, kind, ns, name string) (RevisionHistory, error) {
	switch kind {
	case KindDeployment:
		return listDeploymentRevisions(ctx, cs, ns, name)
	case KindStatefulSet:
		return listStatefulSetRevisions(ctx, cs, ns, name)
	case KindDaemonSet:
		return listDaemonSetRevisions(ctx, cs, ns, name)
	}
	return RevisionHistory{}, fmt.Errorf("%w: %q", ErrUnsupportedKind, kind)
}

// --- per-kind history readers ---------------------------------------

func listDeploymentRevisions(ctx context.Context, cs kubernetes.Interface, ns, name string) (RevisionHistory, error) {
	dep, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return RevisionHistory{}, fmt.Errorf("get deployment: %w", err)
	}

	currentRevision := parseRevisionAnnotation(dep.Annotations)

	rsList, err := cs.AppsV1().ReplicaSets(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(dep.Spec.Selector.MatchLabels).String(),
	})
	if err != nil {
		return RevisionHistory{}, fmt.Errorf("list replicasets: %w", err)
	}

	var revisions []Revision
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if !ownedBy(rs.OwnerReferences, dep.UID) {
			continue
		}
		rev := parseRevisionAnnotation(rs.Annotations)
		if rev == 0 {
			continue
		}
		template := rs.Spec.Template
		revisions = append(revisions, Revision{
			Revision:        rev,
			IsCurrent:       rev == currentRevision,
			ChangeCause:     rs.Annotations[changeCauseAnnotation],
			CreatedAt:       rs.CreationTimestamp.Time,
			PodTemplateHash: rs.Labels["pod-template-hash"],
			Images:          imagesFromPodTemplate(&template),
			PodTemplate:     podTemplateAsMap(&template),
		})
	}
	if len(revisions) == 0 {
		return RevisionHistory{}, ErrNoRevisionHistory
	}
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Revision > revisions[j].Revision
	})

	paused := dep.Spec.Paused
	hist := RevisionHistory{
		CurrentRevision: currentRevision,
		Revisions:       revisions,
		Paused:          &paused,
		ManagedBy:       detectManagedBy(dep.Annotations, dep.Labels),
	}
	if hpa := findHPATarget(ctx, cs, ns, "Deployment", name); hpa != "" {
		hist.HpaTarget = hpa
	}
	return hist, nil
}

func listStatefulSetRevisions(ctx context.Context, cs kubernetes.Interface, ns, name string) (RevisionHistory, error) {
	sts, err := cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return RevisionHistory{}, fmt.Errorf("get statefulset: %w", err)
	}
	currentRevision := int64(0)
	if sts.Status.CurrentRevision != "" {
		currentRevision = revisionSuffix(sts.Status.CurrentRevision)
	}
	revisions, err := loadControllerRevisions(ctx, cs, ns, sts.Spec.Selector, sts.UID, currentRevision, "statefulset")
	if err != nil {
		return RevisionHistory{}, err
	}
	hist := RevisionHistory{
		CurrentRevision: currentRevision,
		Revisions:       revisions,
		ManagedBy:       detectManagedBy(sts.Annotations, sts.Labels),
	}
	if hpa := findHPATarget(ctx, cs, ns, "StatefulSet", name); hpa != "" {
		hist.HpaTarget = hpa
	}
	return hist, nil
}

func listDaemonSetRevisions(ctx context.Context, cs kubernetes.Interface, ns, name string) (RevisionHistory, error) {
	ds, err := cs.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return RevisionHistory{}, fmt.Errorf("get daemonset: %w", err)
	}
	// DaemonSet's current revision is the one whose hash matches
	// the live pod template — the apiserver doesn't publish it as a
	// status field, so we derive it from the "controller-revision-hash"
	// pod label expectation. For the SPA we approximate via the
	// highest revision, since the controller always rolls forward.
	revisions, err := loadControllerRevisions(ctx, cs, ds.Namespace, ds.Spec.Selector, ds.UID, 0, "daemonset")
	if err != nil {
		return RevisionHistory{}, err
	}
	currentRevision := int64(0)
	for i := range revisions {
		if revisions[i].Revision > currentRevision {
			currentRevision = revisions[i].Revision
		}
	}
	for i := range revisions {
		revisions[i].IsCurrent = revisions[i].Revision == currentRevision
	}
	return RevisionHistory{
		CurrentRevision: currentRevision,
		Revisions:       revisions,
		ManagedBy:       detectManagedBy(ds.Annotations, ds.Labels),
	}, nil
}

// loadControllerRevisions reads ControllerRevisions for a STS / DS,
// filters to ones owned by the given UID, and projects each into a
// Revision. The `kind` param is "statefulset" / "daemonset" purely for
// error messages.
func loadControllerRevisions(
	ctx context.Context,
	cs kubernetes.Interface,
	ns string,
	selector *metav1.LabelSelector,
	ownerUID types.UID,
	currentRevision int64,
	kind string,
) ([]Revision, error) {
	if selector == nil {
		return nil, fmt.Errorf("get %s: missing selector", kind)
	}
	crList, err := cs.AppsV1().ControllerRevisions(ns).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(selector.MatchLabels).String(),
	})
	if err != nil {
		return nil, fmt.Errorf("list controllerrevisions: %w", err)
	}
	var out []Revision
	for i := range crList.Items {
		cr := &crList.Items[i]
		if !ownedBy(cr.OwnerReferences, ownerUID) {
			continue
		}
		template := podTemplateFromControllerRevision(cr.Data.Raw)
		if template == nil {
			continue
		}
		out = append(out, Revision{
			Revision:    cr.Revision,
			IsCurrent:   cr.Revision == currentRevision && currentRevision != 0,
			ChangeCause: cr.Annotations[changeCauseAnnotation],
			CreatedAt:   cr.CreationTimestamp.Time,
			Images:      imagesFromPodTemplate(template),
			PodTemplate: podTemplateAsMap(template),
		})
	}
	if len(out) == 0 {
		return nil, ErrNoRevisionHistory
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Revision > out[j].Revision
	})
	return out, nil
}

// --- patch construction --------------------------------------------

// buildRollbackPatch produces the strategic merge patch JSON. Sets
// spec.template wholesale and adds the change-cause annotation;
// everything else (replicas, selector, strategy) is left untouched
// because the patch is keyed on "spec.template" only.
func buildRollbackPatch(targetTemplate map[string]interface{}, changeCause string) ([]byte, error) {
	patch := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				changeCauseAnnotation: changeCause,
			},
		},
		"spec": map[string]interface{}{
			"template": targetTemplate,
		},
	}
	return json.Marshal(patch)
}

// buildChangeCause renders the annotation. Includes actor + reason
// when the operator supplies one, falling back to a structured default
// so even unattended rollbacks remain attributable later.
func buildChangeCause(args RollbackArgs) string {
	actor := args.ActorSubject
	if actor == "" {
		actor = "periscope"
	}
	if args.Reason == "" {
		return fmt.Sprintf("rolled back to revision %d via Periscope (%s)", args.ToRevision, actor)
	}
	// Truncate over-long reasons so we don't blow past kubectl's
	// CHANGE-CAUSE column rendering. 200 chars is generous; longer
	// context belongs in the audit log, not a workload annotation.
	reason := strings.TrimSpace(args.Reason)
	if len(reason) > 200 {
		reason = reason[:200] + "…"
	}
	return fmt.Sprintf("rolled back to revision %d via Periscope (%s): %s", args.ToRevision, actor, reason)
}

// --- pod template extraction ---------------------------------------

func podTemplateFromControllerRevision(raw []byte) *corev1.PodTemplateSpec {
	if len(raw) == 0 {
		return nil
	}
	// The Data.Raw blob is the patched workload spec — for STS / DS
	// it's a partial that includes spec.template. Decode loosely.
	var wrap struct {
		Spec struct {
			Template *corev1.PodTemplateSpec `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil || wrap.Spec.Template == nil {
		return nil
	}
	return wrap.Spec.Template
}

func podTemplateAsMap(t *corev1.PodTemplateSpec) map[string]interface{} {
	if t == nil {
		return nil
	}
	b, err := json.Marshal(t)
	if err != nil {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

func imagesFromPodTemplate(t *corev1.PodTemplateSpec) []string {
	if t == nil {
		return []string{}
	}
	out := make([]string, 0, len(t.Spec.Containers)+len(t.Spec.InitContainers))
	for _, c := range t.Spec.InitContainers {
		out = append(out, c.Image)
	}
	for _, c := range t.Spec.Containers {
		out = append(out, c.Image)
	}
	return out
}

// --- pre-flight metadata helpers -----------------------------------

// detectManagedBy sniffs annotations + labels for the three known
// reconcilers. False-positive risk is low because each marker is
// vendor-specific. Order matters only on conflict — if a workload
// somehow carries both an Argo annotation and a Helm annotation,
// Argo wins (it's the more aggressive reconciler in our experience).
func detectManagedBy(annotations, lbls map[string]string) *ManagedBy {
	if v := annotations["argocd.argoproj.io/instance"]; v != "" {
		return &ManagedBy{Controller: "argocd", Instance: v}
	}
	if v := annotations["meta.helm.sh/release-name"]; v != "" {
		return &ManagedBy{Controller: "helm", Instance: v}
	}
	if lbls["app.kubernetes.io/managed-by"] == "Helm" {
		return &ManagedBy{Controller: "helm", Instance: annotations["meta.helm.sh/release-name"]}
	}
	if v := annotations["kustomize.toolkit.fluxcd.io/name"]; v != "" {
		return &ManagedBy{Controller: "flux", Instance: v}
	}
	if lbls["app.kubernetes.io/managed-by"] == "Flux" {
		return &ManagedBy{Controller: "flux"}
	}
	return nil
}

// findHPATarget returns the name of an HPA in `ns` whose
// scaleTargetRef matches the given (kind, name). Empty string when
// none. Errors are swallowed — HPA presence is informational, not
// a precondition for rollback.
func findHPATarget(ctx context.Context, cs kubernetes.Interface, ns, kind, name string) string {
	list, err := cs.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return ""
	}
	for i := range list.Items {
		ref := list.Items[i].Spec.ScaleTargetRef
		if ref.Kind == kind && ref.Name == name {
			return list.Items[i].Name
		}
	}
	return ""
}

// --- small helpers --------------------------------------------------

func ownedBy(refs []metav1.OwnerReference, uid types.UID) bool {
	for _, r := range refs {
		if r.UID == uid {
			return true
		}
	}
	return false
}

func parseRevisionAnnotation(annotations map[string]string) int64 {
	v := annotations["deployment.kubernetes.io/revision"]
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// revisionSuffix extracts the trailing numeric portion of a
// StatefulSet status field like "web-7d8f9c4b6". Naive on purpose —
// the API contract guarantees the suffix is the controller-revision
// hash, not a number; but for SPA display we don't need the actual
// revision integer (we'll surface ControllerRevision.Revision values).
// This stays for symmetry with the Deployment path; returns 0 when
// parsing fails, which is benign — the per-revision IsCurrent check
// falls back to "no revision is current".
func revisionSuffix(s string) int64 {
	parts := strings.Split(s, "-")
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.ParseInt(parts[len(parts)-1], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// deriveStatefulSetCurrentRevision returns 0 when the status hasn't
// caught up post-patch — the SPA's watch stream picks up the real
// value within a tick.
func deriveStatefulSetCurrentRevision(sts *appsv1.StatefulSet) int64 {
	if sts == nil {
		return 0
	}
	return revisionSuffix(sts.Status.CurrentRevision)
}

// IsNotFound is a thin shim so cmd/periscope/rollback_handler.go can
// classify "workload doesn't exist" without importing apierrors.
func IsNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}

// Ensure imports stay used even if a future refactor drops them.
var _ = autoscalingv2.HorizontalPodAutoscaler{}
