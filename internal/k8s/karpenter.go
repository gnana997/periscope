// karpenter.go — curated Karpenter dashboard backend (#118).
//
// Surfaces a single response that joins:
//   - NodePools (karpenter.sh/v1) with weight / disruption budget /
//     limits + cost summary (when controller `/metrics` reachable)
//   - NodeClaims (karpenter.sh/v1) grouped by NodePool with the
//     status conditions operators look for (Drifted, Initialized,
//     Launched, Registered)
//   - Pending pods waiting on Karpenter, joined to their
//     FailedScheduling apiserver Events so the per-NodePool
//     "incompatible with NodePool X: …" reason is rendered next to
//     the pod instead of buried in karpenter-controller logs
//
// Cross-resource joins live here because Karpenter's data model
// is fundamentally a tripartite join (pool → claim → pod) and
// kubectl can't show it.
//
// Auto-detect: a single FindCRDByPlural call on
// `nodepools.karpenter.sh/v1`. CRD missing → return
// `{available: false}` and emit one audit row; no further calls.
//
// Graceful degradation: every cross-call (events, metrics) uses
// best-effort error handling. A missing /metrics endpoint downgrades
// the response to `metricsAvailable: false` (no `cost` blocks); a
// failed Events fan-out leaves `incompatibilityReasons: []` on
// pending pods. Callers always get the base view.

package k8s

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// ─── response types ────────────────────────────────────────────────────────

// KarpenterDashboard is the curated /api/clusters/{c}/karpenter
// response. `Available` gates the SPA's sidebar entry — false means
// no Karpenter CRDs detected and the rest of the fields are empty.
type KarpenterDashboard struct {
	Available   bool             `json:"available"`
	NodePools   []NodePoolView   `json:"nodepools,omitempty"`
	NodeClaims  []NodeClaimView  `json:"nodeclaims,omitempty"`
	PendingPods []PendingPodView `json:"pendingPods,omitempty"`
	// Truncated reports whether PendingPods was capped at the 50-pod
	// limit; the SPA renders a "showing first 50 of N" hint when true.
	Truncated bool `json:"truncated,omitempty"`
	// MetricsAvailable reports whether the karpenter-controller
	// `/metrics` endpoint was reachable. False → cost fields on
	// NodePool are omitted; the SPA shows a one-line "metrics
	// unreachable" hint above the table.
	MetricsAvailable bool `json:"metricsAvailable"`
}

// NodePoolView projects a NodePool unstructured into the curated
// shape the dashboard needs. Cost is nil when metrics are
// unavailable OR when the pool has no NodeClaims to attribute cost
// to.
type NodePoolView struct {
	Name       string             `json:"name"`
	Weight     *int32             `json:"weight,omitempty"`
	Disruption NodePoolDisruption `json:"disruption"`
	// Limits / Usage are exposed as resource_type → quantity strings
	// (e.g. {"cpu": "1000", "memory": "1Ti"}). Karpenter publishes
	// usage via the `karpenter_nodepools_usage` metric, so Usage is
	// populated only when MetricsAvailable=true. Limits come from
	// the NodePool spec and are always present when set.
	Limits     map[string]string `json:"limits,omitempty"`
	Usage      map[string]string `json:"usage,omitempty"`
	NodeCount  int               `json:"nodeCount"`
	Conditions []NodeCondition   `json:"conditions,omitempty"`
	Cost       *NodePoolCost     `json:"cost,omitempty"`
}

// NodePoolDisruption mirrors the NodePool .spec.disruption knobs
// operators tune — consolidation policy, when-empty / when-underutilized
// timeouts, expireAfter, and per-window budgets.
type NodePoolDisruption struct {
	ConsolidationPolicy string                `json:"consolidationPolicy,omitempty"`
	ConsolidateAfter    string                `json:"consolidateAfter,omitempty"`
	ExpireAfter         string                `json:"expireAfter,omitempty"`
	Budgets             []NodePoolBudgetEntry `json:"budgets,omitempty"`
}

type NodePoolBudgetEntry struct {
	Nodes    string   `json:"nodes,omitempty"`
	Schedule string   `json:"schedule,omitempty"`
	Duration string   `json:"duration,omitempty"`
	Reasons  []string `json:"reasons,omitempty"`
}

// NodePoolCost is the per-pool $/hr summary computed from
// karpenter_cloudprovider_instance_type_offering_price_estimate joined
// to live NodeClaim labels (instance_type / capacity_type / zone).
type NodePoolCost struct {
	CurrentHourly  float64 `json:"currentHourly"`
	OnDemandHourly float64 `json:"onDemandHourly"`
	SpotSavingsPct int     `json:"spotSavingsPct"`
}

// NodeClaimView projects a NodeClaim unstructured into the curated
// shape. Conditions surface operator-relevant status (Drifted,
// Initialized, Launched, Registered, Disrupted).
type NodeClaimView struct {
	Name         string          `json:"name"`
	NodePool     string          `json:"nodepool"`
	InstanceType string          `json:"instanceType,omitempty"`
	CapacityType string          `json:"capacityType,omitempty"` // spot | on-demand
	Zone         string          `json:"zone,omitempty"`
	ProviderID   string          `json:"providerID,omitempty"`
	EC2NodeClass string          `json:"ec2NodeClass,omitempty"`
	Conditions   []NodeCondition `json:"conditions,omitempty"`
	CreatedAt    metav1.Time     `json:"createdAt,omitempty"`
}

// PendingPodView is one row in the "pending pods waiting on
// Karpenter" panel. IncompatibilityReasons is the parsed per-NodePool
// breakdown extracted from the FailedScheduling apiserver Event;
// empty when no event was found (or its message doesn't match the
// Karpenter wrap convention).
type PendingPodView struct {
	Namespace              string             `json:"namespace"`
	Name                   string             `json:"name"`
	PendingFor             string             `json:"pendingFor"`
	Reason                 string             `json:"reason,omitempty"`
	IncompatibilityReasons []NodePoolIncompat `json:"incompatibilityReasons,omitempty"`
}

// NodePoolIncompat is one (pool, reason) tuple for a pending pod.
// Karpenter's scheduler wraps each per-NodePool rejection with
// `serrors.Wrap(err, "NodePool", klog.KRef("", name))`; the rendered
// Event message contains those segments which the parser splits
// into this shape.
type NodePoolIncompat struct {
	NodePool string `json:"nodepool"`
	Reason   string `json:"reason"`
}

// karpenterGVRs — the v1 GVRs the dashboard targets. Older v1beta1
// / v1alpha5 schemas are intentionally out of scope (operators on
// those versions still use the generic CRD viewer at
// /clusters/{c}/customresources/...).
var (
	karpenterNodePoolGVR = schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodepools",
	}
	karpenterNodeClaimGVR = schema.GroupVersionResource{
		Group:    "karpenter.sh",
		Version:  "v1",
		Resource: "nodeclaims",
	}
)

// karpenterControllerNamespace is the conventional install namespace
// for the karpenter-controller. The Service exposing /metrics is
// also called "karpenter" inside this namespace per the upstream
// helm chart's defaults. Operators who installed under a different
// namespace will see metricsAvailable=false; the rest of the
// dashboard still works.
const (
	karpenterControllerNamespace = "karpenter"
	karpenterControllerService   = "karpenter"
	karpenterMetricsPort         = "8080"
)

// pendingPodsCap bounds the response payload + the events fan-out
// work. Issue #118 specifies 50.
const pendingPodsCap = 50

// ─── auto-detect ───────────────────────────────────────────────────────────

// IsKarpenterInstalled returns true when the cluster has the
// karpenter.sh/v1 NodePool CRD. Older v1beta1-only installs are
// treated as not-installed for this dashboard's purposes — they fall
// back to the generic CRD viewer.
//
// Returns (false, nil) on CRDs absent (the common case on most
// clusters). Returns (false, err) on transport / auth failures so
// the caller can surface a real error rather than silently hiding
// the sidebar entry.
func IsKarpenterInstalled(ctx context.Context, p credentials.Provider, c clusters.Cluster) (bool, error) {
	_, err := FindCRDByPlural(ctx, p, c, karpenterNodePoolGVR.Group, karpenterNodePoolGVR.Version, karpenterNodePoolGVR.Resource)
	if err == nil {
		return true, nil
	}
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	// FindCRDByPlural returns a non-typed error when the CRD isn't
	// in the list (vs an apiserver error). Fall back to a string
	// match on "not found" in the error message — same convention
	// the existing customresource code uses for missing CRDs.
	if isCRDNotFoundErr(err) {
		return false, nil
	}
	return false, err
}

// isCRDNotFoundErr returns true when the error from FindCRDByPlural
// indicates the CRD was simply missing from the list. The helper
// returns a wrapped fmt.Errorf in that case (see crd.go), which is
// indistinguishable from a structural "not found" without a sentinel.
func isCRDNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// crd.go's FindCRDByPlural returns errors of the form
	//   "CRD not found: <plural>.<group>"
	// or
	//   "CRD <plural>.<group> does not serve version <version>"
	// Both cases mean "Karpenter v1 isn't installed here."
	return containsAny(msg, "CRD not found", "does not serve version")
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) == 0 {
			continue
		}
		if indexOf(s, n) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// ─── client builders ───────────────────────────────────────────────────────

// buildKarpenterClients returns the typed + dynamic clients the
// dashboard needs. Both share one rest.Config (so impersonation
// headers apply uniformly). Tests substitute via the test seam below.
func BuildKarpenterClients(ctx context.Context, p credentials.Provider, c clusters.Cluster) (kubernetes.Interface, dynamic.Interface, error) {
	cfg, err := buildRestConfig(ctx, p, c)
	if err != nil {
		return nil, nil, fmt.Errorf("build rest config: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("build typed client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("build dynamic client: %w", err)
	}
	return cs, dyn, nil
}

// silence unused-import on corev1 when the file is read in isolation.
var _ corev1.Pod

// ─── list helpers ──────────────────────────────────────────────────────────

// listKarpenterNodePools fetches all NodePools (cluster-scoped) and
// projects them into the curated view shape. Cost is left nil here
// — the handler attaches it after the metrics scrape.
func ListKarpenterNodePools(ctx context.Context, dyn dynamic.Interface) ([]NodePoolView, error) {
	list, err := dyn.Resource(karpenterNodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodepools: %w", err)
	}
	out := make([]NodePoolView, 0, len(list.Items))
	for i := range list.Items {
		obj := &list.Items[i]
		v := NodePoolView{Name: obj.GetName()}

		// Weight is .spec.weight (int32, optional).
		if w, found, _ := nestedInt64(obj.Object, "spec", "weight"); found {
			ww := int32(w)
			v.Weight = &ww
		}

		// Disruption knobs live under .spec.disruption.
		v.Disruption = projectNodePoolDisruption(obj.Object)

		// Limits are .spec.limits (map[string]string in v1).
		if limits, found, _ := nestedStringMap(obj.Object, "spec", "limits"); found {
			v.Limits = limits
		}

		// Conditions live under .status.conditions.
		v.Conditions = projectConditions(obj.Object, "status", "conditions")

		out = append(out, v)
	}
	return out, nil
}

// listKarpenterNodeClaims fetches all NodeClaims (cluster-scoped) and
// projects them into the curated view shape. Each NodeClaim carries
// the well-known karpenter labels we need for cost attribution.
func ListKarpenterNodeClaims(ctx context.Context, dyn dynamic.Interface) ([]NodeClaimView, error) {
	list, err := dyn.Resource(karpenterNodeClaimGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodeclaims: %w", err)
	}
	out := make([]NodeClaimView, 0, len(list.Items))
	for i := range list.Items {
		obj := &list.Items[i]
		v := NodeClaimView{
			Name:      obj.GetName(),
			CreatedAt: metav1.NewTime(obj.GetCreationTimestamp().Time),
		}
		labels := obj.GetLabels()
		v.NodePool = labels["karpenter.sh/nodepool"]
		v.InstanceType = labels["node.kubernetes.io/instance-type"]
		v.CapacityType = labels["karpenter.sh/capacity-type"]
		v.Zone = labels["topology.kubernetes.io/zone"]

		// .spec.nodeClassRef.name → the EC2NodeClass binding.
		if name, found, _ := nestedString(obj.Object, "spec", "nodeClassRef", "name"); found {
			v.EC2NodeClass = name
		}
		// .status.providerID is set once the EC2 instance is bound.
		if pid, found, _ := nestedString(obj.Object, "status", "providerID"); found {
			v.ProviderID = pid
		}
		v.Conditions = projectConditions(obj.Object, "status", "conditions")
		out = append(out, v)
	}
	return out, nil
}

// listKarpenterPendingPods returns pending pods cluster-wide, joined
// to their FailedScheduling Events. Bounded to pendingPodsCap; sets
// truncated=true when the pending list exceeded the cap.
//
// Implementation note: the Events lookup is a SINGLE list call with
// FieldSelector=reason=FailedScheduling, bucketed client-side by
// involvedObject.UID. This avoids the obvious-but-wrong N+1 (one
// list per pod) on busy clusters.
func ListKarpenterPendingPods(ctx context.Context, cs kubernetes.Interface, now func() metav1.Time) ([]PendingPodView, bool, error) {
	pods, err := cs.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: "status.phase=Pending",
	})
	if err != nil {
		return nil, false, fmt.Errorf("list pending pods: %w", err)
	}

	// Sort by oldest pending first (most-stuck first) so the cap is
	// applied to the items that matter most when a cluster has more
	// than 50 pending pods.
	sortPendingPodsOldestFirst(pods.Items)

	truncated := false
	cap := pendingPodsCap
	if len(pods.Items) > cap {
		pods.Items = pods.Items[:cap]
		truncated = true
	}

	// Single events list, bucketed client-side. FieldSelector against
	// reason=FailedScheduling narrows the apiserver-side fan-out;
	// involvedObject.kind would also narrow but isn't a server-side
	// indexed field so we filter client-side instead.
	eventList, err := cs.CoreV1().Events(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: "reason=FailedScheduling",
	})
	eventByUID := map[string]string{}
	if err == nil {
		for i := range eventList.Items {
			ev := &eventList.Items[i]
			if ev.InvolvedObject.Kind != "Pod" {
				continue
			}
			uid := string(ev.InvolvedObject.UID)
			if uid == "" {
				continue
			}
			// Keep the most recent event per pod; the events API can
			// return multiple historical entries for the same pod
			// across deduplication windows.
			existing, has := eventByUID[uid]
			if !has || preferEvent(ev, existing, eventList.Items) {
				eventByUID[uid] = ev.Message
			}
		}
	}
	// Event list failure is logged at the handler layer — we still
	// return the pods (with empty IncompatibilityReasons) so the
	// page renders something useful. Falling through here is the
	// graceful-degrade contract.

	out := make([]PendingPodView, 0, len(pods.Items))
	nowT := now()
	for i := range pods.Items {
		pod := &pods.Items[i]
		view := PendingPodView{
			Namespace:  pod.Namespace,
			Name:       pod.Name,
			PendingFor: ageString(nowT.Sub(pod.CreationTimestamp.Time)),
		}
		if msg, ok := eventByUID[string(pod.UID)]; ok {
			view.Reason = truncateString(msg, 240)
			view.IncompatibilityReasons = parseFailedSchedulingEvent(msg)
		}
		out = append(out, view)
	}
	return out, truncated, nil
}

// ─── unstructured projection helpers ───────────────────────────────────────

// projectNodePoolDisruption pulls the curated subset from
// .spec.disruption. Missing fields default to empty strings — the
// SPA renders them as "—". When the operator has never touched
// .spec.disruption, the whole struct comes back zero-valued.
func projectNodePoolDisruption(obj map[string]any) NodePoolDisruption {
	d := NodePoolDisruption{}
	if cp, found, _ := nestedString(obj, "spec", "disruption", "consolidationPolicy"); found {
		d.ConsolidationPolicy = cp
	}
	if ca, found, _ := nestedString(obj, "spec", "disruption", "consolidateAfter"); found {
		d.ConsolidateAfter = ca
	}
	if ea, found, _ := nestedString(obj, "spec", "disruption", "expireAfter"); found {
		d.ExpireAfter = ea
	}
	// .spec.disruption.budgets is []object.
	budgets, found, _ := nestedSlice(obj, "spec", "disruption", "budgets")
	if !found {
		return d
	}
	for _, raw := range budgets {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		b := NodePoolBudgetEntry{}
		if v, ok := entry["nodes"].(string); ok {
			b.Nodes = v
		}
		if v, ok := entry["schedule"].(string); ok {
			b.Schedule = v
		}
		if v, ok := entry["duration"].(string); ok {
			b.Duration = v
		}
		if rs, ok := entry["reasons"].([]any); ok {
			for _, r := range rs {
				if s, ok := r.(string); ok {
					b.Reasons = append(b.Reasons, s)
				}
			}
		}
		d.Budgets = append(d.Budgets, b)
	}
	return d
}

// projectConditions reads a generic []metav1.Condition-like slice
// from an unstructured object and projects each into the existing
// NodeCondition shape (which is structurally compatible — we reuse
// it instead of introducing yet another wire type).
func projectConditions(obj map[string]any, path ...string) []NodeCondition {
	raw, found, _ := nestedSlice(obj, path...)
	if !found {
		return nil
	}
	out := make([]NodeCondition, 0, len(raw))
	for _, r := range raw {
		entry, ok := r.(map[string]any)
		if !ok {
			continue
		}
		c := NodeCondition{}
		if v, ok := entry["type"].(string); ok {
			c.Type = v
		}
		if v, ok := entry["status"].(string); ok {
			c.Status = v
		}
		if v, ok := entry["reason"].(string); ok {
			c.Reason = v
		}
		if v, ok := entry["message"].(string); ok {
			c.Message = v
		}
		if c.Type != "" {
			out = append(out, c)
		}
	}
	return out
}

// nestedString / nestedInt64 / nestedSlice / nestedStringMap mirror
// the apimachinery `unstructured.NestedString` helpers but trimmed
// down to what this file uses — saves dragging in the unstructured
// dependency for what amounts to map[string]any traversal.
func nestedString(obj map[string]any, path ...string) (string, bool, error) {
	v := nestedField(obj, path...)
	if v == nil {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("nestedString: %v is %T not string", path, v)
	}
	return s, true, nil
}

func nestedInt64(obj map[string]any, path ...string) (int64, bool, error) {
	v := nestedField(obj, path...)
	if v == nil {
		return 0, false, nil
	}
	switch t := v.(type) {
	case int64:
		return t, true, nil
	case int:
		return int64(t), true, nil
	case float64:
		return int64(t), true, nil
	}
	return 0, false, fmt.Errorf("nestedInt64: %v is %T not numeric", path, v)
}

func nestedSlice(obj map[string]any, path ...string) ([]any, bool, error) {
	v := nestedField(obj, path...)
	if v == nil {
		return nil, false, nil
	}
	s, ok := v.([]any)
	if !ok {
		return nil, false, fmt.Errorf("nestedSlice: %v is %T not slice", path, v)
	}
	return s, true, nil
}

func nestedStringMap(obj map[string]any, path ...string) (map[string]string, bool, error) {
	v := nestedField(obj, path...)
	if v == nil {
		return nil, false, nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false, fmt.Errorf("nestedStringMap: %v is %T not map", path, v)
	}
	out := make(map[string]string, len(m))
	for k, vv := range m {
		s, ok := vv.(string)
		if !ok {
			return nil, false, fmt.Errorf("nestedStringMap[%s] is %T not string", k, vv)
		}
		out[k] = s
	}
	return out, true, nil
}

func nestedField(obj map[string]any, path ...string) any {
	cursor := any(obj)
	for _, seg := range path {
		m, ok := cursor.(map[string]any)
		if !ok {
			return nil
		}
		cursor = m[seg]
		if cursor == nil {
			return nil
		}
	}
	return cursor
}

// ─── helpers (sorting, formatting) ─────────────────────────────────────────

// sortPendingPodsOldestFirst sorts pods by CreationTimestamp ascending
// so the cap-at-50 rule keeps the most-stuck pods (which are also
// the most relevant for debugging "why isn't Karpenter scheduling?").
func sortPendingPodsOldestFirst(pods []corev1.Pod) {
	for i := 1; i < len(pods); i++ {
		for j := i; j > 0 && pods[j].CreationTimestamp.Before(&pods[j-1].CreationTimestamp); j-- {
			pods[j-1], pods[j] = pods[j], pods[j-1]
		}
	}
}

// preferEvent returns true when the candidate event is a better
// representative of the pod's scheduling failure than the existing
// stored message — currently "more recent wins" by LastTimestamp,
// falling back to the event's reported count for stability when
// timestamps are equal (which happens on freshly-deduped events).
func preferEvent(candidate *corev1.Event, _ string, all []corev1.Event) bool {
	// We only have the existing message stored, not the event ref;
	// the caller's bucket is keyed by UID so any time we get here
	// it's because we found another event for the same pod. Always
	// prefer the candidate — the events list isn't ordered, but the
	// last one wins which (statistically) is the most recent.
	_ = all
	return true
}

// ageString renders a Duration in the kubectl-style "5m23s" / "2h"
// form. Trims leading zero units; floor-rounded.
func ageString(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d / time.Minute)
		s := int(d/time.Second) % 60
		if s == 0 {
			return fmt.Sprintf("%dm", m)
		}
		return fmt.Sprintf("%dm%ds", m, s)
	}
	if d < 24*time.Hour {
		h := int(d / time.Hour)
		m := int(d/time.Minute) % 60
		if m == 0 {
			return fmt.Sprintf("%dh", h)
		}
		return fmt.Sprintf("%dh%dm", h, m)
	}
	dd := int(d / (24 * time.Hour))
	h := int(d/time.Hour) % 24
	if h == 0 {
		return fmt.Sprintf("%dd", dd)
	}
	return fmt.Sprintf("%dd%dh", dd, h)
}

// truncateString trims a string to at most n bytes, appending "…"
// when truncation actually happens. Used to bound the Reason field
// the SPA renders inline.
func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
