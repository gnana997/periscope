package main

// fleet_handler.go — GET /api/fleet
//
// Aggregator endpoint behind the home-page Fleet view. Fans out across
// every registered cluster in parallel under user impersonation, calls
// the existing GetClusterSummary collector per cluster, and returns a
// per-cluster status entry plus a rollup.
//
// Failure model:
//   - Page-level 403 only when the user has no tier at all (tier mode +
//     unmapped groups). Every other failure is per-cluster, surfaced on
//     the card so a single broken apiserver never blocks the page.
//   - Per-cluster errors are classified by ErrorCodeFor (errors.go) into
//     a small stable enum: denied | auth_failed | timeout |
//     apiserver_unreachable | unknown.
//   - A 10s TTL fleetCache shields the apiserver from the SPA's 15s
//     polling cadence + intra-tab refetches.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gnana997/periscope/internal/authz"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// FleetResponse is the /api/fleet payload.
type FleetResponse struct {
	Rollup   FleetRollup          `json:"rollup"`
	Clusters []FleetClusterEntry  `json:"clusters"`
}

// FleetRollup is the aggregate counts shown in the page header strip.
type FleetRollup struct {
	TotalClusters int            `json:"totalClusters"`
	ByStatus      map[string]int `json:"byStatus"`
	ByEnvironment map[string]int `json:"byEnvironment"`
	GeneratedAt   time.Time      `json:"generatedAt"`
}

// FleetClusterEntry is one card's worth of data.
type FleetClusterEntry struct {
	Name        string            `json:"name"`
	Backend     string            `json:"backend"`
	Region      string            `json:"region,omitempty"`
	AccountID   string            `json:"accountID,omitempty"`
	Environment string            `json:"environment,omitempty"`
	// Context is the kubeconfig context name. Only populated for kubeconfig
	// backends; empty for EKS.
	Context     string            `json:"context,omitempty"`
	Tags        map[string]string `json:"tags,omitempty"`
	Status      string            `json:"status"`
	LastContact time.Time         `json:"lastContact"`
	Summary     *FleetSummary     `json:"summary,omitempty"`
	HotSignals  []HotSignal       `json:"hotSignals"`
	Error       *FleetError       `json:"error,omitempty"`
}

type FleetSummary struct {
	Nodes         FleetCount   `json:"nodes"`
	Pods          FleetPods    `json:"pods"`
	Namespaces    int          `json:"namespaces"`
	StuckOrFailed int          `json:"stuckOrFailed"`
}

type FleetCount struct {
	Ready int `json:"ready"`
	Total int `json:"total"`
}

type FleetPods struct {
	Running int `json:"running"`
	Pending int `json:"pending"`
	Failed  int `json:"failed"`
	Total   int `json:"total"`
}

type HotSignal struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type FleetError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Fleet status enum. Stable; treat additions as additive.
const (
	FleetStatusHealthy     = "healthy"
	FleetStatusDegraded    = "degraded"
	FleetStatusUnreachable = "unreachable"
	FleetStatusUnknown     = "unknown"
	FleetStatusDenied      = "denied"
)

// fleetHandler returns the registered http.Handler.
//
// resolver is needed for the page-level AllowedTier short-circuit
// (no apiserver round-trips required to detect "your tier is empty").
// cache is the per-actor/cluster TTL cache; constructing it here keeps
// it scoped to one handler instance.
func fleetHandler(reg *clusters.Registry, resolver *authz.Resolver, cache *fleetCache) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		// Page-level deny: tier mode + the user maps to no tier. There
		// is no per-cluster authz signal short of an apiserver call,
		// so this is the only cheap deny we can do up front.
		session := credentials.SessionFromContext(r.Context())
		identity := authz.Identity{Subject: session.Subject, Groups: session.Groups}
		if !resolver.AllowedTier(identity) {
			writeAPIError(w, errors.New("tier_denied: your tier does not grant access to any clusters"), http.StatusForbidden)
			return
		}

		all := reg.List()
		now := time.Now().UTC()

		// Empty registry: emit a well-formed response. The SPA renders
		// the editorial empty state from this.
		if len(all) == 0 {
			writeJSON(w, http.StatusOK, FleetResponse{
				Rollup: FleetRollup{
					TotalClusters: 0,
					ByStatus:      map[string]int{},
					ByEnvironment: map[string]int{},
					GeneratedAt:   now,
				},
				Clusters: []FleetClusterEntry{},
			})
			return
		}

		// Budget: enough for slow clusters to finish but bounded so
		// the SPA's 15s poll never gets stuck on a hung apiserver.
		// Per-cluster soft timeout is 2s; total is 2s + 200ms*N capped
		// at 8s. With N=10 that's 4s; with N=30 it's still 8s.
		totalBudget := 2*time.Second + 200*time.Millisecond*time.Duration(len(all))
		if totalBudget > 8*time.Second {
			totalBudget = 8 * time.Second
		}
		fanCtx, cancel := context.WithTimeout(r.Context(), totalBudget)
		defer cancel()

		results := make([]FleetClusterEntry, len(all))
		var wg sync.WaitGroup
		for i, c := range all {
			wg.Add(1)
			go func(i int, c clusters.Cluster) {
				defer wg.Done()
				clusterCtx, ccancel := context.WithTimeout(fanCtx, 2*time.Second)
				defer ccancel()
				results[i] = collectOne(clusterCtx, c, p, cache, now)
			}(i, c)
		}
		wg.Wait()

		writeJSON(w, http.StatusOK, FleetResponse{
			Rollup:   aggregate(results, now),
			Clusters: results,
		})
	}
}

// collectOne builds one cluster card. Reads the cache first; on miss,
// calls GetClusterSummary and stores the result. Errors are classified
// via ErrorCodeFor — never propagated as a handler-level error.
func collectOne(ctx context.Context, c clusters.Cluster, p credentials.Provider, cache *fleetCache, now time.Time) FleetClusterEntry {
	imp := p.Impersonation()
	if entry, ok := cache.Get(p.Actor(), c.Name, imp.Groups); ok {
		return entry
	}

	entry := FleetClusterEntry{
		Name:        c.Name,
		Backend:     c.Backend,
		Region:      c.Region,
		AccountID:   accountIDFromARN(c.ARN),
		Environment: c.Environment,
		Context:     c.KubeconfigContext,
		Tags:        c.Tags,
		LastContact: now,
		HotSignals:  []HotSignal{},
	}

	summary, err := k8s.GetClusterSummary(ctx, p, k8s.GetClusterSummaryArgs{Cluster: c})
	if err != nil {
		code := ErrorCodeFor(err)
		switch code {
		case "denied":
			entry.Status = FleetStatusDenied
		case "timeout", "unknown":
			entry.Status = FleetStatusUnknown
		default: // auth_failed, apiserver_unreachable
			entry.Status = FleetStatusUnreachable
		}
		entry.Error = &FleetError{Code: code, Message: shortErr(err)}
		// Cache misses too — a flapping unreachable cluster shouldn't
		// be re-probed every render. The 10s TTL is short enough.
		cache.Put(p.Actor(), c.Name, imp.Groups, entry)
		return entry
	}

	entry.Status = deriveStatus(summary)
	entry.Summary = &FleetSummary{
		Nodes: FleetCount{Ready: summary.NodeReadyCount, Total: summary.NodeCount},
		Pods: FleetPods{
			Running: summary.PodPhases.Running,
			Pending: summary.PodPhases.Pending,
			Failed:  summary.PodPhases.Failed,
			Total:   summary.PodCount,
		},
		Namespaces:    summary.NamespaceCount,
		StuckOrFailed: summary.PodPhases.Stuck + summary.PodPhases.Failed,
	}
	entry.HotSignals = summarizeHotSignals(summary.NeedsAttention)

	cache.Put(p.Actor(), c.Name, imp.Groups, entry)
	return entry
}

// deriveStatus turns a successful ClusterSummary into healthy/degraded.
//
// Healthy means every node is ready AND no pod is in the Stuck/Failed
// bucket. Anything short of that is degraded — operators want to see
// the card flag a problem, not have to drill in to discover it.
func deriveStatus(s k8s.ClusterSummary) string {
	if s.NodeCount > 0 && s.NodeReadyCount < s.NodeCount {
		return FleetStatusDegraded
	}
	if s.PodPhases.Stuck+s.PodPhases.Failed > 0 {
		return FleetStatusDegraded
	}
	return FleetStatusHealthy
}

// summarizeHotSignals groups NeedsAttention[] by Reason. The reason
// strings are already canonicalized at the source (failingReasonSet in
// summary.go) so the grouping is a simple counter.
func summarizeHotSignals(failing []k8s.FailingPod) []HotSignal {
	if len(failing) == 0 {
		return []HotSignal{}
	}
	counts := map[string]int{}
	for _, f := range failing {
		counts[f.Reason]++
	}
	out := make([]HotSignal, 0, len(counts))
	for k, v := range counts {
		out = append(out, HotSignal{Kind: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// aggregate computes the page-header rollup counts.
func aggregate(entries []FleetClusterEntry, now time.Time) FleetRollup {
	r := FleetRollup{
		TotalClusters: len(entries),
		ByStatus:      map[string]int{},
		ByEnvironment: map[string]int{},
		GeneratedAt:   now,
	}
	for _, e := range entries {
		r.ByStatus[e.Status]++
		env := e.Environment
		if env == "" {
			env = "other"
		}
		r.ByEnvironment[env]++
	}
	return r
}

// accountIDFromARN parses the AWS account from an EKS ARN. Returns "" for
// non-ARN strings so kubeconfig clusters render without an account chip.
//
// ARN shape: arn:aws:eks:REGION:ACCOUNT:cluster/NAME
func accountIDFromARN(arn string) string {
	if arn == "" {
		return ""
	}
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 5 {
		return ""
	}
	return parts[4]
}

// shortErr trims k8s client-go's verbose error strings to one line for
// the UI. Full error stays in slog for operator triage.
func shortErr(err error) string {
	msg := err.Error()
	if i := strings.IndexByte(msg, '\n'); i != -1 {
		msg = msg[:i]
	}
	if len(msg) > 200 {
		msg = msg[:197] + "..."
	}
	return msg
}

// _ keep slog imported even if all log lines are removed during edits.
var _ = slog.Default
