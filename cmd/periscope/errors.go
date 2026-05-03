package main

import (
	"encoding/json"
	"errors"
	"net/http"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
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

func isContextTimeout(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if e.Error() == "context deadline exceeded" {
			return true
		}
	}
	return false
}
