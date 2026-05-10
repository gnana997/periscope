package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/aws/smithy-go"
	"helm.sh/helm/v3/pkg/storage/driver"
	kerrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// httpStatusFor maps a k8s client-go error to the appropriate HTTP
// status to surface back to the SPA. Forbidden errors propagate as
// 403 so the SPA's isForbidden() check can render the calm
// ForbiddenState empty state instead of a generic red error banner.
//
// Anything not classified is 500.
func httpStatusFor(err error) int {
	// Agent-upstream errors carry their own status (502 / 504 from
	// the agent ErrorHandler). Pivot on the typed value instead of
	// remapping so the kubectl-style 502/504 distinction the agent
	// already made survives the trip to the SPA.
	if aue, ok := k8s.AsAgentUpstreamError(err); ok && aue.HTTPStatus != 0 {
		return aue.HTTPStatus
	}
	switch {
	case kerrors.IsForbidden(err):
		return http.StatusForbidden
	case kerrors.IsUnauthorized(err):
		return http.StatusUnauthorized
	case kerrors.IsNotFound(err):
		return http.StatusNotFound
	case kerrors.IsConflict(err):
		return http.StatusConflict
	case kerrors.IsTimeout(err):
		return http.StatusGatewayTimeout
	case kerrors.IsServerTimeout(err):
		return http.StatusGatewayTimeout
	case kerrors.IsTooManyRequests(err):
		return http.StatusTooManyRequests
	case kerrors.IsBadRequest(err):
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// outcomeFor maps a Kubernetes client-go error to an audit Outcome.
// Forbidden / Unauthorized are forensically interesting denials and
// get their own outcome class so an operator can query "denied"
// rows separately from generic failures (validation errors, network
// timeouts, conflicts).
func outcomeFor(err error) audit.Outcome {
	switch {
	case kerrors.IsForbidden(err), kerrors.IsUnauthorized(err):
		return audit.OutcomeDenied
	default:
		return audit.OutcomeFailure
	}
}

// actorFromContext returns an audit.Actor sourced from the Session
// on context — Subject, Email, Groups all in one shot. Returns the
// "anonymous" zero shape if no session was planted (which is what
// credentials.SessionFromContext already guarantees).
func actorFromContext(ctx context.Context) audit.Actor {
	s := credentials.SessionFromContext(ctx)
	return audit.Actor{Sub: s.Subject, Email: s.Email, Groups: s.Groups}
}

// writeAPIError surfaces a kerrors.StatusError as the structured
// metav1.Status JSON the SPA needs (details.causes[] for field-level
// 409 conflict resolution). For agent-upstream transport failures it
// emits a structured JSON envelope carrying code / category / cluster
// / trace_id so the SPA can render a friendly banner and an operator
// can pivot to logs by trace id. Falls back to plain text for any
// other error so existing clients stay compatible.
func writeAPIError(w http.ResponseWriter, err error, status int) {
	// Agent-upstream comes first: a *AgentUpstreamError can wrap a
	// kerrors.StatusError in degenerate cases (the apiserver raised
	// 503 mid-stream and the agent re-classified it), and we want the
	// agent-upstream envelope to win because it carries the trace id
	// and category the SPA needs.
	if aue, ok := k8s.AsAgentUpstreamError(err); ok {
		writeAgentUpstreamError(w, aue, status)
		return
	}
	var se *kerrors.StatusError
	if errors.As(err, &se) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(&se.ErrStatus)
		return
	}
	http.Error(w, err.Error(), status)
}

// writeAgentUpstreamError emits the structured JSON envelope for a
// *AgentUpstreamError. Field names match the wire shape so a SPA
// fetch handler can shape-check `code === "E_AGENT_UPSTREAM"` and
// surface category / cluster / trace_id without re-parsing the
// Error() string. trace_id is the same id the agent stamped into
// its slog access-log line, so operators can grep across central
// server stdout, agent stdout, and audit DB to follow a single
// failure end-to-end.
func writeAgentUpstreamError(w http.ResponseWriter, aue *k8s.AgentUpstreamError, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code     string `json:"code"`
		Message  string `json:"message"`
		Category string `json:"category,omitempty"`
		Cluster  string `json:"cluster,omitempty"`
		TraceID  string `json:"trace_id,omitempty"`
		Detail   string `json:"detail,omitempty"`
	}{
		Code:     aue.Code,
		Message:  aue.Message,
		Category: aue.Category,
		Cluster:  aue.Cluster,
		TraceID:  aue.TraceID,
		Detail:   aue.Detail,
	})
}

// ErrorCodeFor classifies a k8s/transport error into a stable string
// code for fleet-style multi-cluster responses where a single error
// per cluster needs to be surfaced to the UI without leaking raw
// k8s client-go strings. Wraps httpStatusFor so the classification
// stays single-source.
//
// Used by /api/fleet's per-cluster collector. The codes are part of
// the public API; treat them as additive (do not rename existing
// codes).
func ErrorCodeFor(err error) string {
	if err == nil {
		return ""
	}
	// Agent-upstream errors carry their own stable code so per-cluster
	// fleet collectors render "agent_upstream/<category>" instead of
	// the generic "apiserver_unreachable" bucket.
	if aue, ok := k8s.AsAgentUpstreamError(err); ok {
		if aue.Category != "" {
			return "agent_upstream/" + aue.Category
		}
		return "agent_upstream"
	}
	switch httpStatusFor(err) {
	case http.StatusForbidden:
		return "denied"
	case http.StatusUnauthorized:
		return "auth_failed"
	case http.StatusGatewayTimeout:
		return "timeout"
	case http.StatusInternalServerError:
		// Net errors / dial failures land here. Distinguish "couldn't
		// reach the apiserver at all" from generic unknown.
		if isContextTimeout(err) {
			return "timeout"
		}
		return "apiserver_unreachable"
	}
	return "unknown"
}

// awsErrorToStatus classifies an AWS SDK error into (httpStatus, code)
// for the EKS read-only handlers. Falls through to (502, "E_AWS_API")
// for unrecognized errors so the existing default behavior is
// preserved. Recognized smithy.APIError codes are shared across the
// EKS / SSM / EC2 services Periscope talks to today; the recognized
// set covers the failures an operator can actually act on (fix IAM,
// wait out a throttle, check that the resource exists). Anything else
// stays a generic 502 — the caller's slog.Warn line still records the
// raw error for debugging.
func awsErrorToStatus(err error) (int, string) {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "AccessDeniedException", "UnauthorizedOperation":
			return http.StatusForbidden, "E_AWS_FORBIDDEN"
		case "ResourceNotFoundException", "NotFoundException":
			return http.StatusNotFound, "E_AWS_NOT_FOUND"
		case "ThrottlingException", "TooManyRequestsException", "RequestLimitExceeded":
			return http.StatusTooManyRequests, "E_AWS_THROTTLED"
		}
	}
	return http.StatusBadGateway, "E_AWS_API"
}

// helmErrorToStatus classifies a helm SDK error into (httpStatus, code)
// for the helm preview / write handlers (issue #75 and the rebased
// rollback in #101). Mirrors awsErrorToStatus's shape: explicit map
// for recognized sentinels, fall through to (502, "E_HELM_SDK") for
// anything else.
//
// driver.ErrReleaseNotFound is the most common cause an operator can
// act on (release name typo, namespace mismatch); ErrNoDeployedReleases
// fires on upgrade preview when the release exists but has no deployed
// revision to compare against (release was just rolled back to nothing,
// or every revision is uninstalled).
//
// Render / capabilities errors from pkg/action don't have stable
// sentinel types — they're wrapped errors.New strings. Callers wrap
// the SDK error with their own annotation; we sniff for the substrings
// that are operator-actionable. Anything we can't classify falls
// through to 502, with the caller's slog.Warn still recording the
// raw error for debugging.
func helmErrorToStatus(err error) (int, string) {
	if err == nil {
		return http.StatusInternalServerError, "E_HELM_SDK"
	}
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return http.StatusNotFound, "E_HELM_RELEASE_NOT_FOUND"
	}
	if errors.Is(err, driver.ErrNoDeployedReleases) {
		return http.StatusUnprocessableEntity, "E_HELM_NO_DEPLOYED_RELEASES"
	}
	// Some helm-internal errors wrap an apiserver kerrors.StatusError
	// (e.g. when discovery is permission-denied). Unwrap and delegate
	// so a 403 from the apiserver via helm reads as 403 to the SPA.
	var se *kerrors.StatusError
	if errors.As(err, &se) {
		return httpStatusFor(err), classifyHelmK8sError(err)
	}
	// Render / template / capability errors. No stable type — sniff
	// the wrapped message. The substrings below are taken from helm's
	// own pkg/engine and pkg/action source; they're stable across
	// minor versions but not part of helm's public API.
	msg := err.Error()
	if strings.Contains(msg, "render error") ||
		strings.Contains(msg, "execution error") ||
		strings.Contains(msg, "parse error") ||
		strings.Contains(msg, "template:") {
		return http.StatusUnprocessableEntity, "E_HELM_RENDER_FAILED"
	}
	return http.StatusBadGateway, "E_HELM_SDK"
}

// classifyHelmK8sError returns a stable error code for helm errors
// that wrap a kerrors.StatusError. Mirrors ErrorCodeFor's shape but
// scoped to helm-wrapped k8s errors so the helm and fleet codepaths
// don't collide on string codes.
func classifyHelmK8sError(err error) string {
	switch httpStatusFor(err) {
	case http.StatusForbidden:
		return "E_HELM_K8S_FORBIDDEN"
	case http.StatusUnauthorized:
		return "E_HELM_K8S_UNAUTHORIZED"
	case http.StatusNotFound:
		return "E_HELM_K8S_NOT_FOUND"
	case http.StatusConflict:
		return "E_HELM_K8S_CONFLICT"
	}
	return "E_HELM_K8S"
}

func isContextTimeout(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if e.Error() == "context deadline exceeded" {
			return true
		}
	}
	return false
}
