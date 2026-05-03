package main

// helm_handler.go — read-only Helm release endpoints.
//
//   GET /api/clusters/{cluster}/helm/releases
//   GET /api/clusters/{cluster}/helm/releases/{ns}/{name}?revision=N
//   GET /api/clusters/{cluster}/helm/releases/{ns}/{name}/history
//   GET /api/clusters/{cluster}/helm/releases/{ns}/{name}/diff?from=N&to=M
//
// All endpoints route through credentials.Wrap and rely on the
// impersonating clientset, so a user only sees releases their K8s
// RBAC permits — same security model as every other read endpoint.
//
// Caching: only the list endpoint has a server-side TTL (mirrors
// fleetCache). Detail / history / diff are per-revision idempotent
// reads and are served fresh; the SPA uses TanStack Query to
// collapse repeated reads within a tab session.
//
// Write operations (rollback / upgrade / install / uninstall) are
// out of scope for v1 by design (issue #9). Their landing requires
// the SAR-gating layer from #7 + a manifest pre-render and per-
// resource SAR fan-out — the parsed Resources slice on the detail
// blob is the foundation for that.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// helmListCap bounds the list response. The issue spec lands at 200;
// larger clusters get a `truncated: true` flag and the SPA can warn.
// 200 covers >99% of real clusters per Helm community telemetry.
const helmListCap = 200

// helmDetailMaxBytes caps the per-revision payload (manifest + values
// + notes). 5 MB is generous for real-world charts — istio's
// canonical bundle renders ~600 KB; even prometheus-stack stays
// under 1 MB.
const helmDetailMaxBytes = 5 * 1024 * 1024

// helmHistoryMax is the default number of revisions returned by the
// history endpoint when the client does not pass `?max=`. Mirrors
// helm's CLI default.
const helmHistoryMax = 10

// HelmReleasesResponse is the GET /helm/releases payload.
type HelmReleasesResponse struct {
	Releases  []k8s.HelmReleaseSummary `json:"releases"`
	Truncated bool                     `json:"truncated"`
}

// HelmHistoryResponse is the GET /helm/.../history payload.
type HelmHistoryResponse struct {
	Revisions []k8s.HelmHistoryEntry `json:"revisions"`
}

// helmListHandler returns the cluster-wide release list.
func helmListHandler(reg *clusters.Registry, cache *helmListCache) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}

		actor := p.Actor()
		impGroups := p.Impersonation().Groups
		if actor != "" {
			if cached, ok := cache.Get(actor, c.Name, impGroups); ok {
				writeJSON(w, http.StatusOK, cached)
				return
			}
		}

		releases, truncated, err := k8s.ListHelmReleases(r.Context(), p, c, helmListCap)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("helm list failed", "cluster", c.Name, "err", err)
			writeAPIError(w, err, httpStatusFor(err))
			return
		}

		resp := HelmReleasesResponse{Releases: releases, Truncated: truncated}
		if actor != "" {
			cache.Put(actor, c.Name, impGroups, resp)
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// helmGetHandler returns the per-revision detail blob (values +
// manifest + chart metadata + parsed resources).
func helmGetHandler(reg *clusters.Registry) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := chi.URLParam(r, "ns")
		name := chi.URLParam(r, "name")
		if ns == "" || name == "" {
			http.Error(w, "missing namespace or release name", http.StatusBadRequest)
			return
		}
		revision, err := parseRevisionParam(r.URL.Query().Get("revision"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		detail, err := k8s.GetHelmRelease(r.Context(), p, c, ns, name, revision, helmDetailMaxBytes)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("helm get failed",
				"cluster", c.Name, "namespace", ns, "name", name, "revision", revision, "err", err)
			writeAPIError(w, err, httpStatusFor(err))
			return
		}
		// Per-revision blobs are immutable once written. A short
		// browser cache lets repeat tab-switches stay client-side
		// without bloating server memory.
		w.Header().Set("Cache-Control", "private, max-age=60")
		writeJSON(w, http.StatusOK, detail)
	}
}

// helmHistoryHandler returns the revision metadata list.
func helmHistoryHandler(reg *clusters.Registry) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := chi.URLParam(r, "ns")
		name := chi.URLParam(r, "name")
		if ns == "" || name == "" {
			http.Error(w, "missing namespace or release name", http.StatusBadRequest)
			return
		}
		max := helmHistoryMax
		if v := r.URL.Query().Get("max"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 || n > 100 {
				http.Error(w, "invalid max (1-100)", http.StatusBadRequest)
				return
			}
			max = n
		}

		entries, err := k8s.GetHelmHistory(r.Context(), p, c, ns, name, max)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("helm history failed",
				"cluster", c.Name, "namespace", ns, "name", name, "err", err)
			writeAPIError(w, err, httpStatusFor(err))
			return
		}
		writeJSON(w, http.StatusOK, HelmHistoryResponse{Revisions: entries})
	}
}

// helmDiffHandler returns the structured semantic diff between two
// revisions. Both `from` and `to` query params are required; either
// may be 0 (or absent) but at least one revision pair must be
// resolvable.
func helmDiffHandler(reg *clusters.Registry) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		ns := chi.URLParam(r, "ns")
		name := chi.URLParam(r, "name")
		if ns == "" || name == "" {
			http.Error(w, "missing namespace or release name", http.StatusBadRequest)
			return
		}
		fromRev, err := parseRevisionParam(r.URL.Query().Get("from"))
		if err != nil {
			http.Error(w, "invalid `from`: "+err.Error(), http.StatusBadRequest)
			return
		}
		toRev, err := parseRevisionParam(r.URL.Query().Get("to"))
		if err != nil {
			http.Error(w, "invalid `to`: "+err.Error(), http.StatusBadRequest)
			return
		}
		if fromRev == 0 && toRev == 0 {
			http.Error(w, "diff requires at least one of `from` or `to`", http.StatusBadRequest)
			return
		}

		diff, err := k8s.DiffHelmRevisions(r.Context(), p, c, ns, name, fromRev, toRev, helmDetailMaxBytes)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("helm diff failed",
				"cluster", c.Name, "namespace", ns, "name", name,
				"from", fromRev, "to", toRev, "err", err)
			writeAPIError(w, err, httpStatusFor(err))
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=60")
		writeJSON(w, http.StatusOK, diff)
	}
}

// parseRevisionParam parses a "revision" / "from" / "to" query param.
// Empty string is returned as 0 (caller's "latest" sentinel). Negative
// or non-numeric input is rejected.
func parseRevisionParam(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, errors.New("revision must be a non-negative integer")
	}
	if n < 0 {
		return 0, errors.New("revision must be a non-negative integer")
	}
	return n, nil
}
