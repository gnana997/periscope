// HTTP handlers for the CVE surface (#165).
//
// Seven endpoints under /api/clusters/{cluster}/cve/...:
//
//   GET  /status                            store flags + entry counts
//   GET  /by-instance                       list instances + severity counts
//   GET  /by-instance/{instanceID}          full findings for one instance
//   GET  /by-digest/{digest}                full findings for one image digest
//   GET  /pods                              per-pod aggregate, paginated
//   GET  /pods/{namespace}/{pod}            full per-container findings for one pod
//   POST /refresh                           force refresh, emits one cve_refresh audit row
//
// All read paths serve from the local cve.Store (never call Inspector
// during a SPA request). Cold cluster activation blocks on
// Manager.EnsureHydrated (~10-30s); subsequent reads are O(1).
//
// Empty-state contract: when the CVE manager is nil (Helm-disabled)
// or the store reports Disabled (account-side Inspector v2 not on /
// missing IAM), every endpoint returns HTTP 200 with
// {inspectorEnabled:false, hydrated:true, ...} — NEVER an error.
// This is the contract the SPA relies on to render the "Inspector v2
// not enabled" hint without special-casing transport errors.

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/cve"
)

// cvePodsPageSize is the default + max page size for /cve/pods. Same
// magnitude as the helm list cap; pods/page is the single biggest
// driver of response size on the CVE surface so the cap is explicit
// rather than configurable (frontend pages further anyway).
const cvePodsPageSize = 100

// resolveCveCluster is the shared route-prefix decoder. Returns the
// matched cluster + the cve.Store if the manager is available; nil
// store means "render empty state".
func resolveCveCluster(w http.ResponseWriter, r *http.Request, reg *clusters.Registry) (clusters.Cluster, bool) {
	c, ok := reg.ByName(chi.URLParam(r, "cluster"))
	if !ok {
		http.Error(w, "cluster not found", http.StatusNotFound)
		return clusters.Cluster{}, false
	}
	return c, true
}

// ensureHydratedOrEmpty drives the cold-path hydrate and short-
// circuits the empty-state envelope when the manager is nil or the
// store flips to Disabled. Returns (store, true) when the handler
// should continue; (nil, false) when the handler has already written
// an empty-state response and should return.
func ensureHydratedOrEmpty(w http.ResponseWriter, r *http.Request, mgr *cve.Manager, c clusters.Cluster, empty any) (*cve.Store, bool) {
	if mgr == nil {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, empty)
		return nil, false
	}
	store, err := mgr.EnsureHydrated(r.Context(), c)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, false
		}
		slog.Warn("cve hydrate failed", "cluster", c.Name, "err", err)
		// Degrade gracefully: surface the empty envelope (with
		// hydrated:false) rather than 5xx. The SPA can poll /status
		// to detect recovery.
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, empty)
		return nil, false
	}
	if store.Disabled() {
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, empty)
		return nil, false
	}
	return store, true
}

// cveETag derives a stable ETag for read endpoints from the store's
// (lastHydrate, digestCount, instanceCount) tuple. A delta refresh
// of a single digest does NOT bump the ETag — chips don't need
// real-time accuracy and TTL-grained change detection is enough.
func cveETag(store *cve.Store) string {
	d, i := store.EntryCounts()
	return fmt.Sprintf(`W/"%s-%d-%d"`,
		strconv.FormatInt(store.LastHydrate().UnixNano(), 36), d, i)
}

// setCveReadHeaders applies Cache-Control + ETag for the read path
// and short-circuits with 304 when the client sends a matching
// If-None-Match. Returns true if the handler should return without
// writing a body.
func setCveReadHeaders(w http.ResponseWriter, r *http.Request, store *cve.Store) bool {
	w.Header().Set("Cache-Control", "no-store")
	etag := cveETag(store)
	w.Header().Set("ETag", etag)
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == etag {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

// --- /status -----------------------------------------------------

func cveStatusHandler(reg *clusters.Registry, mgr *cve.Manager) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		c, ok := resolveCveCluster(w, r, reg)
		if !ok {
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if mgr == nil {
			writeJSON(w, http.StatusOK, cve.StatusResp{Hydrated: true})
			return
		}
		// /status deliberately does NOT trigger hydrate — the SPA
		// uses it to poll cache state and we don't want a status
		// poll to start a 10-30s cold scan. Hydrate is triggered by
		// the first read endpoint instead.
		store := mgr.Get(c.Name)
		if store == nil {
			writeJSON(w, http.StatusOK, cve.StatusResp{Hydrated: false})
			return
		}
		d, i := store.EntryCounts()
		writeJSON(w, http.StatusOK, cve.StatusResp{
			InspectorEnabled: !store.Disabled(),
			Hydrated:         store.Hydrated(),
			LastHydrate:      store.LastHydrate(),
			EntryCounts:      cve.EntryCountsWire{Digests: d, Instances: i},
		})
	}
}

// --- /by-instance (list) ----------------------------------------

func cveByInstanceHandler(reg *clusters.Registry, mgr *cve.Manager) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		c, ok := resolveCveCluster(w, r, reg)
		if !ok {
			return
		}
		empty := cve.InstancesResp{Instances: []cve.InstanceRow{}, Hydrated: true}
		store, ok := ensureHydratedOrEmpty(w, r, mgr, c, empty)
		if !ok {
			return
		}
		if setCveReadHeaders(w, r, store) {
			return
		}
		_, instances := store.Snapshot()
		rows := make([]cve.InstanceRow, 0, len(instances))
		for _, e := range instances {
			rows = append(rows, cve.InstanceRow{
				InstanceID:     e.InstanceID,
				Owner:          cve.OwnerRef{Kind: e.OwnerKind, Name: e.OwnerName},
				AMI:            e.AMI,
				SeverityCounts: cve.WireSeverity(cve.CountSeverities(e.Findings)),
				LastFetchedAt:  e.LastFetched,
			})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].InstanceID < rows[j].InstanceID })
		writeJSON(w, http.StatusOK, cve.InstancesResp{
			Instances: rows, InspectorEnabled: true, Hydrated: true,
		})
	}
}

// --- /by-instance/{id} ------------------------------------------

func cveByInstanceOneHandler(reg *clusters.Registry, mgr *cve.Manager) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		c, ok := resolveCveCluster(w, r, reg)
		if !ok {
			return
		}
		empty := cve.FindingsResp{Findings: []cve.Finding{}, Hydrated: true}
		store, ok := ensureHydratedOrEmpty(w, r, mgr, c, empty)
		if !ok {
			return
		}
		if setCveReadHeaders(w, r, store) {
			return
		}
		id := chi.URLParam(r, "instanceID")
		e := store.GetInstance(id)
		if e == nil {
			http.Error(w, "instance not found in CVE cache", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, cve.FindingsResp{
			Findings:         e.Findings,
			LastFetchedAt:    e.LastFetched,
			InspectorEnabled: true,
			Hydrated:         true,
		})
	}
}

// --- /by-digest/{digest} ----------------------------------------

func cveByDigestHandler(reg *clusters.Registry, mgr *cve.Manager) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		c, ok := resolveCveCluster(w, r, reg)
		if !ok {
			return
		}
		empty := cve.FindingsResp{Findings: []cve.Finding{}, Hydrated: true}
		store, ok := ensureHydratedOrEmpty(w, r, mgr, c, empty)
		if !ok {
			return
		}
		if setCveReadHeaders(w, r, store) {
			return
		}
		// Chi unescapes ':' in the URL param, so sha256:abc lands
		// here verbatim.
		digest := chi.URLParam(r, "digest")
		e := store.GetDigest(digest)
		if e == nil {
			http.Error(w, "digest not found in CVE cache", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, cve.FindingsResp{
			Findings:         e.Findings,
			LastFetchedAt:    e.LastFetched,
			InspectorEnabled: true,
			Hydrated:         true,
		})
	}
}

// --- /pods (list, paginated) ------------------------------------

func cvePodsHandler(reg *clusters.Registry, mgr *cve.Manager) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		c, ok := resolveCveCluster(w, r, reg)
		if !ok {
			return
		}
		empty := cve.PodsResp{Pods: []cve.PodRow{}, Hydrated: true}
		store, ok := ensureHydratedOrEmpty(w, r, mgr, c, empty)
		if !ok {
			return
		}
		if setCveReadHeaders(w, r, store) {
			return
		}

		lister := mgr.PodLister(c.Name)
		if lister == nil {
			// Hydrate succeeded but the informer hasn't started —
			// rare race during cold path. Empty list, hydrated:true
			// so the SPA doesn't re-spinner.
			writeJSON(w, http.StatusOK, cve.PodsResp{
				Pods: []cve.PodRow{}, InspectorEnabled: true, Hydrated: true,
			})
			return
		}
		pods, err := lister.List(labels.Everything())
		if err != nil {
			slog.Warn("cve pods: lister failed", "cluster", c.Name, "err", err)
			writeAPIError(w, err, http.StatusInternalServerError)
			return
		}

		// Stable lex order so a base64(ns/name) cursor round-trips
		// across pages.
		sort.Slice(pods, func(i, j int) bool {
			if pods[i].Namespace != pods[j].Namespace {
				return pods[i].Namespace < pods[j].Namespace
			}
			return pods[i].Name < pods[j].Name
		})

		// Decode + apply cursor: skip every pod with key <=
		// cursorKey. Empty cursor starts from the beginning.
		cursorKey := decodeCursor(r.URL.Query().Get("cursor"))
		start := 0
		if cursorKey != "" {
			start = sort.Search(len(pods), func(i int) bool {
				return podKey(pods[i]) > cursorKey
			})
		}
		end := start + cvePodsPageSize
		if end > len(pods) {
			end = len(pods)
		}
		page := pods[start:end]

		rows := make([]cve.PodRow, 0, len(page))
		for _, p := range page {
			rows = append(rows, buildPodRow(p, store))
		}
		var next string
		if end < len(pods) {
			next = encodeCursor(podKey(page[len(page)-1]))
		}
		writeJSON(w, http.StatusOK, cve.PodsResp{
			Pods:             rows,
			Next:             next,
			InspectorEnabled: true,
			Hydrated:         true,
		})
	}
}

// --- /pods/{namespace}/{pod} ------------------------------------

func cvePodsOneHandler(reg *clusters.Registry, mgr *cve.Manager) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		c, ok := resolveCveCluster(w, r, reg)
		if !ok {
			return
		}
		empty := cve.PodRow{Containers: []cve.ContainerRow{}}
		store, ok := ensureHydratedOrEmpty(w, r, mgr, c, empty)
		if !ok {
			return
		}
		if setCveReadHeaders(w, r, store) {
			return
		}
		lister := mgr.PodLister(c.Name)
		if lister == nil {
			http.Error(w, "pod informer not ready", http.StatusServiceUnavailable)
			return
		}
		ns, name := chi.URLParam(r, "namespace"), chi.URLParam(r, "pod")
		p, err := lister.Pods(ns).Get(name)
		if err != nil {
			writeAPIError(w, err, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, buildPodRow(p, store))
	}
}

// --- POST /refresh ----------------------------------------------

func cveRefreshHandler(reg *clusters.Registry, mgr *cve.Manager, auditer *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		c, ok := resolveCveCluster(w, r, reg)
		if !ok {
			return
		}
		var req cve.RefreshReq
		if r.Body != nil {
			// Empty body is acceptable — still emits an audit row
			// (operator chose to act).
			_ = json.NewDecoder(r.Body).Decode(&req)
		}
		evt := audit.Event{
			Actor:   actorFromContext(r.Context()),
			Verb:    audit.VerbCveRefresh,
			Cluster: c.Name,
			Extra: map[string]any{
				"digests":     req.Digests,
				"instanceIds": req.InstanceIDs,
			},
		}
		if mgr == nil {
			// Inspector disabled via Helm — record the action attempt
			// but return cleanly so the SPA gets the same empty-
			// state contract.
			evt.Outcome = audit.OutcomeFailure
			evt.Reason = "inspector disabled"
			auditer.Record(r.Context(), evt)
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusOK, map[string]any{"inspectorEnabled": false})
			return
		}
		// Hydrate-in-flight: if /refresh fires before the cold scan
		// finishes (e.g. operator hit refresh during the spinner),
		// queue behind it and signal 202 with a poll hint instead
		// of blocking the request.
		if store := mgr.Get(c.Name); store == nil || !store.Hydrated() {
			evt.Outcome = audit.OutcomeSuccess
			evt.Extra["queued"] = true
			auditer.Record(r.Context(), evt)
			w.Header().Set("Next-Poll", "2")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"queued": true})
			return
		}
		err := mgr.Refresh(r.Context(), c, req.Digests, req.InstanceIDs)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			evt.Outcome = outcomeFor(err)
			evt.Reason = err.Error()
			auditer.Record(r.Context(), evt)
			writeAPIError(w, err, httpStatusFor(err))
			return
		}
		evt.Outcome = audit.OutcomeSuccess
		auditer.Record(r.Context(), evt)
		writeJSON(w, http.StatusOK, map[string]any{"refreshed": true})
	}
}

// --- helpers ----------------------------------------------------

// podKey is the cursor key shape (namespace/name). Used both for the
// sort + cursor advance.
func podKey(p *corev1.Pod) string {
	return p.Namespace + "/" + p.Name
}

// encodeCursor / decodeCursor wrap the cursor in URL-safe base64 so
// it survives URL round-tripping and is opaque-ish to clients.
func encodeCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}
func decodeCursor(raw string) string {
	if raw == "" {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return ""
	}
	return string(b)
}

// buildPodRow joins a pod against the store: for each container,
// classify ECR / non-ECR / pending and (when scanned) look up
// findings for the digest. Roll up severity counts across scanned
// containers; compute scanCoverage from the per-container mix.
func buildPodRow(p *corev1.Pod, store *cve.Store) cve.PodRow {
	rolled := cve.SeverityCounts{}
	containers := make([]cve.ContainerRow, 0, len(p.Spec.Containers)+len(p.Spec.InitContainers))

	idByName := make(map[string]string, len(p.Status.ContainerStatuses)+len(p.Status.InitContainerStatuses))
	for _, cs := range p.Status.ContainerStatuses {
		idByName[cs.Name] = cs.ImageID
	}
	for _, cs := range p.Status.InitContainerStatuses {
		idByName[cs.Name] = cs.ImageID
	}

	scanned, others := 0, 0
	addContainer := func(name, image string) {
		digest, state := cve.ImageScanState(image, idByName[name])
		row := cve.ContainerRow{Name: name, Image: image, Digest: digest, ScanState: state}
		if state == cve.ScanStateScanned {
			scanned++
			if e := store.GetDigest(digest); e != nil {
				counts := cve.CountSeverities(e.Findings)
				wire := cve.WireSeverity(counts)
				row.SeverityCounts = &wire
				rolled.Add(counts)
			} else {
				zero := cve.WireSeverity(cve.SeverityCounts{})
				row.SeverityCounts = &zero
			}
		} else {
			others++
		}
		containers = append(containers, row)
	}
	for _, ctr := range p.Spec.Containers {
		addContainer(ctr.Name, ctr.Image)
	}
	for _, ctr := range p.Spec.InitContainers {
		addContainer(ctr.Name, ctr.Image)
	}

	var coverage cve.ScanCoverage
	switch {
	case scanned == 0:
		coverage = cve.CoverageNone
	case others == 0:
		coverage = cve.CoverageFull
	default:
		coverage = cve.CoveragePartial
	}

	return cve.PodRow{
		Namespace:              p.Namespace,
		Name:                   p.Name,
		Containers:             containers,
		RolledUpSeverityCounts: cve.WireSeverity(rolled),
		ScanCoverage:           coverage,
	}
}

// stripTrailingNewline is a stylistic no-op; kept here as a hook
// for future content-negotiation needs (the SPA's fetch wrapper
// trims trailing newlines so we don't pay for one).
var _ = strings.TrimSpace

// --- /by-workload/{kind}/{namespace}/{name} ---------------------

func cveByWorkloadHandler(reg *clusters.Registry, mgr *cve.Manager) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, _ credentials.Provider) {
		c, ok := resolveCveCluster(w, r, reg)
		if !ok {
			return
		}
		kind := chi.URLParam(r, "kind")
		namespace := chi.URLParam(r, "namespace")
		name := chi.URLParam(r, "name")
		if !cve.IsSupportedWorkloadKind(kind) {
			// Tight allow-list — keeps the handler honest about what
			// it can resolve. CronJob omission is documented in
			// owner_walk.go.
			http.Error(w, "unsupported workload kind: "+kind, http.StatusBadRequest)
			return
		}
		empty := cve.WorkloadCveResp{
			Workload: cve.WorkloadRef{Kind: kind, Namespace: namespace, Name: name},
			Pods:     []cve.PodRow{},
			Hydrated: true,
		}
		store, ok := ensureHydratedOrEmpty(w, r, mgr, c, empty)
		if !ok {
			return
		}
		if setCveReadHeaders(w, r, store) {
			return
		}

		lister := mgr.PodLister(c.Name)
		if lister == nil {
			// Informer hasn't started yet — same race as /cve/pods.
			// Return empty rather than 503 to keep the SPA's
			// workload Security tab quiet during cold-path warmup.
			writeJSON(w, http.StatusOK, cve.WorkloadCveResp{
				Workload:         cve.WorkloadRef{Kind: kind, Namespace: namespace, Name: name},
				Pods:             []cve.PodRow{},
				InspectorEnabled: true,
				Hydrated:         true,
			})
			return
		}
		rsLister := mgr.ReplicaSetLister(c.Name)
		// rsLister == nil is fine when kind != "Deployment"; the
		// owner-walk helper handles it.

		allPods, err := lister.Pods(namespace).List(labels.Everything())
		if err != nil {
			slog.Warn("cve by-workload: lister failed", "cluster", c.Name, "err", err)
			writeAPIError(w, err, http.StatusInternalServerError)
			return
		}

		matched := make([]*corev1.Pod, 0, len(allPods))
		for _, p := range allPods {
			if cve.PodOwnedBy(p, kind, namespace, name, rsLister) {
				matched = append(matched, p)
			}
		}
		sort.Slice(matched, func(i, j int) bool { return matched[i].Name < matched[j].Name })

		rolled := cve.SeverityCounts{}
		scannedContainers, totalContainers := 0, 0
		rows := make([]cve.PodRow, 0, len(matched))
		for _, p := range matched {
			row := buildPodRow(p, store)
			rows = append(rows, row)
			// Re-aggregate via WireSeverityCounts since PodRow only
			// keeps the wire shape — cheap, the row already walked
			// findings once.
			rolled.Critical += row.RolledUpSeverityCounts.Critical
			rolled.High += row.RolledUpSeverityCounts.High
			rolled.Medium += row.RolledUpSeverityCounts.Medium
			rolled.Low += row.RolledUpSeverityCounts.Low
			rolled.Informational += row.RolledUpSeverityCounts.Informational
			// Coverage counters: any scanned/non-scanned container
			// contributes to the workload-wide ratio so a mostly-
			// scanned DaemonSet with one outlier sidecar doesn't
			// flip the chip to "none".
			for _, ctr := range row.Containers {
				totalContainers++
				if ctr.ScanState == cve.ScanStateScanned {
					scannedContainers++
				}
			}
		}

		var coverage cve.ScanCoverage
		switch {
		case totalContainers == 0:
			// No matched pods (or pods with no containers — exotic).
			// Render as "none" so the SPA shows a "no scan coverage"
			// hint rather than a misleading "full" chip.
			coverage = cve.CoverageNone
		case scannedContainers == 0:
			coverage = cve.CoverageNone
		case scannedContainers == totalContainers:
			coverage = cve.CoverageFull
		default:
			coverage = cve.CoveragePartial
		}

		writeJSON(w, http.StatusOK, cve.WorkloadCveResp{
			Workload:               cve.WorkloadRef{Kind: kind, Namespace: namespace, Name: name},
			Pods:                   rows,
			RolledUpSeverityCounts: cve.WireSeverity(rolled),
			ScanCoverage:           coverage,
			InspectorEnabled:       true,
			Hydrated:               true,
		})
	}
}
