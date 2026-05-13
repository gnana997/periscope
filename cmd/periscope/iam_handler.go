package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/awseks/iam"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// ── /iam/role-permissions ────────────────────────────────────────
//
// GET /api/clusters/{cluster}/iam/role-permissions?roleArn=arn:...
//
// Forward view: returns RolePermissionsResult — every Permission
// row the SPA renders in the per-Pod / per-SA AWS Access tab,
// plus any RawStatement entries for NotAction/NotResource cases.
//
// Auth: same impersonation + EKS-capable gate as the other identity
// endpoints. AWS calls go through the periscope-server's shared
// identity (per ssrf-safe rationale in identity_handler.go).

func iamRolePermissionsHandler(reg *clusters.Registry, cache *iamEngineCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity, errBackendNotEKSCode,
				"IAM policy resolution is only available for EKS-backed clusters")
			return
		}

		roleArn := strings.TrimSpace(r.URL.Query().Get("roleArn"))
		if roleArn == "" {
			writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST",
				"missing required query param: roleArn")
			return
		}

		engine, err := cache.For(r.Context(), c)
		if err != nil {
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "role_permissions_setup", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_IAM_SETUP",
				"failed to set up IAM engine: "+err.Error())
			return
		}

		result, err := engine.RolePermissions(r.Context(), roleArn)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			// Soft-fail: result may still carry partial data
			// (PolicyFetchPartial=true). Emit audit + log, then
			// return the partial result so the SPA renders a banner
			// instead of blanking.
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "role_permissions", err.Error())
			if !result.PolicyFetchPartial {
				// Total failure — no rows at all. Map AWS error.
				status, code := awsErrorToStatus(err)
				writeAPIErrorJSON(w, status, code,
					"failed to fetch role permissions: "+err.Error())
				return
			}
			// Partial — fall through to return what we have.
		} else {
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "role_permissions", "")
		}

		writeJSON(w, http.StatusOK, result)
	}
}

// ── /iam/reverse-lookup ──────────────────────────────────────────
//
// GET /api/clusters/{cluster}/iam/reverse-lookup?action=...&resource=...&namespace=...
//
// Reverse lookup: returns ReverseLookupResponse — every (SA, role,
// permission) tuple in the cluster that matches the action +
// optional resource. Optional namespace scopes the iteration.
//
// PodRefs / PodCount stay empty in v1.1; the SPA renders SA +
// namespace + role without per-pod expansion. Pod enumeration is
// a v1.1.x polish PR — see #187 plan §10 follow-ups.

func iamReverseLookupHandler(reg *clusters.Registry, cache *iamEngineCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity, errBackendNotEKSCode,
				"IAM reverse lookup is only available for EKS-backed clusters")
			return
		}

		query := iam.ReverseLookupQuery{
			Action:    strings.TrimSpace(r.URL.Query().Get("action")),
			Resource:  strings.TrimSpace(r.URL.Query().Get("resource")),
			Namespace: strings.TrimSpace(r.URL.Query().Get("namespace")),
		}
		if query.Action == "" {
			writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST",
				"missing required query param: action")
			return
		}

		engine, err := cache.For(r.Context(), c)
		if err != nil {
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "reverse_lookup_setup", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_IAM_SETUP",
				"failed to set up IAM engine: "+err.Error())
			return
		}

		matches, err := engine.ReverseLookup(r.Context(), query)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			if errors.Is(err, iam.ErrInvalidQuery) {
				writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST", err.Error())
				return
			}
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "reverse_lookup", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to run reverse lookup: "+err.Error())
			return
		}
		emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "reverse_lookup", "")

		// matches may be nil for "no results"; normalize to empty slice
		// so the SPA always sees a JSON array.
		if matches == nil {
			matches = []iam.ReverseLookupMatch{}
		}

		writeJSON(w, http.StatusOK, iam.ReverseLookupResponse{
			Action:   query.Action,
			Resource: query.Resource,
			Scope: iam.ReverseLookupScope{
				Cluster:   c.Name,
				Namespace: query.Namespace,
			},
			Matches: matches,
		})
	}
}
