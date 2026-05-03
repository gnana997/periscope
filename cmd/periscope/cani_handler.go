package main

// cani_handler.go — POST /api/clusters/{cluster}/can-i
//
// Pre-flight RBAC check used by the SPA to grey out actions the user
// cannot perform, replacing the click → 200ms round-trip → 403 → red
// banner UX with a disabled-button-with-tooltip experience.
//
// Works identically across the three authz modes (shared / tier /
// raw): the impersonating clientset built by internal/k8s already
// applies the right Impersonate-User / Impersonate-Group headers, so
// the apiserver evaluates the SAR/SSRR under whatever identity the
// authz resolver decided this request runs as.
//
// Routing strategy (see issue #7):
//   - 3+ namespaced no-subresource checks in the same namespace →
//     one SelfSubjectRulesReview per namespace, evaluate locally.
//   - everything else (cluster-scoped, subresource, single-check
//     namespaces) → per-check SelfSubjectAccessReview.
//
// Read-only metadata; no audit emission (mirrors fleet_handler.go).

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	authv1 "k8s.io/api/authorization/v1"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// Test-swappable wrappers around the apiserver calls. Real builds use
// k8s.CheckSAR / k8s.ListSSRR; tests stub these to avoid wiring a fake
// clientset just to exercise the routing / cache / fail-closed logic.
var (
	caniCheckSARFn = func(ctx context.Context, p credentials.Provider, c clusters.Cluster, attr authv1.ResourceAttributes) (bool, string, error) {
		return k8s.CheckSAR(ctx, p, c, attr)
	}
	caniListSSRRFn = func(ctx context.Context, p credentials.Provider, c clusters.Cluster, namespace string) (*authv1.SubjectRulesReviewStatus, error) {
		return k8s.ListSSRR(ctx, p, c, namespace)
	}
)

// caniMaxChecks bounds the per-request fan-out. Picked to comfortably
// cover the largest action toolbar (~10) plus headroom; rejects
// pathological requests that would amplify a small SPA bug into an
// apiserver storm.
const caniMaxChecks = 64

// caniSSRRBatchThreshold is the per-namespace check count at which we
// switch from per-check SAR to one SSRR + local evaluation. SSRR has a
// larger fixed cost (the apiserver returns the entire rule set), so
// it only wins when several checks share a namespace.
const caniSSRRBatchThreshold = 3

// CanICheck is one (verb, resource, namespace[, subresource]) tuple
// the SPA wants gated. Field shape mirrors authv1.ResourceAttributes
// 1:1 — the SPA already speaks this vocabulary via useCanI's args.
type CanICheck struct {
	Verb        string `json:"verb"`
	Group       string `json:"group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Name        string `json:"name,omitempty"`
}

// CanIRequest is the POST body.
type CanIRequest struct {
	Checks []CanICheck `json:"checks"`
}

// CanIResult is one entry in the response, in the same index as the
// matching check in the request.
type CanIResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

// CanIResponse is the POST response body.
type CanIResponse struct {
	Results []CanIResult `json:"results"`
}

// caniHandler builds the http.Handler. cache is constructed by main()
// so its lifetime is scoped to the process.
func caniHandler(reg *clusters.Registry, cache *caniCache) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}

		var req CanIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(req.Checks) == 0 {
			writeJSON(w, http.StatusOK, CanIResponse{Results: []CanIResult{}})
			return
		}
		if len(req.Checks) > caniMaxChecks {
			http.Error(w, "too many checks (limit 64)", http.StatusBadRequest)
			return
		}

		// Anonymous fail-closed: an unauthenticated request has no
		// useful authz signal to compute. credentials.Wrap already
		// gates this, but defense-in-depth.
		actor := p.Actor()
		if actor == "" || actor == "anonymous" {
			writeJSON(w, http.StatusOK, allDeny(req.Checks, "unauthenticated"))
			return
		}

		results := make([]CanIResult, len(req.Checks))
		// Track which indices we have not yet resolved. We fill in
		// cache hits first, then route the misses through SSRR or SAR.
		impGroups := p.Impersonation().Groups
		pending := make([]int, 0, len(req.Checks))
		for i, ch := range req.Checks {
			if cached, ok := cache.Get(actor, c.Name, impGroups, ch); ok {
				results[i] = cached
				continue
			}
			pending = append(pending, i)
		}

		if len(pending) == 0 {
			writeJSON(w, http.StatusOK, CanIResponse{Results: results})
			return
		}

		// Bucket pending indices by SSRR-eligible namespace. SSRR is
		// worthwhile only when 3+ checks in the same namespace are
		// SSRR-eligible (no subresource, namespaced, no resourceName
		// scoping — RBAC ResourceNames don't round-trip cleanly via
		// SSRR's rule set).
		nsBuckets := map[string][]int{}
		var sarIdxs []int
		for _, i := range pending {
			ch := req.Checks[i]
			if ssrrEligible(ch) {
				nsBuckets[ch.Namespace] = append(nsBuckets[ch.Namespace], i)
			} else {
				sarIdxs = append(sarIdxs, i)
			}
		}

		// SSRR per namespace where the bucket is large enough.
		for ns, idxs := range nsBuckets {
			if len(idxs) < caniSSRRBatchThreshold {
				sarIdxs = append(sarIdxs, idxs...)
				continue
			}
			rules, err := caniListSSRRFn(r.Context(), p, c, ns)
			if err != nil {
				code := ErrorCodeFor(err)
				slog.Warn("cani SSRR failed",
					"cluster", c.Name, "namespace", ns, "code", code, "err", err)
				// Fail-closed for the whole namespace bucket.
				for _, i := range idxs {
					results[i] = CanIResult{Allowed: false, Reason: code}
					cache.Put(actor, c.Name, impGroups, req.Checks[i], results[i])
				}
				continue
			}
			for _, i := range idxs {
				ch := req.Checks[i]
				allowed := k8s.EvaluateSSRR(rules, k8s.SSRRCheck{
					Verb:     ch.Verb,
					Group:    ch.Group,
					Resource: ch.Resource,
					Name:     ch.Name,
				})
				res := CanIResult{Allowed: allowed}
				results[i] = res
				cache.Put(actor, c.Name, impGroups, ch, res)
			}
		}

		// Per-check SAR for the remainder.
		for _, i := range sarIdxs {
			ch := req.Checks[i]
			attr := authv1.ResourceAttributes{
				Namespace:   ch.Namespace,
				Verb:        ch.Verb,
				Group:       ch.Group,
				Resource:    ch.Resource,
				Subresource: ch.Subresource,
				Name:        ch.Name,
			}
			allowed, reason, err := caniCheckSARFn(r.Context(), p, c, attr)
			if err != nil {
				code := ErrorCodeFor(err)
				slog.Warn("cani SAR failed",
					"cluster", c.Name, "verb", ch.Verb, "resource", ch.Resource,
					"namespace", ch.Namespace, "code", code, "err", err)
				results[i] = CanIResult{Allowed: false, Reason: code}
				cache.Put(actor, c.Name, impGroups, ch, results[i])
				continue
			}
			res := CanIResult{Allowed: allowed, Reason: reason}
			results[i] = res
			cache.Put(actor, c.Name, impGroups, ch, res)
		}

		writeJSON(w, http.StatusOK, CanIResponse{Results: results})
	}
}

// ssrrEligible reports whether a check can be evaluated via SSRR's
// returned rule set. Cluster-scoped checks (empty namespace),
// subresources, and resourceName-scoped checks all need SAR.
func ssrrEligible(ch CanICheck) bool {
	if ch.Namespace == "" {
		return false
	}
	if ch.Subresource != "" {
		return false
	}
	if ch.Name != "" {
		// SSRR's ResourceRules.ResourceNames is honored, but only when
		// the rule explicitly lists names. Mixed semantics across rule
		// sets make the per-check SAR safer here.
		return false
	}
	return true
}

// allDeny builds a fail-closed response with the supplied reason.
func allDeny(checks []CanICheck, reason string) CanIResponse {
	out := make([]CanIResult, len(checks))
	for i := range out {
		out[i] = CanIResult{Allowed: false, Reason: reason}
	}
	return CanIResponse{Results: out}
}
