package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	authv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/awseks/iam"
	"github.com/gnana997/periscope/internal/awseks/identity"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// awsAccessDocsURL is the canonical docs path the locked-pane and
// every "missing permissions" advisory links back to. Relative
// path; the SPA prefixes the current origin.
const awsAccessDocsURL = "/docs/usage/aws-access"

// capabilitiesCacheTTL is how long a per-cluster, per-actor probe
// snapshot is reused. 5 min mirrors the user-stated UX trade-off:
// chatty enough to surface fresh state on common workflows, cheap
// enough that page-nav doesn't re-probe RBAC + IAM on every click.
// Operators bypass with `Cache-Control: no-cache` (Re-check button).
const capabilitiesCacheTTL = 5 * time.Minute

// awsAccessConfig is the operator-tunable knob set for #188. Loaded
// once from env vars at handler-construction time and threaded
// through to the capabilities probe.
//
// Today only IAMProbe matters; reserved as a struct so future flags
// (probe caller-arn override, cache TTL override) extend without a
// signature churn.
type awsAccessConfig struct {
	// IAMProbe, when true, asks the capabilities endpoint to call
	// iam:SimulatePrincipalPolicy against the server's own caller
	// identity for the five v1.1 IAM-read perms and emit
	// MISSING_IAM_PERMS with the exact missing list. When false,
	// the capabilities response stays optimistic (Available=true
	// with a Note) and the first real /workload-permissions or
	// /reverse-lookup call surfaces a 403 if a perm is missing.
	//
	// Default: true. Override with PERISCOPE_AWS_ACCESS_IAM_PROBE=false.
	IAMProbe bool
}

func loadAwsAccessConfig() awsAccessConfig {
	cfg := awsAccessConfig{IAMProbe: true}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("PERISCOPE_AWS_ACCESS_IAM_PROBE"))); v != "" {
		cfg.IAMProbe = !(v == "false" || v == "0" || v == "no" || v == "off")
	}
	return cfg
}

// ── /identity/workload-permissions ───────────────────────────────
//
// GET /api/clusters/{cluster}/identity/workload-permissions
//   ?kind=Pod|ServiceAccount|Deployment|StatefulSet|DaemonSet
//   &namespace=N&name=X
//
// Composed forward-view endpoint — one round-trip returns the
// full AWS Access tab body the SPA renders. Per backend-as-source-
// of-truth (#188): every Pod→SA→Role→Permission join, service
// grouping, dual-source warning, and pod enrichment is computed in
// Go so MCP / AI tools can wrap this as one tool call.

func iamWorkloadPermissionsHandler(
	reg *clusters.Registry,
	cache *iamEngineCache,
	emitter *audit.Emitter,
) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity, errBackendNotEKSCode,
				"AWS Access is only available for EKS-backed clusters")
			return
		}

		kind := strings.TrimSpace(r.URL.Query().Get("kind"))
		ns := strings.TrimSpace(r.URL.Query().Get("namespace"))
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if kind == "" || name == "" || (kind != k8s.WorkloadKindServiceAccount && ns == "") {
			writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST",
				"kind, namespace, and name are required (namespace optional for kind=ServiceAccount)")
			return
		}
		if !isKnownWorkloadKind(kind) {
			writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST",
				(k8s.ErrUnknownWorkloadKind{Kind: kind}).Error())
			return
		}

		saName, err := k8s.WorkloadSA(r.Context(), p, c, kind, ns, name)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			var uk k8s.ErrUnknownWorkloadKind
			if errors.As(err, &uk) {
				writeAPIErrorJSON(w, http.StatusBadRequest, "E_BAD_REQUEST", err.Error())
				return
			}
			if apierrors.IsNotFound(err) {
				writeAPIErrorJSON(w, http.StatusNotFound, "E_NOT_FOUND",
					fmt.Sprintf("%s %s/%s not found", kind, ns, name))
				return
			}
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeFailure, "workload_permissions_setup", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_K8S_LOOKUP",
				"failed to resolve workload SA: "+err.Error())
			return
		}

		engine, err := cache.For(r.Context(), c)
		if err != nil {
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeFailure, "workload_permissions_setup", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_IAM_SETUP",
				"failed to set up IAM engine: "+err.Error())
			return
		}
		mgr, err := cache.identityC.For(r.Context(), c)
		if err != nil {
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeFailure, "workload_permissions_setup", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_IDENTITY_SETUP",
				"failed to set up identity manager: "+err.Error())
			return
		}

		entries, err := mgr.Ensure(r.Context())
		if err != nil {
			if errors.Is(err, identity.ErrIRSAListerNotReady) {
				w.Header().Set("Retry-After", "3")
				writeAPIErrorJSON(w, http.StatusServiceUnavailable, "E_IDENTITY_WARMING",
					"identity informer is still syncing; retry shortly")
				return
			}
			if errors.Is(err, context.Canceled) {
				return
			}
			emitIAMRead(r.Context(), emitter, c, audit.OutcomeFailure, "workload_permissions", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code, "failed to build SA→Role index: "+err.Error())
			return
		}

		entry := findSAEntry(entries, ns, saName, kind)
		resp := iam.WorkloadPermissionsResponse{
			Cluster:        c.Name,
			Kind:           kind,
			Namespace:      ns,
			Name:           name,
			CatalogVersion: iam.SensitivePermissionsCatalogVersion,
			FetchedAt:      time.Now().UTC(),
			IdentityChain: iam.IdentityChain{
				ServiceAccount: saName,
			},
		}

		var anyPartial bool
		var allPerms []iam.Permission
		var allRaw []iam.RawStatement
		if entry != nil {
			resp.IdentityChain.DualSource = entry.DualSource
			for _, b := range entry.Bindings {
				resp.IdentityChain.Bindings = append(resp.IdentityChain.Bindings, iam.IdentityChainBinding{
					Source:                   string(b.Source),
					RoleArn:                  b.RoleArn,
					RoleExists:               b.RoleExists,
					PodIdentityAssociationId: b.PodIdentityAssociationId,
					IRSAAnnotationValue:      b.IRSAAnnotationValue,
				})
				if !b.RoleExists {
					resp.Warnings = append(resp.Warnings, iam.AwsAccessWarning{
						Code:    iam.WarningRoleNotFound,
						Message: fmt.Sprintf("Role %s referenced by this SA does not exist in IAM (rename, deletion, or partition mismatch).", b.RoleArn),
						RoleArn: b.RoleArn,
					})
					continue
				}
				if b.RoleArn == "" {
					continue
				}
				rp, err := engine.RolePermissions(r.Context(), b.RoleArn)
				if err != nil {
					anyPartial = anyPartial || rp.PolicyFetchPartial
					resp.Warnings = append(resp.Warnings, iam.AwsAccessWarning{
						Code:    iam.WarningPolicyFetchPartial,
						Message: fmt.Sprintf("Partial policy fetch for %s: %s", b.RoleArn, err.Error()),
						RoleArn: b.RoleArn,
					})
					if rp.RoleArn == "" {
						continue
					}
				}
				if rp.PolicyFetchPartial {
					anyPartial = true
				}
				allPerms = append(allPerms, rp.Permissions...)
				allRaw = append(allRaw, rp.RawStatements...)
			}
			if entry.DualSource {
				irsaRole := ""
				for _, b := range entry.Bindings {
					if b.Source == identity.SourceIRSA || b.Source == identity.SourceBoth {
						irsaRole = b.RoleArn
						break
					}
				}
				resp.Warnings = append(resp.Warnings, iam.AwsAccessWarning{
					Code:    iam.WarningDualSourceIRSAShadowed,
					Message: "This ServiceAccount has both IRSA and Pod Identity bindings. Pod Identity wins at runtime; the IRSA annotation is shadowed dead config.",
					RoleArn: irsaRole,
				})
			}
		} else {
			resp.Warnings = append(resp.Warnings, iam.AwsAccessWarning{
				Code:    iam.WarningNoBindings,
				Message: fmt.Sprintf("ServiceAccount %s/%s has no IAM role bindings (IRSA annotation or Pod Identity association).", ns, saName),
			})
		}

		resp.PolicyFetchPartial = anyPartial
		resp.Groups = iam.GroupByService(allPerms)
		resp.RawStatements = allRaw
		resp.TotalCount = len(allPerms)

		// AffectedPods enrichment: for Pod kind, just the pod itself
		// (already exists in the informer cache, but we already know
		// its identity from the request). For controllers / SA, look
		// up via PodsForSA.
		switch kind {
		case k8s.WorkloadKindPod:
			resp.AffectedPods = []iam.PodRef{{Namespace: ns, Name: name}}
			resp.AffectedPodCount = 1
		default:
			refs, total, perr := mgr.PodsForSA(r.Context(), ns, saName, engineConfig(cache).PodRefsLimit)
			if perr == nil {
				resp.AffectedPods = make([]iam.PodRef, 0, len(refs))
				for _, p := range refs {
					resp.AffectedPods = append(resp.AffectedPods, iam.PodRef{
						Namespace: p.Namespace, Name: p.Name, NodeName: p.NodeName,
					})
				}
				resp.AffectedPodCount = total
			} else {
				// Soft-fail: serve permissions without pods rather
				// than 500. The SPA renders "pods not yet ready".
				resp.AffectedPods = []iam.PodRef{}
			}
		}

		emitIAMRead(r.Context(), emitter, c, audit.OutcomeSuccess, "workload_permissions", "")
		writeJSON(w, http.StatusOK, resp)
	}
}

// isKnownWorkloadKind reports whether kind is in the v1.1 AWS
// Access kind set. Validates early so the handler returns 400
// before calling NewClientset (which may otherwise fail with 500
// in misconfigured cluster contexts).
func isKnownWorkloadKind(kind string) bool {
	switch kind {
	case k8s.WorkloadKindPod, k8s.WorkloadKindServiceAccount,
		k8s.WorkloadKindDeployment, k8s.WorkloadKindStatefulSet,
		k8s.WorkloadKindDaemonSet:
		return true
	}
	return false
}

// findSAEntry locates the SARoleIndexEntry for the resolved
// (namespace, sa). For kind=ServiceAccount the namespace may be
// empty in the request — we still want the entry, so fall back to
// SAName-only match when namespace is unset.
func findSAEntry(entries []identity.SARoleIndexEntry, ns, sa, kind string) *identity.SARoleIndexEntry {
	for i := range entries {
		e := &entries[i]
		if e.SAName != sa {
			continue
		}
		if ns == "" && kind == k8s.WorkloadKindServiceAccount {
			return e
		}
		if e.Namespace == ns {
			return e
		}
	}
	return nil
}

// ── /api/identity/sensitive-catalog ───────────────────────────────
//
// Cluster-agnostic. Exposes the locked sensitive-permissions
// catalog (version + entries) so the SPA's chip palette and
// reverse-lookup autocomplete share the server's source of truth.
// No auth wrap: it's a static asset, no AWS / no k8s call.

func identitySensitiveCatalogHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cat := iam.DefaultCatalog()
		rows := cat.Entries()
		out := iam.SensitiveCatalogResponse{
			Version: cat.Version,
			Entries: make([]iam.SensitiveCatalogEntry, 0, len(rows)),
		}
		for _, e := range rows {
			out.Entries = append(out.Entries, iam.SensitiveCatalogEntry{
				Action:   e.Action,
				Category: e.Category,
				Pattern:  e.Pattern,
				ReverseQuery: iam.ReverseQueryHint{
					Action: e.Action,
				},
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// ── /api/clusters/{cluster}/identity/capabilities ────────────────
//
// Per-feature probe driving the SPA's locked-pane (#188 paywall
// UX). Same response also tells an MCP tool whether to even
// surface a given action.
//
// Cached per (cluster, actor) for 5 min. Re-check button on the
// locked-pane sends `Cache-Control: no-cache` to bypass.

func identityCapabilitiesHandler(
	reg *clusters.Registry,
	cache *iamEngineCache,
	probeCache *capabilitiesCache,
	awsCfg awsAccessConfig,
	emitter *audit.Emitter,
) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}

		bypassCache := strings.Contains(strings.ToLower(r.Header.Get("Cache-Control")), "no-cache")
		cacheKey := capabilitiesCacheKey{cluster: c.Name, actor: p.Actor()}

		if !bypassCache {
			if cached, ok := probeCache.get(cacheKey); ok {
				emitIAMRead(r.Context(), emitter, c, audit.OutcomeSuccess, "capabilities:cache_hit", "")
				w.Header().Set("X-Capabilities-Cache", "hit")
				writeJSON(w, http.StatusOK, cached)
				return
			}
		}

		resp := probeCapabilities(r.Context(), p, c, cache, awsCfg)
		probeCache.put(cacheKey, resp)
		emitIAMRead(r.Context(), emitter, c, audit.OutcomeSuccess, "capabilities", "")
		if bypassCache {
			w.Header().Set("X-Capabilities-Cache", "bypass")
		} else {
			w.Header().Set("X-Capabilities-Cache", "miss")
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func probeCapabilities(
	ctx context.Context,
	p credentials.Provider,
	c clusters.Cluster,
	cache *iamEngineCache,
	awsCfg awsAccessConfig,
) iam.CapabilitiesResponse {
	resp := iam.CapabilitiesResponse{
		Cluster:   c.Name,
		Features:  map[string]iam.FeatureCapability{},
		FetchedAt: time.Now().UTC(),
	}

	// Sensitive catalog is always available — it's static, no
	// AWS / no k8s. The Available=true entry keeps the SPA
	// uniform (always read features[FeatureSensitiveCatalog]).
	resp.Features[iam.FeatureSensitiveCatalog] = iam.FeatureCapability{Available: true}

	if !c.EKSCapable() {
		notEks := iam.FeatureCapability{
			Available: false,
			Reason:    iam.ReasonNotEKS,
			Message:   "This cluster is not backed by EKS; AWS Access surfaces are EKS-only.",
			DocsURL:   awsAccessDocsURL,
		}
		resp.Features[iam.FeatureAwsAccessTab] = notEks
		resp.Features[iam.FeatureReverseLookup] = notEks
		return resp
	}

	// RBAC probes: cluster-scoped SAR for both pods and SAs so the
	// reverse-lookup page (which iterates across all namespaces)
	// works. The forward view needs the same reads on the
	// requested namespace; we treat the cluster-scoped probe as
	// sufficient because Periscope itself runs cluster-wide.
	saAllowed, saReason, saErr := k8s.CheckSAR(ctx, p, c, authv1.ResourceAttributes{
		Verb: "get", Group: "", Resource: "serviceaccounts",
	})
	podAllowed, podReason, podErr := k8s.CheckSAR(ctx, p, c, authv1.ResourceAttributes{
		Verb: "list", Group: "", Resource: "pods",
	})
	if saErr != nil || podErr != nil {
		first := saErr
		if first == nil {
			first = podErr
		}
		rbacFail := iam.FeatureCapability{
			Available: false,
			Reason:    iam.ReasonRBACDenied,
			Message:   "Couldn't probe RBAC permissions: " + first.Error(),
			DocsURL:   awsAccessDocsURL,
		}
		resp.Features[iam.FeatureAwsAccessTab] = rbacFail
		resp.Features[iam.FeatureReverseLookup] = rbacFail
		return resp
	}

	missingRBAC := []string{}
	rbacMessages := []string{}
	if !saAllowed {
		missingRBAC = append(missingRBAC, "get serviceaccounts (cluster-wide)")
		if saReason != "" {
			rbacMessages = append(rbacMessages, "serviceaccounts: "+saReason)
		}
	}
	if !podAllowed {
		missingRBAC = append(missingRBAC, "list pods (cluster-wide)")
		if podReason != "" {
			rbacMessages = append(rbacMessages, "pods: "+podReason)
		}
	}
	if len(missingRBAC) > 0 {
		denied := iam.FeatureCapability{
			Available: false,
			Reason:    iam.ReasonRBACDenied,
			Message:   "Your Kubernetes role lacks reads required to resolve workloads and pods. " + strings.Join(rbacMessages, "; "),
			Missing:   missingRBAC,
			DocsURL:   awsAccessDocsURL,
		}
		resp.Features[iam.FeatureAwsAccessTab] = denied
		resp.Features[iam.FeatureReverseLookup] = denied
		return resp
	}

	// IAM-perms probe (configurable). When enabled (default),
	// resolve periscope-server's own caller ARN and call
	// iam:SimulatePrincipalPolicy against the five v1.1 IAM read
	// perms to populate Missing[] with the exact denied actions.
	// When disabled, return optimistic Available=true with a clear
	// note so the SPA renders the tab and first real call surfaces
	// any 403.
	tab := iam.FeatureCapability{Available: true, DocsURL: awsAccessDocsURL}
	rev := iam.FeatureCapability{Available: true, DocsURL: awsAccessDocsURL}
	if !awsCfg.IAMProbe {
		tab.Reason = iam.ReasonIAMProbeDisabled
		tab.Note = "IAM permission probe disabled by PERISCOPE_AWS_ACCESS_IAM_PROBE=false; first call surfaces missing perms via 403."
		rev.Reason = tab.Reason
		rev.Note = tab.Note
	} else {
		client := newIdentityClient(cache.awsCfg, c)
		missing, probeErr := probeIAMPermissions(ctx, client, iamProbeActions)
		switch {
		case probeErr != nil:
			// Probe itself failed (likely the role lacks
			// iam:SimulatePrincipalPolicy or sts:GetCallerIdentity).
			// Fall back to optimistic Available=true with a Note —
			// the SPA still renders the tab; the first real call
			// surfaces any underlying 403.
			tab.Note = "IAM permission probe couldn't run (" + probeErr.Error() + "). Add iam:SimulatePrincipalPolicy to periscope-server's role to surface the exact missing-perms list."
			rev.Note = tab.Note
		case len(missing) > 0:
			locked := iam.FeatureCapability{
				Available: false,
				Reason:    iam.ReasonMissingIAMPerms,
				Message:   "Periscope's AWS role is missing IAM permissions required to resolve role policies.",
				Missing:   missing,
				DocsURL:   awsAccessDocsURL,
			}
			tab = locked
			rev = locked
		}
	}

	// Identity-configured check: if the SA→Role index is empty,
	// inform the user. Available stays true because the
	// reverse-lookup page is still useful (empty results are
	// information). The forward tab gets a softer note.
	if mgr, err := cache.identityC.For(ctx, c); err == nil {
		entries, err := mgr.Ensure(ctx)
		if err == nil && len(entries) == 0 {
			tab.Note = appendNote(tab.Note, "No IRSA annotations or Pod Identity associations were found in this cluster.")
		} else if errors.Is(err, identity.ErrIRSAListerNotReady) {
			tab.Available = false
			tab.Reason = iam.ReasonInformerWarming
			tab.Message = "ServiceAccount informer is still syncing; retry in a few seconds."
			rev.Available = false
			rev.Reason = iam.ReasonInformerWarming
			rev.Message = tab.Message
		}
	}

	resp.Features[iam.FeatureAwsAccessTab] = tab
	resp.Features[iam.FeatureReverseLookup] = rev
	return resp
}

func appendNote(existing, addition string) string {
	if existing == "" {
		return addition
	}
	return existing + " " + addition
}

// iamProbeActions is the v1.1 IAM read set the AWS Access surface
// needs end-to-end. SimulatePrincipalPolicy is called once per
// capabilities probe with this list as ActionNames; denied
// actions surface as the Missing[] field on the locked-pane.
//
// iam:GetRole is the existence probe from #178 — included here so
// the AWS Access surface lights up "missing" if it's stripped (a
// common downgrade footgun: operators trim "AWS Identity" perms
// thinking they're not needed once #178 is live).
//
// sts:GetCallerIdentity is intentionally not in this list — AWS
// grants it to every authenticated principal by default, and
// resolving the caller ARN is the first step of the probe itself.
// If it's denied, probeIAMPermissions returns an error that the
// handler maps to optimistic-mode with a Note.
var iamProbeActions = []string{
	"iam:GetRole",
	"iam:ListRolePolicies",
	"iam:GetRolePolicy",
	"iam:ListAttachedRolePolicies",
	"iam:GetPolicy",
	"iam:GetPolicyVersion",
}

// callerArnCache is process-wide because periscope-server runs as
// one identity; sts:GetCallerIdentity returns the same ARN
// regardless of the request's per-cluster aws.Config. Resolved on
// first probe and reused thereafter.
//
// Reset on process restart; not invalidated by Re-check (the user-
// facing probe-cache bypass) because the caller ARN doesn't change
// without a re-deploy.
var (
	callerArnMu  sync.Mutex
	callerArnVal string
	callerArnErr error
	callerArnSet bool
)

// resolveCallerArn returns periscope-server's principal ARN in IAM
// role form, with sticky memoization. The first caller pays the
// sts:GetCallerIdentity round-trip; subsequent calls (across all
// clusters, all users) reuse the cached value.
//
// Exposed as a var-function so tests can reset between cases by
// calling resetCallerArnCache().
var resolveCallerArn = func(ctx context.Context, client *identity.Client) (string, error) {
	callerArnMu.Lock()
	defer callerArnMu.Unlock()
	if callerArnSet {
		return callerArnVal, callerArnErr
	}
	arn, err := client.CallerIdentity(ctx)
	callerArnVal = arn
	callerArnErr = err
	callerArnSet = true
	return arn, err
}

// resetCallerArnCache wipes the memoized caller ARN. Test-only;
// keep the symbol unexported.
func resetCallerArnCache() {
	callerArnMu.Lock()
	defer callerArnMu.Unlock()
	callerArnVal = ""
	callerArnErr = nil
	callerArnSet = false
}

// probeIAMPermissions resolves the server's caller ARN and runs
// iam:SimulatePrincipalPolicy for actions. Returns the subset of
// actions denied by the simulator. Errors propagate when STS or
// SimulatePrincipalPolicy itself is denied — caller maps to
// optimistic mode.
func probeIAMPermissions(ctx context.Context, client *identity.Client, actions []string) ([]string, error) {
	arn, err := resolveCallerArn(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("resolve caller ARN: %w", err)
	}
	if arn == "" {
		return nil, fmt.Errorf("resolve caller ARN: empty result")
	}
	return client.SimulateActions(ctx, arn, actions, "*")
}

// ── Capabilities cache ───────────────────────────────────────────

type capabilitiesCacheKey struct {
	cluster string
	actor   string
}

type capabilitiesCacheEntry struct {
	resp      iam.CapabilitiesResponse
	cachedAt  time.Time
}

// capabilitiesCache holds per (cluster, actor) probe snapshots for
// capabilitiesCacheTTL. Stored separately from iamEngineCache to
// keep the IAM engine's per-role policy cache and this user-facing
// probe cache decoupled — they have different TTLs and different
// invalidation triggers.
type capabilitiesCache struct {
	mu      sync.Mutex
	entries map[capabilitiesCacheKey]capabilitiesCacheEntry
	clock   func() time.Time
}

func newCapabilitiesCache() *capabilitiesCache {
	return &capabilitiesCache{
		entries: map[capabilitiesCacheKey]capabilitiesCacheEntry{},
		clock:   time.Now,
	}
}

func (c *capabilitiesCache) get(k capabilitiesCacheKey) (iam.CapabilitiesResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok {
		return iam.CapabilitiesResponse{}, false
	}
	if c.clock().Sub(e.cachedAt) > capabilitiesCacheTTL {
		delete(c.entries, k)
		return iam.CapabilitiesResponse{}, false
	}
	return e.resp, true
}

func (c *capabilitiesCache) put(k capabilitiesCacheKey, resp iam.CapabilitiesResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[k] = capabilitiesCacheEntry{resp: resp, cachedAt: c.clock()}
}
