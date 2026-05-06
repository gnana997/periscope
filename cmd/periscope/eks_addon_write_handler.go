package main

// eks_addon_write_handler.go — write surface for EKS managed
// add-ons (issue #119, PR-2).
//
//   POST /api/clusters/{cluster}/eks/addons             — install
//   GET  /api/clusters/{cluster}/eks/addons/catalog/{name}/configuration?version=X
//                                                       — schema fetch
//
// Async-by-design contract: CreateAddon returns 202 immediately
// with the addon resource in status=CREATING. The actual
// provisioning happens AWS-side over 1-5 minutes. The SPA polls
// GET /eks/addons/{name} (from #117) to watch the status flip; we
// invalidate the per-cluster addons cache (list + per-addon
// detail if we have a name) on every write so the next poll sees
// fresh state.
//
// Audit shape mirrors workload rollback (#71): paired
// Intent + Outcome rows with `addonName` + `addonVersion` in Extra.
// Intent emitted before the SDK call so a hung/aborted invocation
// still leaves a forensic trail.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// addonInstallMaxBodyBytes caps the install request body. The
// configurationValues blob is the only sizeable field; AWS itself
// caps it well under 64 KiB. 32 KiB is generous headroom and stops
// pathological / malicious payloads at the HTTP boundary instead of
// at the AWS SDK marshaller.
const addonInstallMaxBodyBytes = 32 * 1024

// addonWriteSDKTimeout caps a single CreateAddon call. AWS returns
// near-instantly (a few hundred ms) since the actual work is async;
// we set a tight ceiling so a hung call surfaces as a clean 502
// instead of holding the request open for a minute.
const addonWriteSDKTimeout = 6 * time.Second

// addonInstallRequest is the POST /eks/addons body shape.
//
// `resolveConflicts` is whitelist-validated server-side so a typo
// returns a clean 400 instead of bouncing through to AWS as a
// generic 502 E_AWS_API. `serviceAccountRoleARN` is forwarded
// verbatim — IAM scoping (`iam:PassRole`) lives on the operator's
// IAM policy side.
type addonInstallRequest struct {
	AddonName             string `json:"addonName"`
	AddonVersion          string `json:"addonVersion"`
	ConfigurationValues   string `json:"configurationValues,omitempty"`
	ServiceAccountRoleARN string `json:"serviceAccountRoleArn,omitempty"`
	ResolveConflicts      string `json:"resolveConflicts,omitempty"`
}

// addonUpgradeRequest is the PUT /eks/addons/{name} body shape.
// Same fields as install minus `addonName` (URL param) — `addonVersion`
// is the *target* version. Empty `addonVersion` is rejected so the
// SPA must explicitly choose; AWS would happily accept the default
// otherwise but the operator's intent is opaque without it.
type addonUpgradeRequest struct {
	AddonVersion          string `json:"addonVersion"`
	ConfigurationValues   string `json:"configurationValues,omitempty"`
	ServiceAccountRoleARN string `json:"serviceAccountRoleArn,omitempty"`
	ResolveConflicts      string `json:"resolveConflicts,omitempty"`
}

// AddonConfigurationResponse is the GET .../configuration payload —
// just the JSON-Schema string AWS publishes for the (addon, version)
// pair. Nil/empty is a legitimate response (older versions ship no
// schema) — the SPA falls back to the YAML editor.
type AddonConfigurationResponse struct {
	ConfigurationSchema string `json:"configurationSchema,omitempty"`
}

// validResolveConflicts is the AWS-accepted set. NONE / OVERWRITE /
// PRESERVE are documented in CreateAddon's API reference. Empty
// string is also accepted by AWS (defaults to NONE) so we permit
// it; we only reject typos / unknown values.
var validResolveConflicts = map[string]ekstypes.ResolveConflicts{
	"":          "",
	"NONE":      ekstypes.ResolveConflictsNone,
	"OVERWRITE": ekstypes.ResolveConflictsOverwrite,
	"PRESERVE":  ekstypes.ResolveConflictsPreserve,
}

// eksAddonInstallHandler — POST /eks/addons. Body validated +
// whitelist-checked; CreateAddon called with the operator's intent;
// audit pair emitted; 202 + addon detail returned on success.
func eksAddonInstallHandler(reg *clusters.Registry, addons *eksAddonsCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"add-on installs are only available for EKS-backed clusters")
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, addonInstallMaxBodyBytes))
		if err != nil {
			http.Error(w, "request body too large", http.StatusBadRequest)
			return
		}
		var req addonInstallRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "expected JSON body { addonName, addonVersion, ... }", http.StatusBadRequest)
			return
		}
		if req.AddonName == "" {
			writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST",
				"addonName is required")
			return
		}
		if req.AddonVersion == "" {
			writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST",
				"addonVersion is required")
			return
		}
		resolveConflicts, ok := validResolveConflicts[req.ResolveConflicts]
		if !ok {
			writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST",
				"resolveConflicts must be one of NONE, OVERWRITE, PRESERVE (or omitted)")
			return
		}

		actor := actorFromContext(r.Context())
		intent := audit.Event{
			Actor:   actor,
			Verb:    audit.VerbEKSAddonInstallIntent,
			Outcome: audit.OutcomeSuccess,
			Cluster: c.Name,
			Extra: map[string]any{
				"addonName":        req.AddonName,
				"addonVersion":     req.AddonVersion,
				"resolveConflicts": req.ResolveConflicts,
			},
		}
		emitter.Record(r.Context(), intent)

		client := newEKSAddonsClient(p, c)
		eksName := c.EKSName()

		ctx, cancel := context.WithTimeout(r.Context(), addonWriteSDKTimeout)
		defer cancel()
		in := &eks.CreateAddonInput{
			ClusterName:      &eksName,
			AddonName:        &req.AddonName,
			AddonVersion:     &req.AddonVersion,
			ResolveConflicts: resolveConflicts,
		}
		if req.ConfigurationValues != "" {
			in.ConfigurationValues = &req.ConfigurationValues
		}
		if req.ServiceAccountRoleARN != "" {
			in.ServiceAccountRoleArn = &req.ServiceAccountRoleARN
		}
		out, sdkErr := client.CreateAddon(ctx, in)

		// Outcome row goes out for every path — success, failure,
		// denial. Cache invalidation also happens unconditionally:
		// on AWS-side failure the addon may still be in some
		// intermediate state, and a stale ACTIVE row in the cache
		// would lie about it.
		addons.InvalidateList(c.Name)
		addons.InvalidateDetail(c.Name, req.AddonName)

		evt := audit.Event{
			Actor:   actor,
			Verb:    audit.VerbEKSAddonInstall,
			Cluster: c.Name,
			Extra: map[string]any{
				"addonName":        req.AddonName,
				"addonVersion":     req.AddonVersion,
				"resolveConflicts": req.ResolveConflicts,
			},
		}
		if sdkErr != nil {
			if errors.Is(sdkErr, context.Canceled) {
				return
			}
			slog.Warn("eks create addon failed",
				"cluster", c.Name, "addon", req.AddonName, "err", sdkErr)
			evt.Outcome = audit.OutcomeFailure
			evt.Reason = sdkErr.Error()
			emitter.Record(r.Context(), evt)
			status, code := awsErrorToStatus(sdkErr)
			writeAPIErrorJSON(w, status, code,
				"failed to install add-on: "+sdkErr.Error())
			return
		}

		// Success. AWS returns the addon resource with status
		// CREATING; we re-shape it onto the same wire type as the
		// detail endpoint so the SPA can render it without a
		// follow-up GET. The SPA's poll will pick up the status
		// flip from the cache-invalidated detail endpoint.
		//
		// buildAddonSummary expects (addon, catalog, clusterVer) but
		// we don't have the catalog or cluster k8s version on hand
		// here, and a CREATING addon hasn't installed any version
		// the catalog could annotate yet — pass nil/"" so the catalog-
		// derived fields stay zero. The follow-up GET (driven by
		// status-aware polling in useAddon) will fold the catalog in
		// once the addon is ACTIVE.
		var detail AddonDetail
		if out != nil && out.Addon != nil {
			detail = AddonDetail{
				AddonSummary:          buildAddonSummary(out.Addon, nil, ""),
				ARN:                   deref(out.Addon.AddonArn),
				ServiceAccountRoleARN: deref(out.Addon.ServiceAccountRoleArn),
				ConfigurationValues:   deref(out.Addon.ConfigurationValues),
				Owner:                 deref(out.Addon.Owner),
				Publisher:             deref(out.Addon.Publisher),
			}
		}

		evt.Outcome = audit.OutcomeSuccess
		emitter.Record(r.Context(), evt)
		writeJSON(w, http.StatusAccepted, detail)
	}
}

// eksAddonConfigurationHandler — GET .../addons/catalog/{name}/configuration?version=X.
// Lazy schema fetch for the install dialog (and the upgrade dialog
// in PR-3): when the operator picks a version, the SPA calls this
// to drive the schema-aware form.
//
// Cached for 24h per (addon, version) — schemas are immutable per
// version so a long TTL is safe. Sticky errors at the same TTL.
func eksAddonConfigurationHandler(reg *clusters.Registry, schemas *addonConfigSchemaCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"add-on configuration schema is only available for EKS-backed clusters")
			return
		}
		name := chi.URLParam(r, "name")
		version := r.URL.Query().Get("version")
		if name == "" {
			http.Error(w, "missing add-on name", http.StatusBadRequest)
			return
		}
		if version == "" {
			http.Error(w, "missing version query param", http.StatusBadRequest)
			return
		}

		if cached, cachedErr, hit := schemas.Get(name, version); hit {
			if cachedErr != nil {
				emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "configuration:cache_hit", cachedErr.Error())
				status, code := awsErrorToStatus(cachedErr)
				writeAPIErrorJSON(w, status, code,
					"failed to fetch add-on configuration schema: "+cachedErr.Error())
				return
			}
			schema := ""
			if cached != nil {
				schema = cached.ConfigurationSchema
			}
			writeJSON(w, http.StatusOK, AddonConfigurationResponse{ConfigurationSchema: schema})
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "configuration:cache_hit", "")
			return
		}

		client := newEKSAddonsClient(p, c)
		ctx, cancel := context.WithTimeout(r.Context(), addonDescribeTimeout)
		defer cancel()
		out, err := client.DescribeAddonConfiguration(ctx, &eks.DescribeAddonConfigurationInput{
			AddonName:    &name,
			AddonVersion: &version,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks describe addon configuration failed",
				"cluster", c.Name, "addon", name, "version", version, "err", err)
			schemas.Put(name, version, nil, err)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "configuration", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to fetch add-on configuration schema: "+err.Error())
			return
		}

		val := &addonConfigSchemaCacheValue{ConfigurationSchema: deref(out.ConfigurationSchema)}
		schemas.Put(name, version, val, nil)
		writeJSON(w, http.StatusOK, AddonConfigurationResponse{ConfigurationSchema: val.ConfigurationSchema})
		emitAddonsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "configuration", "")
	}
}

// eksAddonUpgradeHandler — PUT /eks/addons/{name}. Body shape
// matches install minus `addonName` (URL param). Wraps eks:UpdateAddon
// with the same async-by-design contract: returns 202 with the
// addon detail in status=UPDATING; SPA polls /eks/addons/{name} for
// the flip.
func eksAddonUpgradeHandler(reg *clusters.Registry, addons *eksAddonsCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"add-on upgrades are only available for EKS-backed clusters")
			return
		}
		name := chi.URLParam(r, "name")
		if name == "" {
			http.Error(w, "missing add-on name", http.StatusBadRequest)
			return
		}

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, addonInstallMaxBodyBytes))
		if err != nil {
			http.Error(w, "request body too large", http.StatusBadRequest)
			return
		}
		var req addonUpgradeRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "expected JSON body { addonVersion, ... }", http.StatusBadRequest)
			return
		}
		if req.AddonVersion == "" {
			writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST",
				"addonVersion is required (target version)")
			return
		}
		resolveConflicts, ok := validResolveConflicts[req.ResolveConflicts]
		if !ok {
			writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST",
				"resolveConflicts must be one of NONE, OVERWRITE, PRESERVE (or omitted)")
			return
		}

		actor := actorFromContext(r.Context())
		intent := audit.Event{
			Actor:   actor,
			Verb:    audit.VerbEKSAddonUpgradeIntent,
			Outcome: audit.OutcomeSuccess,
			Cluster: c.Name,
			Extra: map[string]any{
				"addonName":        name,
				"addonVersion":     req.AddonVersion,
				"resolveConflicts": req.ResolveConflicts,
			},
		}
		emitter.Record(r.Context(), intent)

		client := newEKSAddonsClient(p, c)
		eksName := c.EKSName()

		ctx, cancel := context.WithTimeout(r.Context(), addonWriteSDKTimeout)
		defer cancel()
		in := &eks.UpdateAddonInput{
			ClusterName:      &eksName,
			AddonName:        &name,
			AddonVersion:     &req.AddonVersion,
			ResolveConflicts: resolveConflicts,
		}
		if req.ConfigurationValues != "" {
			in.ConfigurationValues = &req.ConfigurationValues
		}
		if req.ServiceAccountRoleARN != "" {
			in.ServiceAccountRoleArn = &req.ServiceAccountRoleARN
		}
		_, sdkErr := client.UpdateAddon(ctx, in)

		// Cache invalidation runs unconditionally — see the install
		// handler's rationale.
		addons.InvalidateList(c.Name)
		addons.InvalidateDetail(c.Name, name)

		evt := audit.Event{
			Actor:   actor,
			Verb:    audit.VerbEKSAddonUpgrade,
			Cluster: c.Name,
			Extra: map[string]any{
				"addonName":        name,
				"addonVersion":     req.AddonVersion,
				"resolveConflicts": req.ResolveConflicts,
			},
		}
		if sdkErr != nil {
			if errors.Is(sdkErr, context.Canceled) {
				return
			}
			slog.Warn("eks update addon failed",
				"cluster", c.Name, "addon", name, "err", sdkErr)
			evt.Outcome = audit.OutcomeFailure
			evt.Reason = sdkErr.Error()
			emitter.Record(r.Context(), evt)
			status, code := awsErrorToStatus(sdkErr)
			writeAPIErrorJSON(w, status, code,
				"failed to upgrade add-on: "+sdkErr.Error())
			return
		}

		// UpdateAddonOutput carries an Update record (id + status),
		// not a full Addon resource. The SPA's poll of
		// GET /eks/addons/{name} will surface the new state; here
		// we return a thin status-only shape so the dialog can
		// confirm the request was accepted.
		evt.Outcome = audit.OutcomeSuccess
		emitter.Record(r.Context(), evt)
		writeJSON(w, http.StatusAccepted, AddonDetail{
			AddonSummary: AddonSummary{
				Name:    name,
				Status:  string(ekstypes.AddonStatusUpdating),
				Version: req.AddonVersion,
			},
		})
	}
}

// eksAddonDeleteHandler — DELETE /eks/addons/{name}?preserve=true|false.
// Wraps eks:DeleteAddon. The optional `preserve` query param maps
// to AWS's Preserve flag — when true, the underlying K8s resources
// (deployments, configmaps, etc.) stay even after the addon resource
// is gone. Default false.
//
// Returns 202; status flips to DELETING and the resource disappears
// once AWS finishes tearing it down.
func eksAddonDeleteHandler(reg *clusters.Registry, addons *eksAddonsCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"add-on deletes are only available for EKS-backed clusters")
			return
		}
		name := chi.URLParam(r, "name")
		if name == "" {
			http.Error(w, "missing add-on name", http.StatusBadRequest)
			return
		}
		// `preserve` accepts standard truthy strings. Anything else
		// (including missing) is false.
		preserve := false
		switch r.URL.Query().Get("preserve") {
		case "true", "1", "yes":
			preserve = true
		}

		actor := actorFromContext(r.Context())
		intent := audit.Event{
			Actor:   actor,
			Verb:    audit.VerbEKSAddonDeleteIntent,
			Outcome: audit.OutcomeSuccess,
			Cluster: c.Name,
			Extra: map[string]any{
				"addonName": name,
				"preserve":  preserve,
			},
		}
		emitter.Record(r.Context(), intent)

		client := newEKSAddonsClient(p, c)
		eksName := c.EKSName()

		ctx, cancel := context.WithTimeout(r.Context(), addonWriteSDKTimeout)
		defer cancel()
		_, sdkErr := client.DeleteAddon(ctx, &eks.DeleteAddonInput{
			ClusterName: &eksName,
			AddonName:   &name,
			Preserve:    preserve,
		})

		addons.InvalidateList(c.Name)
		addons.InvalidateDetail(c.Name, name)

		evt := audit.Event{
			Actor:   actor,
			Verb:    audit.VerbEKSAddonDelete,
			Cluster: c.Name,
			Extra: map[string]any{
				"addonName": name,
				"preserve":  preserve,
			},
		}
		if sdkErr != nil {
			if errors.Is(sdkErr, context.Canceled) {
				return
			}
			slog.Warn("eks delete addon failed",
				"cluster", c.Name, "addon", name, "err", sdkErr)
			evt.Outcome = audit.OutcomeFailure
			evt.Reason = sdkErr.Error()
			emitter.Record(r.Context(), evt)
			status, code := awsErrorToStatus(sdkErr)
			writeAPIErrorJSON(w, status, code,
				"failed to delete add-on: "+sdkErr.Error())
			return
		}

		evt.Outcome = audit.OutcomeSuccess
		emitter.Record(r.Context(), evt)
		writeJSON(w, http.StatusAccepted, AddonDetail{
			AddonSummary: AddonSummary{
				Name:   name,
				Status: string(ekstypes.AddonStatusDeleting),
			},
		})
	}
}
