// helm_action_handler.go — HTTP handlers for helm install + upgrade
// actions (issue #76).
//
// Two endpoints, two handlers, one shared response shape:
//
//   POST /api/clusters/{cluster}/helm/install
//     body: { ref, version, chartName?, namespace, releaseName,
//             values, atomic?, wait?, waitForJobs?, includeCRDs?,
//             timeoutSeconds? }
//
//   POST /api/clusters/{cluster}/helm/releases/{ns}/{name}/upgrade
//     body: { ref, version, chartName?, values, atomic?, wait?,
//             waitForJobs?, timeoutSeconds?, cleanupOnFail?,
//             maxHistory? }
//
// Sync: blocks until the helm SDK call returns. Default timeout 5min,
// capped at 10min server-side. The HTTP server holds the connection;
// SPA shows a spinner.
//
// Audit: pre/post pair (intent + outcome). Intent fires BEFORE the
// helm SDK call so a partition / hung apiserver still leaves a
// forensic trail. Outcome fires AFTER, capturing the new revision +
// rollback flag on Atomic-failure cases.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// Handler-level test seams. Production wires the real
// k8s.{Install,Upgrade,Uninstall}HelmRelease entry points; tests
// substitute fakes that return canned results / errors so the
// handler-level concerns (validation, response shaping, audit pair
// emission, error mapping) stay testable without spinning up real
// helm SDK.
var (
	installHelmReleaseFn   = k8s.InstallHelmRelease
	upgradeHelmReleaseFn   = k8s.UpgradeHelmRelease
	uninstallHelmReleaseFn = k8s.UninstallHelmRelease
)

// helmActionDefaultTimeout / helmActionMaxTimeout are server-side
// caps on the per-call helm SDK timeout. The body's
// timeoutSeconds field is honored within these bounds; outside is
// clamped silently. Operators with a genuinely long-install chart
// can bump the max in code; v1.1 caps conservatively to keep one
// stuck install from holding the HTTP server thread for an hour.
const (
	helmActionDefaultTimeout = 5 * time.Minute
	helmActionMaxTimeout     = 10 * time.Minute
)

// helmInstallRequest is the install body. Fields default sensibly
// when omitted — atomic + wait + includeCRDs default to true; jobs +
// timeout follow the spec.
type helmInstallRequest struct {
	Ref            string `json:"ref"`
	ChartName      string `json:"chartName,omitempty"`
	Version        string `json:"version"`
	Namespace      string `json:"namespace"`
	ReleaseName    string `json:"releaseName"`
	Values         string `json:"values"`
	Atomic         *bool  `json:"atomic,omitempty"`
	Wait           *bool  `json:"wait,omitempty"`
	WaitForJobs    bool   `json:"waitForJobs,omitempty"`
	IncludeCRDs    *bool  `json:"includeCRDs,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
}

// helmUpgradeRequest is the upgrade body. Namespace + ReleaseName
// come from the URL path; the body just carries the proposed
// (ref, version, values) plus action knobs.
type helmUpgradeRequest struct {
	Ref            string `json:"ref"`
	ChartName      string `json:"chartName,omitempty"`
	Version        string `json:"version"`
	Values         string `json:"values"`
	Atomic         *bool  `json:"atomic,omitempty"`
	Wait           *bool  `json:"wait,omitempty"`
	WaitForJobs    bool   `json:"waitForJobs,omitempty"`
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	CleanupOnFail  bool   `json:"cleanupOnFail,omitempty"`
	MaxHistory     int    `json:"maxHistory,omitempty"`
}

// helmInstallHandler — POST /api/clusters/{cluster}/helm/install
func helmInstallHandler(reg *clusters.Registry, emitter *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		var req helmInstallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Ref == "" || req.Version == "" || req.Namespace == "" || req.ReleaseName == "" {
			http.Error(w, "ref, version, namespace, and releaseName are all required", http.StatusBadRequest)
			return
		}

		args := k8s.InstallArgs{
			Ref:         req.Ref,
			ChartName:   req.ChartName,
			Version:     req.Version,
			Namespace:   req.Namespace,
			ReleaseName: req.ReleaseName,
			Values:      req.Values,
			Atomic:      boolDefault(req.Atomic, true),
			Wait:        boolDefault(req.Wait, true),
			WaitForJobs: req.WaitForJobs,
			IncludeCRDs: boolDefault(req.IncludeCRDs, true),
			Timeout:     clampTimeout(req.TimeoutSeconds),
		}

		// Intent audit before SDK call. Carries the action knobs so
		// post-incident review can correlate "operator tried install
		// X with atomic=false" against a known-bad outcome.
		emitHelmInstallIntent(r.Context(), emitter, c, req, args)

		result, err := installHelmReleaseFn(r.Context(), p, c, args)
		if err != nil {
			handleHelmActionFailure(r.Context(), w, emitter, c, audit.VerbHelmInstall, "install", req, args, err)
			return
		}
		emitHelmInstallOutcome(r.Context(), emitter, c, audit.OutcomeSuccess, req, args, result, "")
		writeJSON(w, http.StatusOK, result)
	}
}

// helmUpgradeHandler — POST /api/clusters/{cluster}/helm/releases/{ns}/{name}/upgrade
func helmUpgradeHandler(reg *clusters.Registry, emitter *audit.Emitter) credentials.Handler {
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
		var req helmUpgradeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if req.Ref == "" || req.Version == "" {
			http.Error(w, "ref and version are required", http.StatusBadRequest)
			return
		}

		args := k8s.UpgradeArgs{
			Ref:           req.Ref,
			ChartName:     req.ChartName,
			Version:       req.Version,
			Namespace:     ns,
			ReleaseName:   name,
			Values:        req.Values,
			Atomic:        boolDefault(req.Atomic, true),
			Wait:          boolDefault(req.Wait, true),
			WaitForJobs:   req.WaitForJobs,
			Timeout:       clampTimeout(req.TimeoutSeconds),
			CleanupOnFail: req.CleanupOnFail,
			MaxHistory:    req.MaxHistory,
		}

		emitHelmUpgradeIntent(r.Context(), emitter, c, ns, name, req, args)

		result, err := upgradeHelmReleaseFn(r.Context(), p, c, args)
		if err != nil {
			handleHelmActionFailureUpgrade(r.Context(), w, emitter, c, ns, name, req, args, err)
			return
		}
		emitHelmUpgradeOutcome(r.Context(), emitter, c, ns, name, audit.OutcomeSuccess, req, args, result, "")
		writeJSON(w, http.StatusOK, result)
	}
}

// handleHelmActionFailure routes a helm install error through the
// audit + HTTP error pipeline. Three classes:
//
//  1. DeniedError (pre-flight SAR denial) → 403 with denied list.
//  2. Chart-fetch sentinel → reuse classifyHelmPreviewErr from
//     helm_preview_handler.go (same status mapping).
//  3. Helm SDK error → helmErrorToStatus.
func handleHelmActionFailure(ctx context.Context, w http.ResponseWriter, emitter *audit.Emitter, c clusters.Cluster, verb audit.Verb, op string, req helmInstallRequest, args k8s.InstallArgs, err error) {
	if denied, ok := k8s.IsDeniedError(err); ok {
		emitHelmInstallOutcome(ctx, emitter, c, audit.OutcomeDenied, req, args, nil, err.Error())
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "E_HELM_PREFLIGHT_DENIED",
			"message": "RBAC pre-flight denied one or more resources",
			"denied":  denied.Denials,
		})
		return
	}
	slog.WarnContext(ctx, "helm install failed",
		"cluster", c.Name, "ref", req.Ref, "version", req.Version,
		"namespace", req.Namespace, "releaseName", req.ReleaseName, "err", err)
	emitHelmInstallOutcome(ctx, emitter, c, audit.OutcomeFailure, req, args, nil, err.Error())
	status, code := classifyHelmPreviewErr(err)
	writeAPIErrorJSON(w, status, code, err.Error())
}

// handleHelmActionFailureUpgrade is the upgrade-mode counterpart. The
// upgrade path's audit shape carries ns + name from the URL; rather
// than parameterize handleHelmActionFailure across both shapes, we
// keep two thin wrappers. The DRY win wouldn't justify the type
// gymnastics.
func handleHelmActionFailureUpgrade(ctx context.Context, w http.ResponseWriter, emitter *audit.Emitter, c clusters.Cluster, ns, name string, req helmUpgradeRequest, args k8s.UpgradeArgs, err error) {
	if denied, ok := k8s.IsDeniedError(err); ok {
		emitHelmUpgradeOutcome(ctx, emitter, c, ns, name, audit.OutcomeDenied, req, args, nil, err.Error())
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "E_HELM_PREFLIGHT_DENIED",
			"message": "RBAC pre-flight denied one or more resources",
			"denied":  denied.Denials,
		})
		return
	}
	slog.WarnContext(ctx, "helm upgrade failed",
		"cluster", c.Name, "ref", req.Ref, "version", req.Version,
		"namespace", ns, "releaseName", name, "err", err)
	emitHelmUpgradeOutcome(ctx, emitter, c, ns, name, audit.OutcomeFailure, req, args, nil, err.Error())
	status, code := classifyHelmPreviewErr(err)
	writeAPIErrorJSON(w, status, code, err.Error())
}

// emit* helpers centralize audit row construction. Intent rows
// capture the operator's stated request; outcome rows capture
// what actually happened (revision, rolledBack, error reason).

func emitHelmInstallIntent(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, req helmInstallRequest, args k8s.InstallArgs) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbHelmInstallIntent,
		Outcome: audit.OutcomeSuccess, // intent rows always "success" — captures intent, not outcome
		Cluster: c.Name,
		Resource: audit.ResourceRef{
			Namespace: req.Namespace,
			Name:      req.ReleaseName,
		},
		Extra: map[string]any{
			"ref":             req.Ref,
			"version":         req.Version,
			"atomic":          args.Atomic,
			"wait":            args.Wait,
			"timeoutSeconds":  int(args.Timeout / time.Second),
		},
	})
}

func emitHelmInstallOutcome(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, outcome audit.Outcome, req helmInstallRequest, args k8s.InstallArgs, result *k8s.HelmActionResult, reason string) {
	if emitter == nil {
		return
	}
	extra := map[string]any{
		"ref":     req.Ref,
		"version": req.Version,
		"atomic":  args.Atomic,
	}
	if result != nil {
		extra["revision"] = result.Release.Revision
		if result.RolledBack {
			extra["rolledBack"] = true
		}
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbHelmInstall,
		Outcome: outcome,
		Cluster: c.Name,
		Resource: audit.ResourceRef{
			Namespace: req.Namespace,
			Name:      req.ReleaseName,
		},
		Reason: reason,
		Extra:  extra,
	})
}

func emitHelmUpgradeIntent(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, ns, name string, req helmUpgradeRequest, args k8s.UpgradeArgs) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbHelmUpgradeIntent,
		Outcome: audit.OutcomeSuccess,
		Cluster: c.Name,
		Resource: audit.ResourceRef{
			Namespace: ns,
			Name:      name,
		},
		Extra: map[string]any{
			"ref":             req.Ref,
			"version":         req.Version,
			"atomic":          args.Atomic,
			"wait":            args.Wait,
			"timeoutSeconds":  int(args.Timeout / time.Second),
		},
	})
}

func emitHelmUpgradeOutcome(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, ns, name string, outcome audit.Outcome, req helmUpgradeRequest, args k8s.UpgradeArgs, result *k8s.HelmActionResult, reason string) {
	if emitter == nil {
		return
	}
	extra := map[string]any{
		"ref":     req.Ref,
		"version": req.Version,
		"atomic":  args.Atomic,
	}
	if result != nil {
		extra["revision"] = result.Release.Revision
		if result.RolledBack {
			extra["rolledBack"] = true
		}
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbHelmUpgrade,
		Outcome: outcome,
		Cluster: c.Name,
		Resource: audit.ResourceRef{
			Namespace: ns,
			Name:      name,
		},
		Reason: reason,
		Extra:  extra,
	})
}

// boolDefault returns the dereferenced bool when non-nil; otherwise
// the default. Lets request bodies leave the field out (nil) and
// have the handler apply a sensible default.
func boolDefault(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

// clampTimeout converts a body-supplied seconds value into a
// time.Duration, clamping into [helmActionDefaultTimeout, helmActionMaxTimeout].
// Zero / negative input gets the default.
func clampTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return helmActionDefaultTimeout
	}
	d := time.Duration(seconds) * time.Second
	if d > helmActionMaxTimeout {
		return helmActionMaxTimeout
	}
	return d
}

// silence unused for the errors import alias when builds drop the
// usage; kept defensively so subsequent additions don't have to
// re-add the import.
var _ = errors.As

// helmUninstallHandler — DELETE /api/clusters/{cluster}/helm/releases/{ns}/{name}
//
// Body: empty. Query params: keepHistory (bool, default false),
// disableHooks (bool, default false). Audit pair fires regardless
// of outcome — destructive actions need the intent row even when
// the operation hangs.
func helmUninstallHandler(reg *clusters.Registry, emitter *audit.Emitter) credentials.Handler {
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

		args := k8s.UninstallArgs{
			Namespace:    ns,
			ReleaseName:  name,
			KeepHistory:  r.URL.Query().Get("keepHistory") == "true",
			DisableHooks: r.URL.Query().Get("disableHooks") == "true",
		}

		emitHelmUninstallIntent(r.Context(), emitter, c, args)

		result, err := uninstallHelmReleaseFn(r.Context(), p, c, args)
		if err != nil {
			handleHelmUninstallFailure(r.Context(), w, emitter, c, args, err)
			return
		}
		emitHelmUninstallOutcome(r.Context(), emitter, c, audit.OutcomeSuccess, args, result, "")
		writeJSON(w, http.StatusOK, result)
	}
}

func handleHelmUninstallFailure(ctx context.Context, w http.ResponseWriter, emitter *audit.Emitter, c clusters.Cluster, args k8s.UninstallArgs, err error) {
	if denied, ok := k8s.IsDeniedError(err); ok {
		emitHelmUninstallOutcome(ctx, emitter, c, audit.OutcomeDenied, args, nil, err.Error())
		writeJSON(w, http.StatusForbidden, map[string]any{
			"code":    "E_HELM_PREFLIGHT_DENIED",
			"message": "RBAC pre-flight denied one or more resources",
			"denied":  denied.Denials,
		})
		return
	}
	slog.WarnContext(ctx, "helm uninstall failed",
		"cluster", c.Name, "namespace", args.Namespace, "releaseName", args.ReleaseName, "err", err)
	emitHelmUninstallOutcome(ctx, emitter, c, audit.OutcomeFailure, args, nil, err.Error())
	status, code := classifyHelmPreviewErr(err)
	writeAPIErrorJSON(w, status, code, err.Error())
}

func emitHelmUninstallIntent(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, args k8s.UninstallArgs) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbHelmUninstallIntent,
		Outcome: audit.OutcomeSuccess,
		Cluster: c.Name,
		Resource: audit.ResourceRef{
			Namespace: args.Namespace,
			Name:      args.ReleaseName,
		},
		Extra: map[string]any{
			"keepHistory":  args.KeepHistory,
			"disableHooks": args.DisableHooks,
		},
	})
}

func emitHelmUninstallOutcome(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, outcome audit.Outcome, args k8s.UninstallArgs, result *k8s.UninstallResult, reason string) {
	if emitter == nil {
		return
	}
	extra := map[string]any{
		"keepHistory":  args.KeepHistory,
		"disableHooks": args.DisableHooks,
	}
	if result != nil {
		extra["revisionsRemoved"] = result.RevisionsRemoved
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbHelmUninstall,
		Outcome: outcome,
		Cluster: c.Name,
		Resource: audit.ResourceRef{
			Namespace: args.Namespace,
			Name:      args.ReleaseName,
		},
		Reason: reason,
		Extra:  extra,
	})
}
