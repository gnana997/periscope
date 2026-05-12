package k8s

// stuck.go — pure-function detector for "this rollout is wedged."
//
// Why backend-side: the per-row stuck signal is computed in Go so a
// future MCP / AI-agent tool layer ("which workloads are stuck right
// now?") and the SPA's badge / banner share one source of truth.
// Matches the pure-logic-in-backend rule applied for CVE grouping.
//
// Inputs are immutable apps/v1 objects + a caller-supplied `now`.
// Callers pass time.Now(); tests pin a fixed time. Goroutine-safe
// because nothing here mutates inputs.

import (
	"time"

	appsv1 "k8s.io/api/apps/v1"
)

// StuckReason names the reason a rollout is flagged. Strings are part
// of the public JSON shape; do not rename without a SPA-side
// counterpart change (web/src/lib/types.ts).
type StuckReason string

const (
	// StuckReasonProgressDeadlineExceeded — the Deployment controller
	// has tripped its Progressing condition with reason
	// "ProgressDeadlineExceeded". Authoritative signal: the controller
	// itself has stopped retrying.
	StuckReasonProgressDeadlineExceeded StuckReason = "progress-deadline-exceeded"

	// StuckReasonStalled — heuristic signal: UpdatedReplicas <
	// effective replicas (or, for DaemonSets, UpdatedNumberScheduled <
	// DesiredNumberScheduled) AND no condition has transitioned for
	// at least StuckThreshold. Used when the kind has no
	// "progress deadline" of its own (StatefulSet, DaemonSet) and as
	// a fallback for Deployments that haven't tripped the deadline.
	StuckReasonStalled StuckReason = "stalled"
)

// StuckState is the per-row stuck-rollout payload. Pointer-valued on
// the DTOs so a healthy workload omits the field entirely.
type StuckState struct {
	Reason  StuckReason `json:"reason"`
	SinceMs int64       `json:"sinceMs"`
}

// StuckThreshold — how long a stalled rollout must have sat without a
// condition transition before earning the `stalled` badge. Mirrored
// in the SPA's tooltip wording; keep them in sync.
const StuckThreshold = 10 * time.Minute

// DetectDeploymentStuck applies, in order:
//  1. Progressing condition with reason "ProgressDeadlineExceeded" →
//     ProgressDeadlineExceeded, since = condition.LastTransitionTime.
//  2. UpdatedReplicas < effective replicas AND latest condition
//     transition older than StuckThreshold → Stalled.
//  3. Otherwise nil (healthy).
func DetectDeploymentStuck(d *appsv1.Deployment, now time.Time) *StuckState {
	if d == nil {
		return nil
	}
	replicas := effectiveReplicas(d.Spec.Replicas, d.Status.Replicas)
	if replicas == 0 {
		return nil
	}

	// (1) authoritative deadline-exceeded path
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Reason == "ProgressDeadlineExceeded" {
			return &StuckState{
				Reason:  StuckReasonProgressDeadlineExceeded,
				SinceMs: sinceMs(c.LastTransitionTime.Time, now),
			}
		}
	}

	// (2) stalled heuristic — only when there's actually unfinished work
	if d.Status.UpdatedReplicas >= replicas {
		return nil
	}
	since := latestDeploymentConditionTime(d.Status.Conditions)
	if since.IsZero() {
		since = d.CreationTimestamp.Time
	}
	if since.IsZero() {
		return nil
	}
	if now.Sub(since) < StuckThreshold {
		return nil
	}
	return &StuckState{
		Reason:  StuckReasonStalled,
		SinceMs: sinceMs(since, now),
	}
}

// DetectStatefulSetStuck — STS has no Progressing condition; only the
// stalled path applies. Same shape as Deployment otherwise.
func DetectStatefulSetStuck(s *appsv1.StatefulSet, now time.Time) *StuckState {
	if s == nil {
		return nil
	}
	replicas := effectiveReplicas(s.Spec.Replicas, s.Status.Replicas)
	if replicas == 0 {
		return nil
	}
	if s.Status.UpdatedReplicas >= replicas {
		return nil
	}
	since := latestStatefulSetConditionTime(s.Status.Conditions)
	if since.IsZero() {
		since = s.CreationTimestamp.Time
	}
	if since.IsZero() {
		return nil
	}
	if now.Sub(since) < StuckThreshold {
		return nil
	}
	return &StuckState{
		Reason:  StuckReasonStalled,
		SinceMs: sinceMs(since, now),
	}
}

// DetectDaemonSetStuck — flagged stalled when UpdatedNumberScheduled <
// DesiredNumberScheduled for longer than StuckThreshold.
func DetectDaemonSetStuck(d *appsv1.DaemonSet, now time.Time) *StuckState {
	if d == nil {
		return nil
	}
	desired := d.Status.DesiredNumberScheduled
	if desired == 0 {
		return nil
	}
	if d.Status.UpdatedNumberScheduled >= desired {
		return nil
	}
	since := latestDaemonSetConditionTime(d.Status.Conditions)
	if since.IsZero() {
		since = d.CreationTimestamp.Time
	}
	if since.IsZero() {
		return nil
	}
	if now.Sub(since) < StuckThreshold {
		return nil
	}
	return &StuckState{
		Reason:  StuckReasonStalled,
		SinceMs: sinceMs(since, now),
	}
}

// effectiveReplicas resolves spec.Replicas vs status.Replicas. When
// spec is unset (nil), the controller's status replica count is the
// best signal of what the desired count is. Mirrors the existing
// summary builders' replica-resolution convention.
func effectiveReplicas(spec *int32, status int32) int32 {
	if spec != nil {
		return *spec
	}
	return status
}

// sinceMs returns the number of milliseconds between `since` and
// `now`, clamped to zero. Clock skew between the apiserver and us
// must not produce negative values.
func sinceMs(since, now time.Time) int64 {
	d := now.Sub(since)
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}

// latestDeploymentConditionTime returns the most recent
// LastTransitionTime across the supplied conditions, or zero time if
// the slice is empty.
func latestDeploymentConditionTime(conds []appsv1.DeploymentCondition) time.Time {
	var latest time.Time
	for _, c := range conds {
		if c.LastTransitionTime.Time.After(latest) {
			latest = c.LastTransitionTime.Time
		}
	}
	return latest
}

// latestStatefulSetConditionTime — same shape, STS condition type.
func latestStatefulSetConditionTime(conds []appsv1.StatefulSetCondition) time.Time {
	var latest time.Time
	for _, c := range conds {
		if c.LastTransitionTime.Time.After(latest) {
			latest = c.LastTransitionTime.Time
		}
	}
	return latest
}

// latestDaemonSetConditionTime — same shape, DS condition type.
func latestDaemonSetConditionTime(conds []appsv1.DaemonSetCondition) time.Time {
	var latest time.Time
	for _, c := range conds {
		if c.LastTransitionTime.Time.After(latest) {
			latest = c.LastTransitionTime.Time
		}
	}
	return latest
}
