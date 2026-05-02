package main

import (
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
