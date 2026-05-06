package main

// eks_addon_catalog_handler.go — read-only catalog of every AWS-
// published add-on available on the cluster's K8s version (issue
// #119, PR-1).
//
//   GET /api/clusters/{cluster}/eks/addons/catalog
//
// Pairs with #117's installed-addons surface (eks_addons_handler.go):
// the catalog answers "what could I install on this cluster?" while
// #117 answers "what's installed and is it stale?". A future PR-2
// adds the install action; PR-3 adds upgrade + delete.
//
// Single AWS call (DescribeAddonVersions with no AddonName filter)
// drives this whole endpoint. The catalog cache (eks_addon_catalog_
// cache.go) is keyed by k8sVersion alone — answer doesn't depend on
// which cluster asked, so a fleet of N 1.30 clusters hits AWS once
// per 6h. Per-cluster install state is layered in at request time
// from the existing eksAddonsCache (best-effort: if the list cache
// is cold, "installed" is left null and the SPA can layer client-
// side from useAddons()).
//
// Reuses VerbEKSAddonsRead with op:"catalog" / "catalog:cache_hit"
// to avoid taxonomy churn — same compliance-readable pattern as
// "list" / "detail" today.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// addonCatalogPageMaxResults is the AWS-side page size cap. AWS's
// public catalog has ~50 add-ons today; 100 is comfortable headroom.
const addonCatalogPageMaxResults = 100

// addonCatalogPageCap bounds NextToken pagination. 5 pages × 100 =
// 500 add-ons — far above the public catalog (~50) plus marketplace
// growth. Beyond this we log a truncation warning rather than
// pulling for minutes on a runaway response.
const addonCatalogPageCap = 5

// CatalogAddonVersion is one (version, k8s-compat) row inside a
// catalog entry. Mirrors the SDK's AddonVersionInfo + Compatibilities
// flatten.
type CatalogAddonVersion struct {
	Version            string   `json:"version"`
	KubernetesVersions []string `json:"kubernetesVersions"`
	Default            bool     `json:"default,omitempty"`
}

// CatalogInstalled annotates a catalog row when the addon is already
// installed on this cluster. nil = "available, not installed".
type CatalogInstalled struct {
	Version string `json:"version"`
	Status  string `json:"status,omitempty"`
}

// CatalogAddon is one row of the catalog response.
type CatalogAddon struct {
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Publisher string `json:"publisher,omitempty"`
	// MarketplaceProduct is true when AWS reports the addon as a
	// marketplace listing. Operators must accept the marketplace EULA
	// outside Periscope before install will succeed; the SPA flags
	// these rows so the dialog can warn (PR-2 wires the warning).
	MarketplaceProduct bool                  `json:"marketplaceProduct,omitempty"`
	CompatibleVersions []CatalogAddonVersion `json:"compatibleVersions"`
	// Installed is non-nil when the addon is installed on this cluster.
	// Best-effort: only populated when the per-cluster addons-list
	// cache is warm. The SPA falls back to layering from useAddons()
	// if the field is absent.
	Installed *CatalogInstalled `json:"installed,omitempty"`
}

// AddonCatalogResponse is the GET /eks/addons/catalog payload.
type AddonCatalogResponse struct {
	Available         []CatalogAddon `json:"available"`
	KubernetesVersion string         `json:"kubernetesVersion,omitempty"`
}

func eksAddonCatalogHandler(reg *clusters.Registry, catalog *addonCatalogCache, addons *eksAddonsCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"add-on catalog is only available for EKS-backed clusters")
			return
		}

		client := newEKSAddonsClient(p, c)
		eksName := c.EKSName()

		// Cluster K8s version drives the catalog filter. We always
		// describe the cluster — DescribeCluster is per-cluster, the
		// catalog cache is per-k8sVer, so we need the version even on
		// a catalog cache hit.
		clusterVer, err := describeClusterVersion(r.Context(), client, eksName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks describe cluster failed (catalog)", "cluster", c.Name, "err", err)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "catalog", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to describe cluster: "+err.Error())
			return
		}

		// Cache check. Sticky errors are returned as the cached failure.
		if cached, cachedErr, hit := catalog.Get(clusterVer); hit {
			if cachedErr != nil {
				emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "catalog:cache_hit", cachedErr.Error())
				status, code := awsErrorToStatus(cachedErr)
				writeAPIErrorJSON(w, status, code,
					"failed to fetch add-on catalog: "+cachedErr.Error())
				return
			}
			resp := buildCatalogResponse(cached, clusterVer)
			layerInstalled(&resp, addons, c.Name)
			writeJSON(w, http.StatusOK, resp)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "catalog:cache_hit", "")
			return
		}

		val, err := fetchAddonCatalog(r.Context(), client, clusterVer)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks describe addon versions (catalog) failed",
				"cluster", c.Name, "k8s", clusterVer, "err", err)
			catalog.Put(clusterVer, nil, err)
			emitAddonsRead(r.Context(), emitter, c, audit.OutcomeFailure, "catalog", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to fetch add-on catalog: "+err.Error())
			return
		}

		catalog.Put(clusterVer, val, nil)
		resp := buildCatalogResponse(val, clusterVer)
		layerInstalled(&resp, addons, c.Name)
		writeJSON(w, http.StatusOK, resp)
		emitAddonsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "catalog", "")
	}
}

// fetchAddonCatalog calls DescribeAddonVersions with no AddonName
// filter and consumes pagination up to addonCatalogPageCap pages.
// AWS's response shape: Addons is one AddonInfo per addon name;
// each carries the version list inside.
//
// Worst-case latency is bounded at addonCatalogPageCap *
// addonDescribeTimeout = 5 * 4s = 20s, but real responses fit in
// one page (~50 add-ons) and complete in well under a second.
func fetchAddonCatalog(parent context.Context, client eksAddonsAPI, k8sVersion string) (*addonCatalogCacheValue, error) {
	val := &addonCatalogCacheValue{KubernetesVersion: k8sVersion}
	var nextToken *string
	for page := 0; page < addonCatalogPageCap; page++ {
		ctx, cancel := context.WithTimeout(parent, addonDescribeTimeout)
		in := &eks.DescribeAddonVersionsInput{
			MaxResults: int32Ptr(addonCatalogPageMaxResults),
			NextToken:  nextToken,
		}
		if k8sVersion != "" {
			in.KubernetesVersion = &k8sVersion
		}
		out, err := client.DescribeAddonVersions(ctx, in)
		cancel()
		if err != nil {
			return nil, err
		}
		for _, info := range out.Addons {
			val.Entries = append(val.Entries, mapCatalogAddon(info, k8sVersion))
		}
		if out.NextToken == nil || *out.NextToken == "" {
			nextToken = nil
			break
		}
		nextToken = out.NextToken
	}
	if nextToken != nil {
		slog.Warn("eks addon catalog truncated; not paginating further",
			"k8s", k8sVersion, "page_cap", addonCatalogPageCap, "page_size", addonCatalogPageMaxResults)
	}
	sortCatalogEntries(val.Entries)
	return val, nil
}

// mapCatalogAddon flattens one AWS AddonInfo into the wire shape.
// Compatibilities are per-version on the SDK side; the wire shape
// preserves the per-version list verbatim so the SPA can render
// version × k8s tables.
func mapCatalogAddon(info ekstypes.AddonInfo, k8sVersion string) CatalogAddon {
	out := CatalogAddon{
		Name:      deref(info.AddonName),
		Type:      deref(info.Type),
		Owner:     deref(info.Owner),
		Publisher: deref(info.Publisher),
	}
	if info.MarketplaceInformation != nil {
		out.MarketplaceProduct = true
	}
	for _, v := range info.AddonVersions {
		versionStr := deref(v.AddonVersion)
		if versionStr == "" {
			continue
		}
		entry := CatalogAddonVersion{Version: versionStr}
		for _, compat := range v.Compatibilities {
			cv := deref(compat.ClusterVersion)
			if cv != "" {
				entry.KubernetesVersions = append(entry.KubernetesVersions, cv)
			}
			// "default" applies to (version, k8sVersion) — surface it
			// when the queried k8sVer matches a default-marked compat.
			if compat.DefaultVersion && (k8sVersion == "" || cv == k8sVersion) {
				entry.Default = true
			}
		}
		out.CompatibleVersions = append(out.CompatibleVersions, entry)
	}
	return out
}

// sortCatalogEntries: AWS-owned first, then alphabetical by name.
// "aws" / "amazon-web-services" / empty owner are treated as AWS-
// authored; everything else is third-party.
func sortCatalogEntries(entries []CatalogAddon) {
	sort.SliceStable(entries, func(i, j int) bool {
		ai, aj := isAWSOwned(entries[i].Owner), isAWSOwned(entries[j].Owner)
		if ai != aj {
			return ai
		}
		return entries[i].Name < entries[j].Name
	})
}

func isAWSOwned(owner string) bool {
	switch owner {
	case "", "aws", "amazon-web-services":
		return true
	}
	return false
}

// buildCatalogResponse copies the cache value into a response shape.
// The cache holds entries unannotated; install state is layered in
// after by layerInstalled.
func buildCatalogResponse(val *addonCatalogCacheValue, clusterVer string) AddonCatalogResponse {
	if val == nil {
		return AddonCatalogResponse{
			Available:         []CatalogAddon{},
			KubernetesVersion: clusterVer,
		}
	}
	resp := AddonCatalogResponse{
		KubernetesVersion: clusterVer,
		Available:         make([]CatalogAddon, len(val.Entries)),
	}
	copy(resp.Available, val.Entries)
	return resp
}

// layerInstalled annotates response rows with installed-state from
// the per-cluster addons cache. Best-effort: if the addons-list
// cache is cold, leaves Installed nil. The SPA already calls
// useAddons() so it can layer client-side as a fallback.
func layerInstalled(resp *AddonCatalogResponse, addons *eksAddonsCache, clusterName string) {
	if addons == nil {
		return
	}
	list, ok := addons.GetList(clusterName)
	if !ok || list == nil {
		return
	}
	byName := make(map[string]AddonSummary, len(list.Addons))
	for _, a := range list.Addons {
		byName[a.Name] = a
	}
	for i := range resp.Available {
		row := &resp.Available[i]
		if installed, ok := byName[row.Name]; ok {
			row.Installed = &CatalogInstalled{
				Version: installed.Version,
				Status:  installed.Status,
			}
		}
	}
}
