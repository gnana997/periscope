package main

// karpenter_handler.go — curated Karpenter dashboard endpoint (#118).
//
//   GET /api/clusters/{cluster}/karpenter
//
// Single-shot read. Joins NodePools / NodeClaims / pending pods +
// FailedScheduling events / controller metrics into one response.
//
// Auto-detect: when karpenter.sh/v1 CRDs are absent the handler
// returns `{available: false}` immediately (HTTP 200, not 422 — the
// SPA's sidebar logic gates on this field, not a status code). One
// audit row still emits so compliance can answer "did anyone load
// the Karpenter view on this cluster?" even when Karpenter isn't
// installed.
//
// Graceful degradation: every cross-call (events, metrics) is
// best-effort. The response always carries the base view (NodePools
// / NodeClaims / pending list) even when secondary data sources fail.
// Failures are logged at WARN; the response sets `metricsAvailable:
// false` (cost blocks omitted) but doesn't fail.
//
// Audit: VerbKarpenterRead emits on every call, with `op` in Extra
// as one of:
//   - "available_false"  — Karpenter not installed
//   - "list"             — full read (includes truncated counts /
//                          metrics availability)
//   - "list:list_failed" — base list call (NodePools or NodeClaims)
//                          failed; client got 5xx

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// karpenterRequestTimeout caps the whole-handler latency. The
// response shape's truncation flag handles cap-at-50; this timeout
// handles the long-tail "apiserver pod list takes 30 seconds on a
// 50k-pod cluster" case.
const karpenterRequestTimeout = 15 * time.Second

// Test seams. Production wires these to the real k8s.* entry points;
// tests substitute fakes that return canned data so the handler-level
// concerns (auto-detect short-circuit, parallel fan-out, audit
// emission, graceful degradation) stay testable without a real
// apiserver.
var (
	karpenterIsInstalledFn = k8s.IsKarpenterInstalled
	karpenterBuildClientsFn = k8s.BuildKarpenterClients
	karpenterListPoolsFn = k8s.ListKarpenterNodePools
	karpenterListClaimsFn = k8s.ListKarpenterNodeClaims
	karpenterListPendingFn = k8s.ListKarpenterPendingPods
	karpenterScrapeMetricsFn = k8s.ScrapeKarpenterMetrics
	karpenterComputeCostsFn = k8s.ComputeKarpenterCosts
)

func karpenterHandler(reg *clusters.Registry, emitter *audit.Emitter) credentials.Handler {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), karpenterRequestTimeout)
		defer cancel()

		// Auto-detect first. CRDs absent → respond immediately.
		installed, err := karpenterIsInstalledFn(ctx, p, c)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.WarnContext(ctx, "karpenter detect failed", "cluster", c.Name, "err", err)
			emitKarpenterRead(ctx, emitter, c, audit.OutcomeFailure, "detect_failed", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_INTERNAL", err.Error())
			return
		}
		if !installed {
			writeJSON(w, http.StatusOK, k8s.KarpenterDashboard{Available: false})
			emitKarpenterRead(ctx, emitter, c, audit.OutcomeSuccess, "available_false", "")
			return
		}

		// Build clients once; share between list calls and the metrics scrape.
		cs, dyn, err := karpenterBuildClientsFn(ctx, p, c)
		if err != nil {
			slog.WarnContext(ctx, "karpenter build clients failed", "cluster", c.Name, "err", err)
			emitKarpenterRead(ctx, emitter, c, audit.OutcomeFailure, "client_build_failed", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_INTERNAL", err.Error())
			return
		}

		// Parallel fan-out. Two of the four legs are required (NodePools
		// + NodeClaims — the dashboard's purpose collapses without them);
		// the other two (pending pods, metrics) are best-effort.
		var (
			wg          sync.WaitGroup
			pools       []k8s.NodePoolView
			poolsErr    error
			claims      []k8s.NodeClaimView
			claimsErr   error
			pending     []k8s.PendingPodView
			pendingTrnc bool
			pendingErr  error
			metrics     *k8s.KarpenterMetrics
			metricsErr  error
		)
		now := func() metav1.Time { return metav1.Now() }

		wg.Add(4)
		go func() {
			defer wg.Done()
			pools, poolsErr = karpenterListPoolsFn(ctx, dyn)
		}()
		go func() {
			defer wg.Done()
			claims, claimsErr = karpenterListClaimsFn(ctx, dyn)
		}()
		go func() {
			defer wg.Done()
			pending, pendingTrnc, pendingErr = karpenterListPendingFn(ctx, cs, now)
		}()
		go func() {
			defer wg.Done()
			metrics, metricsErr = karpenterScrapeMetricsFn(ctx, cs)
		}()
		wg.Wait()

		// Required legs failing → 5xx with reason. The dashboard makes
		// no sense without NodePools / NodeClaims, so don't paper over.
		if poolsErr != nil {
			emitKarpenterRead(ctx, emitter, c, audit.OutcomeFailure, "list:nodepools_failed", poolsErr.Error())
			writeAPIErrorJSON(w, http.StatusBadGateway, "E_KARPENTER_LIST", poolsErr.Error())
			return
		}
		if claimsErr != nil {
			emitKarpenterRead(ctx, emitter, c, audit.OutcomeFailure, "list:nodeclaims_failed", claimsErr.Error())
			writeAPIErrorJSON(w, http.StatusBadGateway, "E_KARPENTER_LIST", claimsErr.Error())
			return
		}

		// Best-effort legs: log + degrade gracefully.
		if pendingErr != nil {
			slog.WarnContext(ctx, "karpenter pending pods list failed",
				"cluster", c.Name, "err", pendingErr)
			pending = nil
		}
		if metricsErr != nil {
			slog.InfoContext(ctx, "karpenter metrics unavailable; cost summary omitted",
				"cluster", c.Name, "err", metricsErr,
				"timed_out", errors.Is(metricsErr, context.DeadlineExceeded))
		}

		// Cost compute attaches NodePoolCost in place AND fills NodeCount
		// from the claim grouping. Metrics-nil is tolerated; pools without
		// claims get Cost=nil regardless.
		karpenterComputeCostsFn(pools, claims, metrics)

		resp := k8s.KarpenterDashboard{
			Available:        true,
			NodePools:        pools,
			NodeClaims:       claims,
			PendingPods:      pending,
			Truncated:        pendingTrnc,
			MetricsAvailable: metrics != nil,
		}
		writeJSON(w, http.StatusOK, resp)
		emitKarpenterRead(ctx, emitter, c, audit.OutcomeSuccess, "list", "")
	}
}

// emitKarpenterRead centralizes the audit-row construction so every
// path through karpenterHandler emits a row with consistent fields.
// op is one of "available_false" / "detect_failed" /
// "client_build_failed" / "list" / "list:nodepools_failed" /
// "list:nodeclaims_failed" — see the file header for the full
// taxonomy.
func emitKarpenterRead(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, outcome audit.Outcome, op, reason string) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbKarpenterRead,
		Outcome: outcome,
		Cluster: c.Name,
		Reason:  reason,
		Extra:   map[string]any{"op": op},
	})
}
