// Workload rollback HTTP surface (issue #71).
//
// Two endpoints, kept in this dedicated file so main.go doesn't grow
// further. Both ride on credentials.Wrap so the user's impersonation
// chain reaches the apiserver as the human, and audit emission
// follows the same shape used by apply / delete / trigger handlers.
//
//	GET  /api/clusters/{cluster}/{kind}/{ns}/{name}/revisions
//	POST /api/clusters/{cluster}/{kind}/{ns}/{name}/rollback
//
// kind is one of "deployments" | "statefulsets" | "daemonsets". The
// router gates on this set; unknown kinds get 404 from the router
// itself (handlers don't need to validate again).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// listRevisionsHandler is GET /revisions — returns the rollout history
// + pre-flight metadata. No audit emission: this is a read.
func listRevisionsHandler(reg *clusters.Registry) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		kind := chi.URLParam(r, "kind")
		if !k8s.IsRollbackableKind(kind) {
			http.Error(w, "kind does not support rollback", http.StatusBadRequest)
			return
		}
		args := k8s.ListRevisionsArgs{
			Cluster:   c,
			Kind:      kind,
			Namespace: chi.URLParam(r, "ns"),
			Name:      chi.URLParam(r, "name"),
		}
		hist, err := k8s.ListRevisions(r.Context(), p, args)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			writeAPIError(w, err, classifyRollbackErr(err))
			return
		}
		writeJSON(w, http.StatusOK, hist)
	}
}

// rollbackHandler is POST /rollback. Audit shape:
//
//	1. VerbRollbackIntent emitted BEFORE the apiserver patch fires —
//	   captures the operator's intent even when the patch later fails
//	   or the request hangs. Carries the target revision + reason.
//	2. VerbRollback emitted AFTER the patch with the outcome (success
//	   carries the new revision number; failure carries the error).
//
// The two-row pattern is what makes incident review possible: an
// operator who clicked "rollback to revision 3" but never got a
// response still leaves a forensic trail.
func rollbackHandler(reg *clusters.Registry, auditer *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		kind := chi.URLParam(r, "kind")
		if !k8s.IsRollbackableKind(kind) {
			http.Error(w, "kind does not support rollback", http.StatusBadRequest)
			return
		}
		ns := chi.URLParam(r, "ns")
		name := chi.URLParam(r, "name")

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4*1024))
		if err != nil {
			http.Error(w, "request body too large", http.StatusBadRequest)
			return
		}
		var req struct {
			Revision int64  `json:"revision"`
			Reason   string `json:"reason"`
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Revision <= 0 {
			http.Error(w, "expected JSON body { revision: <positive int>, reason?: string }", http.StatusBadRequest)
			return
		}

		actor := actorFromContext(r.Context())
		args := k8s.RollbackArgs{
			Cluster:      c,
			Kind:         kind,
			Namespace:    ns,
			Name:         name,
			ToRevision:   req.Revision,
			Reason:       req.Reason,
			ActorSubject: actor.Sub,
		}

		// Intent row — emit before the patch. Note the routes use
		// the workload kind in URL param form ("deployments"), which
		// matches the apiserver's resource plural — fine for the
		// audit Resource field too.
		intent := audit.Event{
			Actor:    actor,
			Verb:     audit.VerbRollbackIntent,
			Outcome:  audit.OutcomeSuccess,
			Cluster:  c.Name,
			Resource: audit.ResourceRef{Group: "apps", Version: "v1", Resource: kind, Namespace: ns, Name: name},
			Extra: map[string]any{
				"toRevision": req.Revision,
				"reason":     req.Reason,
			},
		}
		auditer.Record(r.Context(), intent)

		result, err := k8s.Rollback(r.Context(), p, args)

		evt := audit.Event{
			Actor:    actor,
			Verb:     audit.VerbRollback,
			Cluster:  c.Name,
			Resource: audit.ResourceRef{Group: "apps", Version: "v1", Resource: kind, Namespace: ns, Name: name},
			Extra: map[string]any{
				"toRevision": req.Revision,
				"reason":     req.Reason,
			},
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			evt.Outcome = outcomeFor(err)
			evt.Reason = err.Error()
			auditer.Record(r.Context(), evt)
			writeAPIError(w, err, classifyRollbackErr(err))
			return
		}

		evt.Outcome = audit.OutcomeSuccess
		evt.Extra["newRevision"] = result.NewRevision
		auditer.Record(r.Context(), evt)
		writeJSON(w, http.StatusOK, result)
	}
}

// classifyRollbackErr maps the rollback package's sentinels to HTTP
// status codes. Falls through to httpStatusFor for client-go errors.
func classifyRollbackErr(err error) int {
	switch {
	case errors.Is(err, k8s.ErrUnsupportedKind):
		return http.StatusBadRequest
	case errors.Is(err, k8s.ErrRevisionNotFound):
		return http.StatusNotFound
	case errors.Is(err, k8s.ErrAlreadyAtRevision):
		return http.StatusConflict
	case errors.Is(err, k8s.ErrDeploymentPaused):
		return http.StatusConflict
	case errors.Is(err, k8s.ErrNoRevisionHistory):
		return http.StatusUnprocessableEntity
	}
	return httpStatusFor(err)
}
