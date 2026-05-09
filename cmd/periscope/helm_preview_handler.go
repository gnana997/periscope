// helm_preview_handler.go — HTTP handlers for the helm dry-run +
// diff preview backend (issue #75).
//
// Two endpoints, two handlers, one shared response shape:
//
//   POST /api/clusters/{cluster}/helm/install-preview
//     body: { ref, version, chartName?, namespace, releaseName, values }
//
//   POST /api/clusters/{cluster}/helm/releases/{ns}/{name}/upgrade-preview
//     body: { ref, version, chartName?, values }
//
// The install-preview path has no existing release in its URL — the
// release name is operator-supplied in the body (it's the name they
// WOULD use). The upgrade-preview path follows the established
// /helm/releases/{ns}/{name}/{verb} convention because the release
// exists; ns/name come from the URL path.
//
// Both handlers run a per-manifest RBAC pre-flight inline (verb=create
// for install, verb=patch for upgrade) and surface the denied list as
// a top-level field on the response. The SPA renders denials inline so
// operators see "the apiserver would reject these objects" before they
// hit the actual install/upgrade button.
//
// Audit emission: VerbHelmPreview, single verb both modes, with `op`
// in Extra distinguishing "install" vs "upgrade". Mirrors the
// VerbEKSInsightsRead pattern (one verb covers list + detail with op
// in Extra).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// previewHelmInstallFn / previewHelmUpgradeFn are test seams.
// Production wires the real k8s.PreviewHelm{Install,Upgrade} entry
// points; tests substitute fakes that return canned PreviewResult /
// errors so the handler-level concerns (validation, response
// shaping, audit emission, error mapping) can be tested without
// also exercising chart fetch + helm SDK.
var (
	previewHelmInstallFn = k8s.PreviewHelmInstall
	previewHelmUpgradeFn = k8s.PreviewHelmUpgrade
)

// helmInstallPreviewRequest is the install-preview body shape.
// Namespace + ReleaseName are caller-supplied because no release
// exists yet — they're the values that WOULD be used.
type helmInstallPreviewRequest struct {
	Ref         string `json:"ref"`
	ChartName   string `json:"chartName,omitempty"` // required for HTTP refs, ignored for OCI
	Version     string `json:"version"`
	Namespace   string `json:"namespace"`
	ReleaseName string `json:"releaseName"`
	Values      string `json:"values"` // verbatim values.yaml; "" = chart defaults
}

// helmUpgradePreviewRequest is the upgrade-preview body shape.
// Namespace + release name come from the URL path; the body just
// carries the proposed (ref, version, values).
type helmUpgradePreviewRequest struct {
	Ref       string `json:"ref"`
	ChartName string `json:"chartName,omitempty"`
	Version   string `json:"version"`
	Values    string `json:"values"`
}

// helmInstallPreviewHandler — POST /api/clusters/{cluster}/helm/install-preview
func helmInstallPreviewHandler(reg *clusters.Registry, emitter *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}

		var req helmInstallPreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Ref == "" || req.Version == "" || req.Namespace == "" || req.ReleaseName == "" {
			http.Error(w, "ref, version, namespace, and releaseName are all required", http.StatusBadRequest)
			return
		}

		args := k8s.PreviewArgs{
			Ref:         req.Ref,
			ChartName:   req.ChartName,
			Version:     req.Version,
			Namespace:   req.Namespace,
			ReleaseName: req.ReleaseName,
			Values:      req.Values,
		}
		result, err := previewHelmInstallFn(r.Context(), p, c, args)
		if err != nil {
			slog.WarnContext(r.Context(), "helm install preview failed",
				"cluster", c.Name, "ref", req.Ref, "version", req.Version,
				"namespace", req.Namespace, "releaseName", req.ReleaseName, "err", err)
			emitHelmPreview(r.Context(), emitter, c, audit.OutcomeFailure, "install", err.Error())
			status, code := classifyHelmPreviewErr(err)
			writeAPIErrorJSON(w, status, code, err.Error())
			return
		}

		outcome := audit.OutcomeSuccess
		if len(result.Denied) > 0 {
			// Pre-flight denial isn't a server-side failure — the SPA
			// shows the denial inline — but it's forensically useful
			// to mark the audit row OutcomeDenied so a "what did the
			// user try to install that they couldn't" query lands
			// these rows separately from clean previews.
			outcome = audit.OutcomeDenied
		}
		emitHelmPreview(r.Context(), emitter, c, outcome, "install", "")
		writeJSON(w, http.StatusOK, result)
	}
}

// helmUpgradePreviewHandler — POST /api/clusters/{cluster}/helm/releases/{ns}/{name}/upgrade-preview
func helmUpgradePreviewHandler(reg *clusters.Registry, emitter *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := chi.URLParam(r, "ns")
		name := chi.URLParam(r, "name")
		if ns == "" || name == "" {
			http.Error(w, "namespace and name path params required", http.StatusBadRequest)
			return
		}

		var req helmUpgradePreviewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Ref == "" || req.Version == "" {
			http.Error(w, "ref and version are required", http.StatusBadRequest)
			return
		}

		args := k8s.PreviewArgs{
			Ref:         req.Ref,
			ChartName:   req.ChartName,
			Version:     req.Version,
			Namespace:   ns,
			ReleaseName: name,
			Values:      req.Values,
		}
		result, err := previewHelmUpgradeFn(r.Context(), p, c, args)
		if err != nil {
			slog.WarnContext(r.Context(), "helm upgrade preview failed",
				"cluster", c.Name, "ref", req.Ref, "version", req.Version,
				"namespace", ns, "name", name, "err", err)
			emitHelmPreview(r.Context(), emitter, c, audit.OutcomeFailure, "upgrade", err.Error())
			status, code := classifyHelmPreviewErr(err)
			writeAPIErrorJSON(w, status, code, err.Error())
			return
		}

		outcome := audit.OutcomeSuccess
		if len(result.Denied) > 0 {
			outcome = audit.OutcomeDenied
		}
		emitHelmPreview(r.Context(), emitter, c, outcome, "upgrade", "")
		writeJSON(w, http.StatusOK, result)
	}
}

// classifyHelmPreviewErr maps a preview-path error into (httpStatus,
// errorCode). The preview path has two error families:
//
//  1. Chart-fetch errors — sentinels from internal/k8s/helm_chart.go.
//     We mirror classifyChartFetchErr's status mapping but also pin
//     stable error codes (E_CHART_*) so the SPA can branch on the
//     specific cause without parsing free-form messages.
//
//  2. Helm SDK errors — wrapped through helmErrorToStatus.
//
// Order matters: chart errors are checked first because the helm SDK
// never sees them (they fail before LoadArchive). Helm SDK errors are
// the residual after chart-fetch passes.
func classifyHelmPreviewErr(err error) (int, string) {
	switch {
	case errors.Is(err, k8s.ErrChartNotFound):
		return http.StatusNotFound, "E_CHART_NOT_FOUND"
	case errors.Is(err, k8s.ErrChartVersionNotFound):
		return http.StatusNotFound, "E_CHART_VERSION_NOT_FOUND"
	case errors.Is(err, k8s.ErrChartUnauthorized):
		return http.StatusUnauthorized, "E_CHART_UNAUTHORIZED"
	case errors.Is(err, k8s.ErrChartUnsupportedRef):
		return http.StatusUnprocessableEntity, "E_CHART_UNSUPPORTED_REF"
	case errors.Is(err, k8s.ErrChartUnsupportedDeps):
		return http.StatusUnprocessableEntity, "E_CHART_UNSUPPORTED_DEPS"
	case errors.Is(err, k8s.ErrChartNotAChart):
		return http.StatusUnprocessableEntity, "E_CHART_NOT_A_CHART"
	case errors.Is(err, k8s.ErrChartInvalid):
		return http.StatusUnprocessableEntity, "E_CHART_INVALID"
	case errors.Is(err, k8s.ErrChartTimeout):
		return http.StatusGatewayTimeout, "E_CHART_TIMEOUT"
	case errors.Is(err, k8s.ErrChartUnreachable):
		return http.StatusBadGateway, "E_CHART_UNREACHABLE"
	}
	return helmErrorToStatus(err)
}

// emitHelmPreview centralises the audit shape for helm_preview rows.
// Mirrors emitInsightsRead's pattern — pulls actor from context,
// always carries the op (install vs upgrade) in Extra so reviewers
// can filter "what did this user preview" queries by mode.
func emitHelmPreview(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, outcome audit.Outcome, op, reason string) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbHelmPreview,
		Outcome: outcome,
		Cluster: c.Name,
		Reason:  reason,
		Extra: map[string]any{
			"op": op,
		},
	})
}
