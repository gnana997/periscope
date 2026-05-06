package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/aws/smithy-go"
	kerrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/credentials"
)

// httpStatusFor maps a k8s client-go error to the appropriate HTTP
// status to surface back to the SPA. Forbidden errors propagate as
// 403 so the SPA's isForbidden() check can render the calm
// ForbiddenState empty state instead of a generic red error banner.
//
// Anything not classified is 500.
func httpStatusFor(err error) int {
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
// 409 conflict resolution). Falls back to plain text for non-Status
// errors so existing clients stay compatible.
func writeAPIError(w http.ResponseWriter, err error, status int) {
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

func isContextTimeout(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if e.Error() == "context deadline exceeded" {
			return true
		}
	}
	return false
}
