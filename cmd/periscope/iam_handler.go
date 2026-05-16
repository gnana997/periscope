package main

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/awseks/iam"
	"github.com/gnana997/periscope/internal/awseks/identity"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// emitIAMRead writes one audit row per IAM-engine / AWS-Access
// surface call. Mirror of emitIdentityRead but emits under
// VerbAwsIAMRead so operator audit-feed filters can split "who
// read identity surface" (#178) from "who read IAM policies"
// (#187, #188) cleanly.
func emitIAMRead(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, outcome audit.Outcome, op, reason string) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbAwsIAMRead,
		Outcome: outcome,
		Cluster: c.Name,
		Reason:  reason,
		Extra: map[string]any{
			"op": op,
		},
	})
}

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
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeFailure, "role_permissions_setup", err.Error())
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
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeFailure, "role_permissions", err.Error())
			if !result.PolicyFetchPartial {
				// Total failure — no rows at all. Map AWS error.
				status, code := awsErrorToStatus(err)
				writeAPIErrorJSON(w, status, code,
					"failed to fetch role permissions: "+err.Error())
				return
			}
			// Partial — fall through to return what we have.
		} else {
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeSuccess, "role_permissions", "")
		}

		writeJSON(w, http.StatusOK, result)
	}
}

// ── /iam/reverse-lookup ──────────────────────────────────────────
//
// GET /api/clusters/{cluster}/iam/reverse-lookup?action=...&resource=...&namespace=...
//
// Reverse lookup: returns ReverseLookupResponse — one row per
// matched pod (#188 wire-shape change from v1.0; previous shape
// was one row per SA with embedded podRefs).
//
// Pipeline:
//   1. Engine returns []ReverseLookupMatch (one per (SA, role,
//      permission) tuple).
//   2. Handler memoizes identity.Manager.PodsForSA per (ns, sa) so
//      a SA bound to many matches resolves pods once.
//   3. Each match flattens to one row per pod, with binding source
//      (IRSA / PodIdentity / Both) attributed from the SA's index
//      entry. Dual-source SAs emit one row per binding per pod so
//      the SPA renders the honest dual-source story.
//   4. SortReverseLookupRows imposes sensitive-first ordering.
//   5. Truncated at cfg.MaxRowsCap so a SA bound to thousands of
//      pods doesn't blow the response.

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
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeFailure, "reverse_lookup_setup", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_IAM_SETUP",
				"failed to set up IAM engine: "+err.Error())
			return
		}

		mgr, err := cache.identityC.For(r.Context(), c)
		if err != nil {
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeFailure, "reverse_lookup_setup", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_IDENTITY_SETUP",
				"failed to set up identity manager: "+err.Error())
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
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeFailure, "reverse_lookup", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to run reverse lookup: "+err.Error())
			return
		}

		rows, totalPods, truncated := flattenReverseLookupRows(r.Context(), mgr, matches, engineConfig(cache).PodRefsLimit, engineConfig(cache).MaxRowsCap)
		emitIAMRead(r.Context(), emitter, c, audit.OutcomeSuccess, "reverse_lookup", "")

		if rows == nil {
			rows = []iam.ReverseLookupPodRow{}
		}

		writeJSON(w, http.StatusOK, iam.ReverseLookupResponse{
			Action:   query.Action,
			Resource: query.Resource,
			Scope: iam.ReverseLookupScope{
				Cluster:   c.Name,
				Namespace: query.Namespace,
			},
			Rows:      rows,
			Truncated: truncated,
			TotalPods: totalPods,
		})
	}
}

// engineConfig returns the active iam.Config for the cache. Wraps
// the unexported field so the handler can read PodRefsLimit /
// MaxRowsCap without exporting the field.
func engineConfig(c *iamEngineCache) iam.Config {
	return c.cfg
}

// flattenReverseLookupRows joins matches against the cluster's pod
// informer cache and produces one row per matched pod. Binding
// source attribution is looked up per (ns, sa) from the identity
// manager's current SA→Role index — a SA with both IRSA and Pod
// Identity bindings emits one row per pod per binding.
//
// Memoizes PodsForSA per (ns, sa) so two roles bound to the same
// SA only walk the indexer once. Total pod count is the
// untruncated sum across all matches (after dedup of (pod,
// permission, role, source) tuples) so the SPA can render
// "showing N of M".
func flattenReverseLookupRows(
	ctx context.Context,
	mgr *identity.Manager,
	matches []iam.ReverseLookupMatch,
	podLimit int,
	rowCap int,
) (rows []iam.ReverseLookupPodRow, totalPods int, truncated bool) {
	type podBucket struct {
		refs  []identity.PodRef
		total int
	}
	memoPods := map[string]podBucket{}
	memoBindings := map[string][]identity.SARoleBinding{}

	bindingsForSA := func(ns, sa string) []identity.SARoleBinding {
		key := ns + "/" + sa
		if v, ok := memoBindings[key]; ok {
			return v
		}
		entries, err := mgr.Ensure(ctx)
		if err != nil {
			// Soft-fail: leave Source empty on rows for this SA
			// rather than dropping matches entirely.
			memoBindings[key] = nil
			return nil
		}
		var bindings []identity.SARoleBinding
		for _, e := range entries {
			if e.Namespace == ns && e.SAName == sa {
				bindings = e.Bindings
				break
			}
		}
		memoBindings[key] = bindings
		return bindings
	}

	for _, m := range matches {
		key := m.Namespace + "/" + m.SAName
		bucket, ok := memoPods[key]
		if !ok {
			refs, total, err := mgr.PodsForSA(ctx, m.Namespace, m.SAName, podLimit)
			if err != nil {
				// Pod informer not ready or transient lookup
				// failure — skip this SA's rows rather than 500.
				memoPods[key] = podBucket{}
				continue
			}
			bucket = podBucket{refs: refs, total: total}
			memoPods[key] = bucket
		}
		if len(bucket.refs) == 0 {
			continue
		}

		source := sourceForRole(bindingsForSA(m.Namespace, m.SAName), m.RoleArn)
		for _, ref := range bucket.refs {
			rows = append(rows, iam.ReverseLookupPodRow{
				Pod: iam.PodRef{
					Namespace: ref.Namespace,
					Name:      ref.Name,
					NodeName:  ref.NodeName,
				},
				SAName:     m.SAName,
				Namespace:  m.Namespace,
				RoleArn:    m.RoleArn,
				Permission: m.Permission,
				Source:     source,
			})
		}
		totalPods += bucket.total
	}

	iam.SortReverseLookupRows(rows)
	if rowCap > 0 && len(rows) > rowCap {
		rows = rows[:rowCap]
		truncated = true
	}
	return rows, totalPods, truncated
}

// sourceForRole maps a matched roleArn to the binding source on
// the SA's index entry. Empty string when no binding matches —
// happens if the SA→Role index moved between the engine's
// snapshot and the handler's lookup; SPA renders without a source
// chip rather than failing the row.
func sourceForRole(bindings []identity.SARoleBinding, roleArn string) string {
	for _, b := range bindings {
		if b.RoleArn == roleArn {
			return string(b.Source)
		}
	}
	return ""
}
