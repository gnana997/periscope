package main

// eks_addons_handler.go — read-only EKS managed add-on endpoints.
//
//   GET /api/clusters/{cluster}/eks/addons
//   GET /api/clusters/{cluster}/eks/addons/{name}
//
// Pairs with the Upgrade Insights surface (eks_insights_handler.go):
// Insights tells operators "vpc-cni must be ≥1.18 before 1.30" and
// this surface tells them what version is actually installed plus
// whether it blocks the upcoming K8s minor. Same EKS-only contract,
// audit shape, error envelope, and IAM-hits-403 pattern as the
// existing read-only EKS endpoints.
//
// Two-tier cache:
//   - eksAddonsCache (1h, per-cluster) covers ListAddons +
//     DescribeAddon + DescribeCluster + DescribeAddonConfiguration.
//   - addonVersionsCache (6h, keyed by (addonName, k8sVersion))
//     covers the AWS-published DescribeAddonVersions catalog. A
//     fleet of N clusters running coredns hits AWS once per addon-
//     name per 6h, not N times.
//
// Parallel fan-out: DescribeAddon is per-name and unbounded by AWS
// SDK's connection-pool limits in practice. Mirrors the WaitGroup
// pattern in eks_nodegroups_handler.go's drift fold-in — disjoint
// summary slots, per-row context timeout, no errgroup dep.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// eksAddonsListMaxResults caps the AWS-side page size. EKS clusters
// have at most a handful of add-ons (vpc-cni, coredns, kube-proxy,
// CSI drivers, observability bundles) so 100 is comfortably above
// every realistic cluster. A NextToken response logs a truncation
// warning rather than silently fetching every page.
const eksAddonsListMaxResults = 100

// addonDescribeTimeout caps a single per-addon DescribeAddon /
// DescribeAddonVersions call inside the parallel fan-out. A
// misbehaving region must not stall the whole list response.
const addonDescribeTimeout = 4 * time.Second

// eksAddonsAPI is the SDK seam for testability. The real client
// returned by eks.NewFromConfig satisfies this implicitly.
//
// CreateAddon is grouped here (rather than a separate write seam)
// because all add-on handlers share the same eks.Client — the
// fakeEKSAddonsClient harness in tests stubs the same interface.
type eksAddonsAPI interface {
	ListAddons(ctx context.Context, in *eks.ListAddonsInput, opts ...func(*eks.Options)) (*eks.ListAddonsOutput, error)
	DescribeAddon(ctx context.Context, in *eks.DescribeAddonInput, opts ...func(*eks.Options)) (*eks.DescribeAddonOutput, error)
	DescribeAddonVersions(ctx context.Context, in *eks.DescribeAddonVersionsInput, opts ...func(*eks.Options)) (*eks.DescribeAddonVersionsOutput, error)
	DescribeAddonConfiguration(ctx context.Context, in *eks.DescribeAddonConfigurationInput, opts ...func(*eks.Options)) (*eks.DescribeAddonConfigurationOutput, error)
	DescribeCluster(ctx context.Context, in *eks.DescribeClusterInput, opts ...func(*eks.Options)) (*eks.DescribeClusterOutput, error)
	CreateAddon(ctx context.Context, in *eks.CreateAddonInput, opts ...func(*eks.Options)) (*eks.CreateAddonOutput, error)
}

var newEKSAddonsClient = defaultNewEKSAddonsClient

func defaultNewEKSAddonsClient(p credentials.Provider, c clusters.Cluster) eksAddonsAPI {
	return eks.NewFromConfig(aws.Config{
		Region:      c.Region,
		Credentials: p,
	})
}

// ── Wire types ───────────────────────────────────────────────────────

// AddonHealthIssue mirrors the AWS shape verbatim for the SPA's
// error panel.
type AddonHealthIssue struct {
	Code        string   `json:"code,omitempty"`
	Message     string   `json:"message,omitempty"`
	ResourceIDs []string `json:"resourceIds,omitempty"`
}

// AddonSummary is one row in the list response — and the embedded
// base of AddonDetail so the detail endpoint returns a strict
// superset of fields.
type AddonSummary struct {
	Name              string     `json:"name"`
	Status            string     `json:"status"`
	Version           string     `json:"version,omitempty"`
	KubernetesVersion string     `json:"kubernetesVersion,omitempty"`
	HealthIssueCount  int        `json:"healthIssueCount"`
	// HealthGlyph is the SPA's three-state symbol:
	//   "ok"     → ●  healthy & current
	//   "update" → ▲  healthy but newer version exists or blocks next minor
	//   "fail"   → ✕  health issues present
	HealthGlyph     string `json:"healthGlyph"`
	UpdateAvailable bool   `json:"updateAvailable"`
	LatestVersion   string `json:"latestVersion,omitempty"`
	// CompatMinK8s / CompatMaxK8s describe the cluster K8s versions
	// the *installed* add-on version supports, derived from the
	// addon's own Compatibilities list. The SPA renders them as
	// "compat: 1.27 – 1.29". Empty when the catalog had nothing to
	// say (the addon name is custom or the lookup failed soft).
	CompatMinK8s string `json:"compatMinK8s,omitempty"`
	CompatMaxK8s string `json:"compatMaxK8s,omitempty"`
	// BlocksNextMinor is true when the installed version's compat
	// list does NOT contain (cluster.k8s + 1). The SPA upgrades the
	// glyph to ▲ + a "blocks 1.30" subtitle on the row.
	BlocksNextMinor bool       `json:"blocksNextMinor"`
	CreatedAt       *time.Time `json:"createdAt,omitempty"`
	ModifiedAt      *time.Time `json:"modifiedAt,omitempty"`
}

// AddonsCounts is the bucket header the SPA renders on the overview
// card and the tab. Server-computed for consistency across browsers
// and tabs.
type AddonsCounts struct {
	Total           int `json:"total"`
	Healthy         int `json:"healthy"`
	UpdateAvailable int `json:"updateAvailable"`
	Unhealthy       int `json:"unhealthy"`
	BlocksNextMinor int `json:"blocksNextMinor"`
}

// AddonsListResponse is the GET /eks/addons payload.
type AddonsListResponse struct {
	Addons []AddonSummary `json:"addons"`
	Counts AddonsCounts   `json:"counts"`
	// ClusterKubernetesVersion is the cluster's K8s version as AWS
	// reports it (DescribeCluster.Version, e.g. "1.29"). The SPA uses
	// it to label the table header — "EKS add-ons · prod-eu-west-1
	// (k8s 1.29)" matches the issue mockup.
	ClusterKubernetesVersion string `json:"clusterKubernetesVersion,omitempty"`
}

// AddonVersionEntry is one entry in the addon's catalog version
// list, surfaced verbatim on the detail blob so the SPA can render
// the version-history table without another AWS round-trip.
type AddonVersionEntry struct {
	Version              string   `json:"version"`
	CompatibleK8sVersions []string `json:"compatibleK8sVersions"`
	DefaultVersion       bool     `json:"defaultVersion"`
}

// AddonDetail is the GET /eks/addons/{name} payload.
type AddonDetail struct {
	AddonSummary
	ARN                   string              `json:"arn,omitempty"`
	ServiceAccountRoleARN string              `json:"serviceAccountRoleArn,omitempty"`
	ConfigurationValues   string              `json:"configurationValues,omitempty"`
	// ConfigurationSchema is the JSON schema AWS publishes for the
	// addon's config. Best-effort — if DescribeAddonConfiguration
	// fails (e.g. AccessDenied because the addon-resource scope
	// doesn't cover it), we log + leave this empty so the operator
	// still sees the version + health blob.
	ConfigurationSchema string              `json:"configurationSchema,omitempty"`
	HealthIssues        []AddonHealthIssue  `json:"healthIssues,omitempty"`
	AvailableVersions   []AddonVersionEntry `json:"availableVersions,omitempty"`
	// Owner / Publisher are surfaced for the SPA's "marketplace vs
	// AWS-managed" disambiguation.
	Owner     string `json:"owner,omitempty"`
	Publisher string `json:"publisher,omitempty"`
}

// ── Handlers ─────────────────────────────────────────────────────────

func eksAddonsListHandler(reg *clusters.Registry, cache *eksAddonsCache, versions *addonVersionsCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"add-ons are only available for EKS-backed clusters")
			return
		}

		if cached, ok := cache.GetList(c.Name); ok {
			writeJSON(w, http.StatusOK, *cached)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "list:cache_hit", "")
			return
		}

		client := newEKSAddonsClient(p, c)
		eksName := c.EKSName()

		// Step 1: cluster K8s version. Drives the BlocksNextMinor
		// computation and the addon-versions catalog filter.
		clusterVer, err := describeClusterVersion(r.Context(), client, eksName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks describe cluster failed", "cluster", c.Name, "err", err)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "list", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to describe cluster: "+err.Error())
			return
		}

		// Step 2: list installed addon names.
		names, err := listAllAddonNames(r.Context(), client, eksName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks addons list failed", "cluster", c.Name, "err", err)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "list", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to list add-ons: "+err.Error())
			return
		}

		// Step 3: parallel fan-out. Each goroutine writes to a
		// disjoint &summaries[i] slot — no race window.
		summaries := make([]AddonSummary, len(names))
		var wg sync.WaitGroup
		for i, name := range names {
			wg.Add(1)
			go func(i int, name string) {
				defer wg.Done()
				summaries[i] = describeAndAnnotate(r.Context(), client, versions, eksName, name, clusterVer)
			}(i, name)
		}
		wg.Wait()

		// Sort: unhealthy first (operator clicks them now), then
		// blocks-next-minor (the upgrade-readiness gates), then
		// update-available, then alphabetical. The SPA renders the
		// order verbatim.
		sort.SliceStable(summaries, func(i, j int) bool {
			a, b := summaries[i], summaries[j]
			if (a.HealthGlyph == "fail") != (b.HealthGlyph == "fail") {
				return a.HealthGlyph == "fail"
			}
			if a.BlocksNextMinor != b.BlocksNextMinor {
				return a.BlocksNextMinor
			}
			if a.UpdateAvailable != b.UpdateAvailable {
				return a.UpdateAvailable
			}
			return a.Name < b.Name
		})

		resp := AddonsListResponse{
			Addons:                   summaries,
			Counts:                   buildAddonsCounts(summaries),
			ClusterKubernetesVersion: clusterVer,
		}
		cache.PutList(c.Name, resp)
		writeJSON(w, http.StatusOK, resp)
		emitAddonsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "list", "")
	}
}

func eksAddonsGetHandler(reg *clusters.Registry, cache *eksAddonsCache, versions *addonVersionsCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"add-ons are only available for EKS-backed clusters")
			return
		}
		name := chi.URLParam(r, "name")
		if name == "" {
			http.Error(w, "missing add-on name", http.StatusBadRequest)
			return
		}

		if cached, ok := cache.GetDetail(c.Name, name); ok {
			writeJSON(w, http.StatusOK, *cached)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "detail:cache_hit", "")
			return
		}

		client := newEKSAddonsClient(p, c)
		eksName := c.EKSName()

		// Cluster K8s version drives the catalog filter + BlocksNextMinor.
		clusterVer, err := describeClusterVersion(r.Context(), client, eksName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks describe cluster failed (detail)", "cluster", c.Name, "err", err)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "detail", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to describe cluster: "+err.Error())
			return
		}

		out, err := client.DescribeAddon(r.Context(), &eks.DescribeAddonInput{
			ClusterName: &eksName,
			AddonName:   &name,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks describe addon failed",
				"cluster", c.Name, "addon", name, "err", err)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "detail", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to describe add-on: "+err.Error())
			return
		}
		if out.Addon == nil {
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "detail", "empty response")
			writeAPIErrorJSON(w, http.StatusBadGateway, "E_AWS_API",
				"DescribeAddon returned empty response")
			return
		}

		summary := buildAddonSummary(out.Addon)
		catalog := lookupAddonVersions(r.Context(), client, versions, name, clusterVer)
		annotateFromCatalog(&summary, catalog, clusterVer)

		detail := AddonDetail{
			AddonSummary:          summary,
			ARN:                   deref(out.Addon.AddonArn),
			ServiceAccountRoleARN: deref(out.Addon.ServiceAccountRoleArn),
			ConfigurationValues:   deref(out.Addon.ConfigurationValues),
			Owner:                 deref(out.Addon.Owner),
			Publisher:             deref(out.Addon.Publisher),
		}
		if out.Addon.Health != nil {
			for _, issue := range out.Addon.Health.Issues {
				detail.HealthIssues = append(detail.HealthIssues, AddonHealthIssue{
					Code:        string(issue.Code),
					Message:     deref(issue.Message),
					ResourceIDs: issue.ResourceIds,
				})
			}
		}
		if catalog != nil {
			detail.AvailableVersions = catalog.Versions
		}

		// Best-effort schema fetch. AccessDenied here is non-fatal —
		// the addon-resource ARN scope doesn't include the catalog
		// surface, and the SPA degrades gracefully without the schema.
		if summary.Version != "" {
			schemaCtx, cancel := context.WithTimeout(r.Context(), addonDescribeTimeout)
			schemaOut, schemaErr := client.DescribeAddonConfiguration(schemaCtx, &eks.DescribeAddonConfigurationInput{
				AddonName:    &name,
				AddonVersion: &summary.Version,
			})
			cancel()
			if schemaErr != nil {
				slog.Debug("eks describe addon configuration soft-fail",
					"cluster", c.Name, "addon", name, "err", schemaErr)
			} else if schemaOut != nil {
				detail.ConfigurationSchema = deref(schemaOut.ConfigurationSchema)
			}
		}

		cache.PutDetail(c.Name, name, detail)
		writeJSON(w, http.StatusOK, detail)
		emitAddonsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "detail", "")
	}
}

// ── SDK → wire mapping ───────────────────────────────────────────────

func listAllAddonNames(ctx context.Context, client eksAddonsAPI, clusterName string) ([]string, error) {
	out, err := client.ListAddons(ctx, &eks.ListAddonsInput{
		ClusterName: &clusterName,
		MaxResults:  int32Ptr(eksAddonsListMaxResults),
	})
	if err != nil {
		return nil, err
	}
	if out.NextToken != nil && *out.NextToken != "" {
		// Same truncation policy as eks_insights_handler.go: log and
		// return what we got. >100 add-ons in one cluster is outside
		// the realistic operator scenario.
		slog.Warn("eks addons list truncated; not paginating",
			"cluster", clusterName, "page_size", eksAddonsListMaxResults)
	}
	return out.Addons, nil
}

func describeClusterVersion(ctx context.Context, client eksAddonsAPI, clusterName string) (string, error) {
	out, err := client.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: &clusterName,
	})
	if err != nil {
		return "", err
	}
	if out.Cluster == nil {
		return "", nil
	}
	return deref(out.Cluster.Version), nil
}

// describeAndAnnotate is the per-addon body of the parallel fan-out.
// On per-row error, returns a placeholder summary so the SPA can show
// "this addon exists but its details are unavailable" instead of
// dropping the row. Mirrors eks_nodegroups_handler.go's per-describe
// soft-fail behavior.
func describeAndAnnotate(parent context.Context, client eksAddonsAPI, versions *addonVersionsCache, clusterName, addonName, clusterVer string) AddonSummary {
	ctx, cancel := context.WithTimeout(parent, addonDescribeTimeout)
	defer cancel()
	out, err := client.DescribeAddon(ctx, &eks.DescribeAddonInput{
		ClusterName: &clusterName,
		AddonName:   &addonName,
	})
	if err != nil || out == nil || out.Addon == nil {
		if err != nil {
			slog.Warn("eks describe addon failed (list step)",
				"cluster", clusterName, "addon", addonName, "err", err)
		}
		return AddonSummary{
			Name:        addonName,
			Status:      "DEGRADED_DESCRIBE",
			HealthGlyph: "fail",
		}
	}
	summary := buildAddonSummary(out.Addon)
	catalog := lookupAddonVersions(parent, client, versions, addonName, clusterVer)
	annotateFromCatalog(&summary, catalog, clusterVer)
	return summary
}

func buildAddonSummary(in *ekstypes.Addon) AddonSummary {
	out := AddonSummary{
		Name:       deref(in.AddonName),
		Status:     string(in.Status),
		Version:    deref(in.AddonVersion),
		CreatedAt:  in.CreatedAt,
		ModifiedAt: in.ModifiedAt,
	}
	if in.Health != nil {
		out.HealthIssueCount = len(in.Health.Issues)
	}
	out.HealthGlyph = computeHealthGlyph(out.Status, out.HealthIssueCount, false, false)
	return out
}

// lookupAddonVersions wraps the shared catalog cache. Cache miss
// invokes DescribeAddonVersions filtered by (addonName, k8sVersion);
// errors are sticky-cached for 6h to prevent retry storms.
func lookupAddonVersions(parent context.Context, client eksAddonsAPI, cache *addonVersionsCache, addonName, k8sVersion string) *addonVersionsCacheValue {
	if cache == nil || addonName == "" {
		return nil
	}
	if cached, err, hit := cache.Get(addonName, k8sVersion); hit {
		if err != nil {
			return nil
		}
		return cached
	}
	ctx, cancel := context.WithTimeout(parent, addonDescribeTimeout)
	defer cancel()
	in := &eks.DescribeAddonVersionsInput{
		AddonName:  &addonName,
		MaxResults: int32Ptr(eksAddonsListMaxResults),
	}
	if k8sVersion != "" {
		in.KubernetesVersion = &k8sVersion
	}
	out, err := client.DescribeAddonVersions(ctx, in)
	if err != nil {
		slog.Warn("eks describe addon versions failed (cached as sticky)",
			"addon", addonName, "k8s", k8sVersion, "err", err)
		cache.Put(addonName, k8sVersion, nil, err)
		return nil
	}
	val := buildAddonVersionsValue(out, k8sVersion)
	cache.Put(addonName, k8sVersion, val, nil)
	return val
}

// buildAddonVersionsValue flattens the AWS catalog response into the
// cache-friendly shape. The "latest" pick is the first entry whose
// compatibilities include the queried k8sVersion — AWS returns
// versions newest-first, so first-match is latest-compatible.
func buildAddonVersionsValue(out *eks.DescribeAddonVersionsOutput, k8sVersion string) *addonVersionsCacheValue {
	if out == nil || len(out.Addons) == 0 {
		return &addonVersionsCacheValue{}
	}
	val := &addonVersionsCacheValue{}
	// AWS's response shape: Addons is one AddonInfo per matching
	// addonName; AddonVersions inside is the version list. With our
	// AddonName filter Addons is length 1.
	for _, info := range out.Addons {
		for _, v := range info.AddonVersions {
			versionStr := deref(v.AddonVersion)
			if versionStr == "" {
				continue
			}
			compatVers := make([]string, 0, len(v.Compatibilities))
			isDefault := false
			compatibleWithQuery := false
			for _, compat := range v.Compatibilities {
				cv := deref(compat.ClusterVersion)
				if cv != "" {
					compatVers = append(compatVers, cv)
				}
				if compat.DefaultVersion {
					isDefault = true
				}
				if k8sVersion != "" && cv == k8sVersion {
					compatibleWithQuery = true
				}
			}
			val.Versions = append(val.Versions, AddonVersionEntry{
				Version:               versionStr,
				CompatibleK8sVersions: compatVers,
				DefaultVersion:        isDefault,
			})
			// First version compatible with the queried k8sVersion is
			// "latest compatible" — preserves AWS's ordering. When
			// k8sVersion is empty, the first AWS-returned version
			// wins.
			if val.Latest == "" && (k8sVersion == "" || compatibleWithQuery) {
				val.Latest = versionStr
			}
			if isDefault && val.DefaultVersion == "" && (k8sVersion == "" || compatibleWithQuery) {
				val.DefaultVersion = versionStr
			}
		}
	}
	return val
}

// annotateFromCatalog folds catalog-derived fields onto a summary in
// place. UpdateAvailable / LatestVersion / CompatMin/Max /
// BlocksNextMinor + glyph are all driven from the catalog lookup.
// catalog == nil is the soft-fail case (cache returned a sticky
// error or the lookup itself failed); leave catalog fields zero,
// keep the summary's basic health-derived glyph intact.
func annotateFromCatalog(s *AddonSummary, catalog *addonVersionsCacheValue, clusterVer string) {
	if catalog == nil {
		// catalog-soft-fail path: BlocksNextMinor stays false, glyph
		// stays whatever buildAddonSummary computed from health.
		return
	}
	s.LatestVersion = catalog.Latest
	if s.LatestVersion != "" && s.Version != "" && s.LatestVersion != s.Version {
		s.UpdateAvailable = true
	}
	// Compat range derived from the *installed* version's entry in
	// the catalog. If the installed version isn't in the catalog
	// (custom build, hand-installed), we leave compat empty rather
	// than guessing.
	if installed := findVersionEntry(catalog.Versions, s.Version); installed != nil {
		s.CompatMinK8s, s.CompatMaxK8s = compatRange(installed.CompatibleK8sVersions)
		s.KubernetesVersion = clusterVer
		if next := nextMinor(clusterVer); next != "" {
			s.BlocksNextMinor = !containsString(installed.CompatibleK8sVersions, next)
		}
	}
	s.HealthGlyph = computeHealthGlyph(s.Status, s.HealthIssueCount, s.UpdateAvailable, s.BlocksNextMinor)
}

func buildAddonsCounts(summaries []AddonSummary) AddonsCounts {
	c := AddonsCounts{Total: len(summaries)}
	for _, s := range summaries {
		switch s.HealthGlyph {
		case "ok":
			c.Healthy++
		case "update":
			c.UpdateAvailable++
		case "fail":
			c.Unhealthy++
		}
		if s.BlocksNextMinor {
			c.BlocksNextMinor++
		}
	}
	return c
}

// ── Helpers ──────────────────────────────────────────────────────────

// computeHealthGlyph collapses (status, healthIssues, updateAvailable,
// blocksNextMinor) into one of three SPA-rendered symbols. Failure
// dominates update-available; both dominate ok. The set of failure
// statuses matches the issue's UX spec ("DEGRADED, CREATE_FAILED,
// DELETE_FAILED").
func computeHealthGlyph(status string, healthIssues int, updateAvailable, blocksNextMinor bool) string {
	if healthIssues > 0 {
		return "fail"
	}
	switch status {
	case string(ekstypes.AddonStatusDegraded),
		string(ekstypes.AddonStatusCreateFailed),
		string(ekstypes.AddonStatusDeleteFailed),
		string(ekstypes.AddonStatusUpdateFailed),
		"DEGRADED_DESCRIBE":
		return "fail"
	}
	if updateAvailable || blocksNextMinor {
		return "update"
	}
	return "ok"
}

// findVersionEntry returns the catalog entry matching the installed
// version string, or nil if the installed version isn't in the
// catalog (e.g. operator manually installed a custom build).
func findVersionEntry(versions []AddonVersionEntry, installed string) *AddonVersionEntry {
	if installed == "" {
		return nil
	}
	for i := range versions {
		if versions[i].Version == installed {
			return &versions[i]
		}
	}
	return nil
}

// compatRange returns the (min, max) of a list of K8s minor strings
// like ["1.27", "1.28", "1.29"]. Empty input yields ("", ""). Uses
// numeric minor ordering — "1.10" sorts above "1.9" — so the
// comparison is stable across the EKS minor numbering bumps to come.
func compatRange(versions []string) (string, string) {
	if len(versions) == 0 {
		return "", ""
	}
	type parsed struct {
		raw   string
		major int
		minor int
	}
	out := make([]parsed, 0, len(versions))
	for _, v := range versions {
		maj, min, ok := parseK8sMinor(v)
		if !ok {
			continue
		}
		out = append(out, parsed{raw: v, major: maj, minor: min})
	}
	if len(out) == 0 {
		return "", ""
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].major != out[j].major {
			return out[i].major < out[j].major
		}
		return out[i].minor < out[j].minor
	})
	return out[0].raw, out[len(out)-1].raw
}

// nextMinor returns "1.30" for "1.29", or "" if the input doesn't
// look like a Kubernetes minor version. Used to compute
// BlocksNextMinor against the addon's compat list.
func nextMinor(v string) string {
	maj, min, ok := parseK8sMinor(v)
	if !ok {
		return ""
	}
	return formatK8sMinor(maj, min+1)
}

// parseK8sMinor splits "1.29" into (1, 29). Tolerates trailing
// noise after a "+" (e.g. "1.29.1+eksbuild.1") and returns (0, 0,
// false) for shapes that don't fit.
func parseK8sMinor(v string) (int, int, bool) {
	if v == "" {
		return 0, 0, false
	}
	dot := -1
	for i := 0; i < len(v); i++ {
		if v[i] == '.' {
			dot = i
			break
		}
	}
	if dot <= 0 || dot == len(v)-1 {
		return 0, 0, false
	}
	major, ok := atoi(v[:dot])
	if !ok {
		return 0, 0, false
	}
	rest := v[dot+1:]
	end := len(rest)
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			end = i
			break
		}
	}
	if end == 0 {
		return 0, 0, false
	}
	minor, ok := atoi(rest[:end])
	if !ok {
		return 0, 0, false
	}
	return major, minor, true
}

func formatK8sMinor(major, minor int) string {
	return itoa(major) + "." + itoa(minor)
}

func atoi(s string) (int, bool) {
	if len(s) == 0 {
		return 0, false
	}
	n := 0
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + int(ch-'0')
	}
	return n, true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func emitAddonsRead(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, outcome audit.Outcome, op, reason string) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbEKSAddonsRead,
		Outcome: outcome,
		Cluster: c.Name,
		Reason:  reason,
		Extra: map[string]any{
			"op": op,
		},
	})
}

