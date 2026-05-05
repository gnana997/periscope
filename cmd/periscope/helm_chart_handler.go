// helm_chart_handler.go — chart fetch HTTP surface (issue #73).
//
// Two endpoints, kept distinct so caches and audit emission can
// differ per operation:
//
//	GET  /api/clusters/{cluster}/helm/chart/versions?ref=...&chart=...&nocache=true
//	POST /api/clusters/{cluster}/helm/chart/values        body: {ref, chart?, version}
//
// The {cluster} path param is reserved for v1.2's per-cluster
// credential resolution (ECR via Pod Identity / IRSA, registry
// credentials in cluster ConfigMaps). v1.1 ignores it — every chart
// fetch is unauthenticated public-only — but the path shape stays
// stable so the v1.2 expansion is non-breaking for the SPA.
//
// The versions endpoint is GET (idempotent, query-string-driven, no
// audit row) so the SPA can cheaply re-fetch on every dialog open.
// The values endpoint is POST because:
//   1. The request payload is genuinely a body (ref + chart + version
//      are operator-supplied; ref can be long enough to exceed URL
//      length recommendations on some proxies).
//   2. It emits an audit row — POST clarifies the user-initiated
//      intent vs the GET that's called while typing.
//
// Both endpoints accept ?nocache=true so the SPA can wire a
// "refresh" button when an operator pushed a new version mid-session.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// chartVersionsHandler is GET /helm/chart/versions. Light-touch:
// validates the ref, hits the cache, falls back to fetch. No audit
// row — this fires while the operator is typing into the URL field
// and would bury any meaningful intent in noise.
func chartVersionsHandler(reg *clusters.Registry, vCache *chartVersionsCache) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		// Cluster lookup is for forward-compat with v1.2's per-cluster
		// credential resolution; in v1.1 we only validate that the
		// cluster exists, then ignore it.
		if _, ok := reg.ByName(chi.URLParam(r, "cluster")); !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}

		q := r.URL.Query()
		ref := strings.TrimSpace(q.Get("ref"))
		chart := strings.TrimSpace(q.Get("chart"))
		nocache := q.Get("nocache") == "true"

		if ref == "" {
			http.Error(w, "ref query param required", http.StatusBadRequest)
			return
		}

		// Cache key includes chart name because the same HTTP repo
		// hosts many charts; OCI refs encode the chart in the path
		// already so chart will be empty there.
		key := ref + "|" + chart
		if !nocache {
			if hit, ok := vCache.Get(key); ok {
				writeJSON(w, http.StatusOK, hit)
				return
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), chartHandlerTimeout)
		defer cancel()
		result, err := k8s.FetchChartVersions(ctx, k8s.FetchVersionsArgs{
			Ref:       ref,
			ChartName: chart,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			writeAPIError(w, err, classifyChartFetchErr(err))
			return
		}
		vCache.Put(key, result)
		writeJSON(w, http.StatusOK, result)
	}
}

// chartValuesHandler is POST /helm/chart/values. Audited: emits one
// VerbHelmChartFetch row on success (or failure outcome on error)
// per Fetch button click.
func chartValuesHandler(
	reg *clusters.Registry,
	valCache *chartValuesCache,
	auditer *audit.Emitter,
) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8*1024))
		if err != nil {
			http.Error(w, "request body too large", http.StatusBadRequest)
			return
		}
		var req struct {
			Ref     string `json:"ref"`
			Chart   string `json:"chart"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &req); err != nil ||
			strings.TrimSpace(req.Ref) == "" ||
			strings.TrimSpace(req.Version) == "" {
			http.Error(w, "expected JSON { ref: string, chart?: string, version: string }", http.StatusBadRequest)
			return
		}
		req.Ref = strings.TrimSpace(req.Ref)
		req.Chart = strings.TrimSpace(req.Chart)
		req.Version = strings.TrimSpace(req.Version)

		nocache := r.URL.Query().Get("nocache") == "true"
		key := chartValuesKey(req.Ref, req.Chart, req.Version)

		actor := actorFromContext(r.Context())
		evt := audit.Event{
			Actor:   actor,
			Verb:    audit.VerbHelmChartFetch,
			Cluster: c.Name,
			Extra: map[string]any{
				"ref":     req.Ref,
				"chart":   req.Chart,
				"version": req.Version,
				"cached":  false,
			},
		}

		if !nocache {
			if hit, ok := valCache.Get(key); ok {
				evt.Extra["cached"] = true
				evt.Outcome = audit.OutcomeSuccess
				auditer.Record(r.Context(), evt)
				writeJSON(w, http.StatusOK, hit)
				return
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), chartHandlerTimeout)
		defer cancel()
		result, err := k8s.FetchChartValues(ctx, k8s.FetchValuesArgs{
			Ref:       req.Ref,
			ChartName: req.Chart,
			Version:   req.Version,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			evt.Outcome = outcomeFor(err)
			evt.Reason = err.Error()
			auditer.Record(r.Context(), evt)
			writeAPIError(w, err, classifyChartFetchErr(err))
			return
		}
		valCache.Put(key, result)
		evt.Outcome = audit.OutcomeSuccess
		auditer.Record(r.Context(), evt)
		writeJSON(w, http.StatusOK, result)
	}
}

// chartHandlerTimeout caps the entire fetch+unpack sequence. Slightly
// above the inner HTTP timeout (10s) so the handler is the outer
// guard, not racy with chartFetchClient.Timeout.
const chartHandlerTimeout = 15 * time.Second

// classifyChartFetchErr maps the typed sentinels from
// internal/k8s/helm_chart.go to HTTP status codes. Falls through to
// httpStatusFor for anything unrecognized.
func classifyChartFetchErr(err error) int {
	switch {
	case errors.Is(err, k8s.ErrChartNotFound),
		errors.Is(err, k8s.ErrChartVersionNotFound):
		return http.StatusNotFound
	case errors.Is(err, k8s.ErrChartUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, k8s.ErrChartUnsupportedRef),
		errors.Is(err, k8s.ErrChartUnsupportedDeps),
		errors.Is(err, k8s.ErrChartNotAChart),
		errors.Is(err, k8s.ErrChartInvalid):
		return http.StatusUnprocessableEntity
	case errors.Is(err, k8s.ErrChartTimeout):
		return http.StatusGatewayTimeout
	case errors.Is(err, k8s.ErrChartUnreachable):
		return http.StatusBadGateway
	}
	return httpStatusFor(err)
}
