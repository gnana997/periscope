package main

// eks_insights_handler.go — read-only EKS Upgrade Insights endpoints.
//
//   GET /api/clusters/{cluster}/eks/upgrade-insights
//   GET /api/clusters/{cluster}/eks/upgrade-insights/{insightId}
//
// Both wrap AWS EKS SDK calls (ListInsights and DescribeInsight)
// scoped to the UPGRADE_READINESS category. The unique-to-Periscope
// move is that each affected resource on the detail blob is decorated
// with an `editorPath` field — a deep link into the SPA's existing
// per-kind list page with the YAML tab pre-selected. AWS Console
// can't do that; kubectl can't do that. "From scan to fix in one
// click" is the differentiator.
//
// Both endpoints are EKS-only by design. For non-EKS clusters the
// surface returns 422 + a stable `E_BACKEND_NOT_EKS` code so the SPA
// can render a clear empty state ("upgrade insights are an EKS
// feature; this cluster's backend is `agent`") instead of a generic
// red banner. 404 would be wrong — the cluster exists, the surface
// just doesn't apply.
//
// Audit: every read emits a VerbEKSInsightsRead row regardless of
// outcome (success / failure). Compliance reviewers asked for a
// record that an operator checked upgrade readiness before a
// version bump, so the read itself has to be auditable. The verb
// is documented in internal/audit/event.go as the first read verb;
// this handler is the only emission site today.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/go-chi/chi/v5"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
)

// eksInsightsListMaxResults caps the AWS-side page size. The
// UPGRADE_READINESS category produces a small fixed set of insights
// per cluster (~30) so a single page covers every real cluster.
// We do not paginate; if AWS ever returns a NextToken the handler
// returns what it has and logs a warning — the alternative
// (silently fetching every page) makes the cache TTL story
// confusing because partial results would be the new normal.
const eksInsightsListMaxResults = 100

// errBackendNotEKSCode is the stable string the SPA matches on to
// render the non-EKS empty state. Treat as part of the public API
// once shipped — renaming requires a coordinated SPA release.
const errBackendNotEKSCode = "E_BACKEND_NOT_EKS"

// eksInsightsAPI is the subset of the EKS SDK this handler depends
// on. Defining a narrow interface keeps the test seam tiny — the
// real client (eks.NewFromConfig(...)) satisfies it implicitly.
type eksInsightsAPI interface {
	ListInsights(ctx context.Context, in *eks.ListInsightsInput, opts ...func(*eks.Options)) (*eks.ListInsightsOutput, error)
	DescribeInsight(ctx context.Context, in *eks.DescribeInsightInput, opts ...func(*eks.Options)) (*eks.DescribeInsightOutput, error)
}

// newEKSInsightsClient is swapped by tests for a fake. The default
// builds a real eks.Client from the request's Provider — same
// shape used in internal/k8s/client.go's buildEKSRestConfig.
var newEKSInsightsClient = defaultNewEKSInsightsClient

func defaultNewEKSInsightsClient(p credentials.Provider, c clusters.Cluster) eksInsightsAPI {
	return eks.NewFromConfig(aws.Config{
		Region:      c.Region,
		Credentials: p,
	})
}

// ── Wire types ───────────────────────────────────────────────────────

// UpgradeInsightCounts is the bucket header the SPA renders on the
// overview card and the tab. Keeping it server-computed means the
// summary stays consistent across browsers / refreshes / the card
// vs. the tab. AWS does not return a precomputed count.
type UpgradeInsightCounts struct {
	Passing int `json:"passing"`
	Warning int `json:"warning"`
	Error   int `json:"error"`
	Unknown int `json:"unknown"`
}

// UpgradeInsightSummary is one row in the list response — and the
// embedded base of UpgradeInsightDetail so the detail endpoint
// returns a strict superset of fields. Pointer-typed timestamps so
// "AWS didn't tell us" stays distinguishable from the zero time.
type UpgradeInsightSummary struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Category           string     `json:"category"`
	KubernetesVersion  string     `json:"kubernetesVersion,omitempty"`
	Status             string     `json:"status"`
	StatusReason       string     `json:"statusReason,omitempty"`
	LastRefreshTime    *time.Time `json:"lastRefreshTime,omitempty"`
	LastTransitionTime *time.Time `json:"lastTransitionTime,omitempty"`
	Description        string     `json:"description,omitempty"`
}

// UpgradeInsightsListResponse is the GET /upgrade-insights payload.
type UpgradeInsightsListResponse struct {
	Insights []UpgradeInsightSummary `json:"insights"`
	Counts   UpgradeInsightCounts    `json:"counts"`
	// TargetKubernetesVersion is the version most insights point at,
	// surfaced on the card. Picked as the modal value across the
	// list; ties broken in favor of the highest version. Empty when
	// no insight reports a version (which would be a degenerate AWS
	// response but is permitted by the schema).
	TargetKubernetesVersion string `json:"targetKubernetesVersion,omitempty"`
}

// UpgradeInsightResourceRef is one affected K8s object. The raw
// `kubernetesResourceUri` is preserved verbatim so the SPA can show
// it as a tooltip even when we couldn't parse it; the parsed pieces
// and `editorPath` are populated only on a clean parse.
type UpgradeInsightResourceRef struct {
	KubernetesResourceURI string `json:"kubernetesResourceUri"`
	ARN                   string `json:"arn,omitempty"`
	Group                 string `json:"group,omitempty"`
	Version               string `json:"version,omitempty"`
	Resource              string `json:"resource,omitempty"`
	Namespace             string `json:"namespace,omitempty"`
	Name                  string `json:"name,omitempty"`
	EditorPath            string `json:"editorPath,omitempty"`
	Status                string `json:"status,omitempty"`
	StatusReason          string `json:"statusReason,omitempty"`
}

// DeprecationDetail mirrors the AWS shape, with timestamps repointed
// from *time.Time so JSON encoding emits ISO-8601 instead of the
// SDK's pointer indirection.
type DeprecationDetail struct {
	Usage                          string                  `json:"usage,omitempty"`
	ReplacedWith                   string                  `json:"replacedWith,omitempty"`
	StopServingVersion             string                  `json:"stopServingVersion,omitempty"`
	StartServingReplacementVersion string                  `json:"startServingReplacementVersion,omitempty"`
	ClientStats                    []DeprecationClientStat `json:"clientStats,omitempty"`
}

// DeprecationClientStat is per-user-agent traffic the apiserver saw
// against a deprecated API. Useful for "this controller is the one
// still calling v1beta1 PSP" debugging.
type DeprecationClientStat struct {
	UserAgent                  string     `json:"userAgent,omitempty"`
	NumberOfRequestsLast30Days int32      `json:"numberOfRequestsLast30Days,omitempty"`
	LastRequestTime            *time.Time `json:"lastRequestTime,omitempty"`
}

// UpgradeInsightDetail is the GET /upgrade-insights/{id} payload.
// Embeds the summary so a client that already has a row from the
// list endpoint can detect whether to re-render based on summary
// fields without diff-ing every detail field.
type UpgradeInsightDetail struct {
	UpgradeInsightSummary
	Recommendation     string                      `json:"recommendation,omitempty"`
	AdditionalInfo     map[string]string           `json:"additionalInfo,omitempty"`
	Resources          []UpgradeInsightResourceRef `json:"resources"`
	DeprecationDetails []DeprecationDetail         `json:"deprecationDetails,omitempty"`
}

// apiError is the structured error envelope used for the non-EKS
// branch. Mirrors the http.Error pattern other handlers use but
// adds a stable `code` so the SPA can branch on E_BACKEND_NOT_EKS
// without parsing free-form English.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeAPIErrorJSON(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiError{Code: code, Message: msg})
}

// ── Handlers ─────────────────────────────────────────────────────────

// eksInsightsListHandler returns the cluster's UPGRADE_READINESS
// insights with precomputed bucket counts. Cache-first; cache miss
// invokes ListInsights with the category filter.
func eksInsightsListHandler(reg *clusters.Registry, cache *eksInsightsCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !isEKSBackend(c) {
			// No audit row here — the backend mismatch is a SPA-side
			// branch, not a privileged action that needs a forensic
			// record. The SPA only hits this endpoint speculatively
			// for non-EKS clusters when the operator clicks the tab.
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"upgrade insights are only available for EKS-backed clusters")
			return
		}

		if cached, ok := cache.GetList(c.Name); ok {
			writeJSON(w, http.StatusOK, *cached)
			emitInsightsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "cache_hit", "")
			return
		}

		client := newEKSInsightsClient(p, c)
		eksName := c.EKSName()
		out, err := client.ListInsights(r.Context(), &eks.ListInsightsInput{
			ClusterName: &eksName,
			Filter: &ekstypes.InsightsFilter{
				Categories: []ekstypes.Category{ekstypes.CategoryUpgradeReadiness},
			},
			MaxResults: int32Ptr(eksInsightsListMaxResults),
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks insights list failed", "cluster", c.Name, "err", err)
			emitInsightsRead(r.Context(), emitter, c, audit.OutcomeFailure, "list", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to list upgrade insights: "+err.Error())
			return
		}
		if out.NextToken != nil && *out.NextToken != "" {
			// Truncation case. The UPGRADE_READINESS list is small
			// enough that we don't expect this in practice; log so
			// it's visible if AWS ever changes the cardinality.
			slog.Warn("eks insights list truncated; not paginating",
				"cluster", c.Name, "page_size", eksInsightsListMaxResults)
		}

		resp := buildListResponse(out.Insights)
		cache.PutList(c.Name, resp)
		writeJSON(w, http.StatusOK, resp)
		emitInsightsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "list", "")
	}
}

// eksInsightsGetHandler returns the per-insight detail blob with
// per-resource editor links. Same cache backing as the list
// endpoint but keyed by insightId.
func eksInsightsGetHandler(reg *clusters.Registry, cache *eksInsightsCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !isEKSBackend(c) {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"upgrade insights are only available for EKS-backed clusters")
			return
		}
		insightID := chi.URLParam(r, "insightId")
		if insightID == "" {
			http.Error(w, "missing insightId", http.StatusBadRequest)
			return
		}

		if cached, ok := cache.GetDetail(c.Name, insightID); ok {
			writeJSON(w, http.StatusOK, *cached)
			emitInsightsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "detail:cache_hit", "")
			return
		}

		client := newEKSInsightsClient(p, c)
		eksName := c.EKSName()
		out, err := client.DescribeInsight(r.Context(), &eks.DescribeInsightInput{
			ClusterName: &eksName,
			Id:          &insightID,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks insights describe failed",
				"cluster", c.Name, "insight_id", insightID, "err", err)
			emitInsightsRead(r.Context(), emitter, c, audit.OutcomeFailure, "detail", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to describe insight: "+err.Error())
			return
		}
		if out.Insight == nil {
			emitInsightsRead(r.Context(), emitter, c, audit.OutcomeFailure, "detail", "empty response")
			writeAPIErrorJSON(w, http.StatusBadGateway, "E_AWS_API",
				"DescribeInsight returned empty response")
			return
		}

		detail := buildDetailResponse(c.Name, out.Insight)
		cache.PutDetail(c.Name, insightID, detail)
		writeJSON(w, http.StatusOK, detail)
		emitInsightsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "detail", "")
	}
}

// ── SDK → wire mapping ───────────────────────────────────────────────

func buildListResponse(in []ekstypes.InsightSummary) UpgradeInsightsListResponse {
	out := UpgradeInsightsListResponse{
		Insights: make([]UpgradeInsightSummary, 0, len(in)),
	}
	versionVotes := map[string]int{}
	for i := range in {
		s := mapInsightSummary(&in[i])
		out.Insights = append(out.Insights, s)
		switch s.Status {
		case "PASSING":
			out.Counts.Passing++
		case "WARNING":
			out.Counts.Warning++
		case "ERROR":
			out.Counts.Error++
		default:
			out.Counts.Unknown++
		}
		if s.KubernetesVersion != "" {
			versionVotes[s.KubernetesVersion]++
		}
	}
	out.TargetKubernetesVersion = pickModalVersion(versionVotes)
	return out
}

func buildDetailResponse(cluster string, in *ekstypes.Insight) UpgradeInsightDetail {
	out := UpgradeInsightDetail{
		UpgradeInsightSummary: mapInsightSummaryFull(in),
		Recommendation:        deref(in.Recommendation),
		AdditionalInfo:        in.AdditionalInfo,
		Resources:             make([]UpgradeInsightResourceRef, 0, len(in.Resources)),
	}
	for i := range in.Resources {
		out.Resources = append(out.Resources, mapResource(cluster, &in.Resources[i]))
	}
	if in.CategorySpecificSummary != nil {
		for i := range in.CategorySpecificSummary.DeprecationDetails {
			out.DeprecationDetails = append(out.DeprecationDetails,
				mapDeprecationDetail(&in.CategorySpecificSummary.DeprecationDetails[i]))
		}
	}
	return out
}

// summaryFromInsightFields builds the wire summary from already-
// dereffed scalars. Shared between the list ([]InsightSummary) and
// detail (Insight) SDK shapes — neither has a usable interface
// surface, so we pass the fields explicitly instead of reflecting.
// One source of truth for the wire shape; if a field is added to
// UpgradeInsightSummary, only this body needs to change.
func summaryFromInsightFields(
	id, name, k8sVer, description string,
	category ekstypes.Category,
	status *ekstypes.InsightStatus,
	refresh, transition *time.Time,
) UpgradeInsightSummary {
	return UpgradeInsightSummary{
		ID:                 id,
		Name:               name,
		Category:           string(category),
		KubernetesVersion:  k8sVer,
		Status:             insightStatus(status),
		StatusReason:       insightStatusReason(status),
		LastRefreshTime:    refresh,
		LastTransitionTime: transition,
		Description:        description,
	}
}

func mapInsightSummary(in *ekstypes.InsightSummary) UpgradeInsightSummary {
	return summaryFromInsightFields(
		deref(in.Id), deref(in.Name), deref(in.KubernetesVersion), deref(in.Description),
		in.Category, in.InsightStatus, in.LastRefreshTime, in.LastTransitionTime,
	)
}

func mapInsightSummaryFull(in *ekstypes.Insight) UpgradeInsightSummary {
	return summaryFromInsightFields(
		deref(in.Id), deref(in.Name), deref(in.KubernetesVersion), deref(in.Description),
		in.Category, in.InsightStatus, in.LastRefreshTime, in.LastTransitionTime,
	)
}

func mapResource(cluster string, in *ekstypes.InsightResourceDetail) UpgradeInsightResourceRef {
	uri := deref(in.KubernetesResourceUri)
	out := UpgradeInsightResourceRef{
		KubernetesResourceURI: uri,
		ARN:                   deref(in.Arn),
		Status:                insightStatus(in.InsightStatus),
		StatusReason:          insightStatusReason(in.InsightStatus),
	}
	if parsed, ok := parseKubernetesResourceURI(cluster, uri); ok {
		out.Group = parsed.Group
		out.Version = parsed.Version
		out.Resource = parsed.Plural
		out.Namespace = parsed.Namespace
		out.Name = parsed.Name
		out.EditorPath = parsed.EditorPath
	}
	return out
}

func mapDeprecationDetail(in *ekstypes.DeprecationDetail) DeprecationDetail {
	out := DeprecationDetail{
		Usage:                          deref(in.Usage),
		ReplacedWith:                   deref(in.ReplacedWith),
		StopServingVersion:             deref(in.StopServingVersion),
		StartServingReplacementVersion: deref(in.StartServingReplacementVersion),
	}
	for i := range in.ClientStats {
		cs := &in.ClientStats[i]
		out.ClientStats = append(out.ClientStats, DeprecationClientStat{
			UserAgent:                  deref(cs.UserAgent),
			NumberOfRequestsLast30Days: cs.NumberOfRequestsLast30Days,
			LastRequestTime:            cs.LastRequestTime,
		})
	}
	return out
}

// ── Helpers ──────────────────────────────────────────────────────────

func isEKSBackend(c clusters.Cluster) bool {
	// Empty backend defaults to EKS — same convention as
	// internal/k8s/client.go's buildRestConfig switch.
	return c.Backend == clusters.BackendEKS || c.Backend == ""
}

func insightStatus(s *ekstypes.InsightStatus) string {
	if s == nil {
		return "UNKNOWN"
	}
	return string(s.Status)
}

func insightStatusReason(s *ekstypes.InsightStatus) string {
	if s == nil || s.Reason == nil {
		return ""
	}
	return *s.Reason
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func int32Ptr(v int32) *int32 { return &v }

// pickModalVersion returns the version with the highest count.
// Ties broken by lexicographic order of the version string. EKS
// minor versions are 1.27+ in 2026 and AWS does not roll back minors,
// so lex order matches numeric order in practice — this would NOT
// be safe for "1.9" vs "1.10" if EKS ever regressed there. Empty
// when the input is empty.
func pickModalVersion(votes map[string]int) string {
	var best string
	var bestCount int
	for v, c := range votes {
		if c > bestCount || (c == bestCount && v > best) {
			best = v
			bestCount = c
		}
	}
	return best
}

// emitInsightsRead is the single audit-emission helper this handler
// uses. Centralizing the call shape means a future change to the
// row's Extra payload (request_id is already auto-snapped by the
// emitter) lands in one place. Mirrors emitNodegroupsRead — both
// pull the actor from the request context via actorFromContext.
func emitInsightsRead(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, outcome audit.Outcome, op, reason string) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbEKSInsightsRead,
		Outcome: outcome,
		Cluster: c.Name,
		Reason:  reason,
		Extra: map[string]any{
			"op": op,
		},
	})
}
