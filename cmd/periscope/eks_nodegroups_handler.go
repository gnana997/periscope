package main

// eks_nodegroups_handler.go — read-only EKS managed node group
// endpoints.
//
//   GET /api/clusters/{cluster}/eks/nodegroups
//   GET /api/clusters/{cluster}/eks/nodegroups/{name}
//
// Wraps eks.ListNodegroups + eks.DescribeNodegroup. Same EKS-only
// gating + audit pattern as eks_insights_handler.go: 422 +
// E_BACKEND_NOT_EKS for non-EKS, 502 for AWS failures, audit row on
// every call regardless of outcome.
//
// PR-2 scope (this commit): nodegroup list with current AMI release
// version + custom-AMI flag. NO drift computation — the
// DriftComputed flag is always false on responses produced here.
// PR-3 wires the SSM-based "latest AMI" lookup and fills the drift
// fields onto the response.
//
// Custom AMIs (AmiType == "CUSTOM"): we surface the launch template
// reference and a `customAmi: true` flag so the SPA can render a
// "drift not tracked" badge. PR-3 explicitly skips the drift hop
// for these — when an operator ships a custom image, freshness is
// the operator's responsibility.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
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

// eksNodegroupsListMaxResults caps the AWS-side page size. EKS
// allows up to ~30 nodegroups per cluster in practice; 100 is a
// safe ceiling. We do not paginate — same rationale as the insights
// list handler: a NextToken response is logged as a truncation
// warning rather than silently fetching every page (which would
// make the cache TTL story confusing).
const eksNodegroupsListMaxResults = 100

// driftLookupTimeout caps a single SSM/EC2 call. A misbehaving SSM
// in one region must not stall the whole list response — we'd
// rather skip drift on that nodegroup than block the page.
const driftLookupTimeout = 4 * time.Second

// eksNodegroupsAPI is the SDK seam for testability. The real client
// returned by eks.NewFromConfig satisfies this implicitly.
type eksNodegroupsAPI interface {
	ListNodegroups(ctx context.Context, in *eks.ListNodegroupsInput, opts ...func(*eks.Options)) (*eks.ListNodegroupsOutput, error)
	DescribeNodegroup(ctx context.Context, in *eks.DescribeNodegroupInput, opts ...func(*eks.Options)) (*eks.DescribeNodegroupOutput, error)
}

// newEKSNodegroupsClient is swapped by tests for a fake. Default
// builds the real eks.Client from the request's Provider — same
// shape as newEKSInsightsClient.
var newEKSNodegroupsClient = defaultNewEKSNodegroupsClient

func defaultNewEKSNodegroupsClient(p credentials.Provider, c clusters.Cluster) eksNodegroupsAPI {
	return eks.NewFromConfig(aws.Config{
		Region:      c.Region,
		Credentials: p,
	})
}

// ── Wire types ───────────────────────────────────────────────────────

// NodegroupSummary is one row in the list response. The drift fields
// are present on the wire so the SPA can pre-allocate columns; in
// PR-2 they always come back zero/false (DriftComputed=false).
type NodegroupSummary struct {
	Name              string `json:"name"`
	Status            string `json:"status"`
	AmiType           string `json:"amiType"`
	CapacityType      string `json:"capacityType,omitempty"`
	KubernetesVersion string `json:"kubernetesVersion,omitempty"`
	ReleaseVersion    string `json:"releaseVersion,omitempty"`
	// CustomAMI is true iff AmiType == "CUSTOM". Surfaced explicitly
	// so the SPA doesn't need to pattern-match the AmiType string.
	CustomAMI bool `json:"customAmi"`
	// InstanceTypesPreview is at most three instance type strings
	// joined with ", ", suitable for a single table cell. The full
	// list is on the detail blob.
	InstanceTypesPreview string `json:"instanceTypesPreview,omitempty"`
	DesiredSize          int32  `json:"desiredSize"`
	MinSize              int32  `json:"minSize"`
	MaxSize              int32  `json:"maxSize"`
	HealthIssueCount     int    `json:"healthIssueCount"`
	CreatedAt            *time.Time `json:"createdAt,omitempty"`

	// Drift fields. Populated by PR-3; always zero in PR-2.
	DriftComputed       bool   `json:"driftComputed"`
	LatestReleaseVersion string `json:"latestReleaseVersion,omitempty"`
	DaysBehind          int    `json:"daysBehind,omitempty"`
	IsBehind            bool   `json:"isBehind,omitempty"`
}

// NodegroupsListResponse is the GET /eks/nodegroups payload.
type NodegroupsListResponse struct {
	Nodegroups []NodegroupSummary `json:"nodegroups"`
	// CountsBehind / CountsCustom are precomputed for the overview
	// card so it doesn't have to iterate. PR-2: CountsBehind is
	// always 0 because nothing has drift computed yet.
	Counts NodegroupsCounts `json:"counts"`
}

type NodegroupsCounts struct {
	Total   int `json:"total"`
	Behind  int `json:"behind"`
	Custom  int `json:"custom"`
	Healthy int `json:"healthy"`
}

// NodegroupHealthIssue mirrors the AWS shape verbatim for the SPA's
// error panel. Pointer-typed time so "AWS didn't tell us" stays
// distinguishable from the zero time.
type NodegroupHealthIssue struct {
	Code        string   `json:"code,omitempty"`
	Message     string   `json:"message,omitempty"`
	ResourceIDs []string `json:"resourceIds,omitempty"`
}

// LaunchTemplateRef captures the (id, name, version) tuple AWS
// returns when a nodegroup was created from a launch template. For
// CUSTOM AMI nodegroups this is also where the operator specified
// the AMI ID — but we don't fetch the launch template's contents
// (would require ec2:DescribeLaunchTemplateVersions, which is out
// of scope for v1).
type LaunchTemplateRef struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// NodegroupDetail is the GET /eks/nodegroups/{name} payload. Embeds
// the summary so callers that already cached the row from the list
// endpoint don't need to diff every field.
type NodegroupDetail struct {
	NodegroupSummary
	ARN              string                 `json:"arn,omitempty"`
	NodeRole         string                 `json:"nodeRole,omitempty"`
	InstanceTypes    []string               `json:"instanceTypes,omitempty"`
	Subnets          []string               `json:"subnets,omitempty"`
	DiskSize         int32                  `json:"diskSize,omitempty"`
	Labels           map[string]string      `json:"labels,omitempty"`
	Tags             map[string]string      `json:"tags,omitempty"`
	HealthIssues     []NodegroupHealthIssue `json:"healthIssues,omitempty"`
	LaunchTemplate   *LaunchTemplateRef     `json:"launchTemplate,omitempty"`
	ModifiedAt       *time.Time             `json:"modifiedAt,omitempty"`
	AutoScalingGroups []string              `json:"autoScalingGroups,omitempty"`
}

// ── Handlers ─────────────────────────────────────────────────────────

// eksNodegroupsListHandler returns the cluster's managed node group
// list with drift fields folded onto each row. `amiCache` may be
// nil in tests; in that case drift stays uncomputed
// (DriftComputed=false on every row) and the SPA renders "—" in the
// drift column. main.go always passes a non-nil cache so production
// responses populate drift for AWS-managed (non-CUSTOM) nodegroups.
func eksNodegroupsListHandler(reg *clusters.Registry, cache *eksNodegroupsCache, amiCache *amiCatalogCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"managed node group introspection is only available for EKS-backed clusters")
			return
		}

		if cached, ok := cache.GetList(c.Name); ok {
			writeJSON(w, http.StatusOK, *cached)
			emitNodegroupsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "list:cache_hit", "")
			return
		}

		client := newEKSNodegroupsClient(p, c)
		eksName := c.EKSName()

		// Step 1: list names. Step 2: DescribeNodegroup per name in
		// sequence. We don't fan out concurrently — typical clusters
		// have ~5 node groups, AWS rate-limits Describe* on the same
		// connection, and serial keeps the audit log linear.
		names, err := listAllNodegroupNames(r.Context(), client, eksName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks nodegroups list failed", "cluster", c.Name, "err", err)
			emitNodegroupsRead(r.Context(), emitter, c, audit.OutcomeFailure, "list", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to list node groups: "+err.Error())
			return
		}

		summaries := make([]NodegroupSummary, 0, len(names))
		for _, name := range names {
			out, err := client.DescribeNodegroup(r.Context(), &eks.DescribeNodegroupInput{
				ClusterName:   &eksName,
				NodegroupName: &name,
			})
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				// One failed describe shouldn't sink the whole list —
				// surface a placeholder row so the operator knows the
				// nodegroup exists but its details are unavailable.
				slog.Warn("eks nodegroup describe failed (list step)",
					"cluster", c.Name, "nodegroup", name, "err", err)
				summaries = append(summaries, NodegroupSummary{
					Name:   name,
					Status: "DEGRADED_DESCRIBE",
				})
				continue
			}
			if out.Nodegroup != nil {
				summaries = append(summaries, buildNodegroupSummary(out.Nodegroup))
			}
		}

		// Sort: CUSTOM nodegroups first (they're the operator-managed
		// ones the operator likely cares about reviewing manually),
		// then alphabetical. The SPA renders the order verbatim.
		sort.SliceStable(summaries, func(i, j int) bool {
			if summaries[i].CustomAMI != summaries[j].CustomAMI {
				return summaries[i].CustomAMI
			}
			return summaries[i].Name < summaries[j].Name
		})

		// Drift fold-in. AmiCache being non-nil is the gate; CUSTOM
		// rows are skipped inside applyDrift. The catalog client is
		// built once per request and reused across rows so the
		// 30min cache amortizes calls within the same fleet view.
		//
		// Fan-out is parallel: each applyDrift carries its own
		// driftLookupTimeout (4s), the cache is sync-safe via its
		// own mutex, and iterations write to disjoint &summaries[i]
		// slots — no race window. Worst-case wall-clock collapses
		// from N×SSM-latency to one SSM-latency on a cold cache.
		// Mirrors fleet_handler.go's WaitGroup pattern; no errgroup
		// dependency.
		if amiCache != nil {
			catalogClient := newAMICatalogClient(p, c)
			var wg sync.WaitGroup
			for i := range summaries {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					applyDrift(r.Context(), &summaries[i], catalogClient, amiCache)
				}(i)
			}
			wg.Wait()
		}

		resp := NodegroupsListResponse{
			Nodegroups: summaries,
			Counts:     buildNodegroupsCounts(summaries),
		}
		cache.PutList(c.Name, resp)
		writeJSON(w, http.StatusOK, resp)
		emitNodegroupsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "list", "")
	}
}

// eksNodegroupsGetHandler is the per-nodegroup detail handler.
// Same amiCache nil-handling contract as the list handler.
func eksNodegroupsGetHandler(reg *clusters.Registry, cache *eksNodegroupsCache, amiCache *amiCatalogCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity,
				errBackendNotEKSCode,
				"managed node group introspection is only available for EKS-backed clusters")
			return
		}
		name := chi.URLParam(r, "name")
		if name == "" {
			http.Error(w, "missing node group name", http.StatusBadRequest)
			return
		}

		if cached, ok := cache.GetDetail(c.Name, name); ok {
			writeJSON(w, http.StatusOK, *cached)
			emitNodegroupsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "detail:cache_hit", "")
			return
		}

		client := newEKSNodegroupsClient(p, c)
		eksName := c.EKSName()
		out, err := client.DescribeNodegroup(r.Context(), &eks.DescribeNodegroupInput{
			ClusterName:   &eksName,
			NodegroupName: &name,
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("eks nodegroup describe failed",
				"cluster", c.Name, "nodegroup", name, "err", err)
			emitNodegroupsRead(r.Context(), emitter, c, audit.OutcomeFailure, "detail", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code,
				"failed to describe node group: "+err.Error())
			return
		}
		if out.Nodegroup == nil {
			emitNodegroupsRead(r.Context(), emitter, c, audit.OutcomeFailure, "detail", "empty response")
			writeAPIErrorJSON(w, http.StatusBadGateway, "E_AWS_API",
				"DescribeNodegroup returned empty response")
			return
		}

		detail := buildNodegroupDetail(out.Nodegroup)
		if amiCache != nil {
			catalogClient := newAMICatalogClient(p, c)
			applyDrift(r.Context(), &detail.NodegroupSummary, catalogClient, amiCache)
		}
		cache.PutDetail(c.Name, name, detail)
		writeJSON(w, http.StatusOK, detail)
		emitNodegroupsRead(r.Context(), emitter, c, audit.OutcomeSuccess, "detail", "")
	}
}

// applyDrift folds the catalog lookup onto a nodegroup summary in
// place. CUSTOM AMIs are skipped — the SPA renders "drift not
// tracked" for them based on CustomAMI=true. Errors are logged and
// leave DriftComputed=false; the nodegroup row simply shows "—" in
// the drift column. The per-row cap on AWS calls bounds the
// fleet-view fan-out.
func applyDrift(parent context.Context, ng *NodegroupSummary, client amiCatalogAPI, cache *amiCatalogCache) {
	if ng.CustomAMI {
		return
	}
	amiType := ekstypes.AMITypes(ng.AmiType)
	k8s := ng.KubernetesVersion
	if k8s == "" {
		return
	}

	cached, cachedErr, hit := cache.Get(amiType, k8s)
	if !hit {
		ctx, cancel := context.WithTimeout(parent, driftLookupTimeout)
		defer cancel()
		latest, err := latestForNodegroup(ctx, client, amiType, k8s)
		cache.Put(amiType, k8s, latest, err)
		cached, cachedErr = latest, err
	}
	if cachedErr != nil || cached == nil {
		return
	}

	d := computeDrift(ng.ReleaseVersion, cached)
	ng.DriftComputed = true
	ng.LatestReleaseVersion = d.LatestReleaseVersion
	ng.IsBehind = d.IsBehind
	ng.DaysBehind = d.DaysBehind
}

// ── SDK → wire mapping ───────────────────────────────────────────────

func listAllNodegroupNames(ctx context.Context, client eksNodegroupsAPI, clusterName string) ([]string, error) {
	out, err := client.ListNodegroups(ctx, &eks.ListNodegroupsInput{
		ClusterName: &clusterName,
		MaxResults:  int32Ptr(eksNodegroupsListMaxResults),
	})
	if err != nil {
		return nil, err
	}
	if out.NextToken != nil && *out.NextToken != "" {
		// Same truncation policy as eks_insights_handler.go: log and
		// return what we got. >100 nodegroups in one cluster is
		// outside the realistic operator scenario this UI targets.
		slog.Warn("eks nodegroups list truncated; not paginating",
			"cluster", clusterName, "page_size", eksNodegroupsListMaxResults)
	}
	return out.Nodegroups, nil
}

func buildNodegroupSummary(in *ekstypes.Nodegroup) NodegroupSummary {
	amiType := string(in.AmiType)
	out := NodegroupSummary{
		Name:              deref(in.NodegroupName),
		Status:            string(in.Status),
		AmiType:           amiType,
		CapacityType:      string(in.CapacityType),
		KubernetesVersion: deref(in.Version),
		ReleaseVersion:    deref(in.ReleaseVersion),
		CustomAMI:         amiType == string(ekstypes.AMITypesCustom),
		CreatedAt:         in.CreatedAt,
	}
	if in.ScalingConfig != nil {
		out.DesiredSize = derefInt32(in.ScalingConfig.DesiredSize)
		out.MinSize = derefInt32(in.ScalingConfig.MinSize)
		out.MaxSize = derefInt32(in.ScalingConfig.MaxSize)
	}
	if len(in.InstanceTypes) > 0 {
		out.InstanceTypesPreview = joinPreview(in.InstanceTypes, 3)
	}
	if in.Health != nil {
		out.HealthIssueCount = len(in.Health.Issues)
	}
	return out
}

func buildNodegroupDetail(in *ekstypes.Nodegroup) NodegroupDetail {
	out := NodegroupDetail{
		NodegroupSummary: buildNodegroupSummary(in),
		ARN:              deref(in.NodegroupArn),
		NodeRole:         deref(in.NodeRole),
		InstanceTypes:    in.InstanceTypes,
		Subnets:          in.Subnets,
		Labels:           in.Labels,
		Tags:             in.Tags,
		ModifiedAt:       in.ModifiedAt,
	}
	if in.DiskSize != nil {
		out.DiskSize = *in.DiskSize
	}
	if in.LaunchTemplate != nil {
		out.LaunchTemplate = &LaunchTemplateRef{
			ID:      deref(in.LaunchTemplate.Id),
			Name:    deref(in.LaunchTemplate.Name),
			Version: deref(in.LaunchTemplate.Version),
		}
	}
	if in.Health != nil {
		for _, issue := range in.Health.Issues {
			out.HealthIssues = append(out.HealthIssues, NodegroupHealthIssue{
				Code:        string(issue.Code),
				Message:     deref(issue.Message),
				ResourceIDs: issue.ResourceIds,
			})
		}
	}
	if in.Resources != nil {
		for _, asg := range in.Resources.AutoScalingGroups {
			out.AutoScalingGroups = append(out.AutoScalingGroups, deref(asg.Name))
		}
	}
	return out
}

func buildNodegroupsCounts(summaries []NodegroupSummary) NodegroupsCounts {
	c := NodegroupsCounts{Total: len(summaries)}
	for _, s := range summaries {
		if s.CustomAMI {
			c.Custom++
		}
		if s.IsBehind {
			c.Behind++
		}
		if s.HealthIssueCount == 0 && s.Status == string(ekstypes.NodegroupStatusActive) {
			c.Healthy++
		}
	}
	return c
}

// ── Helpers ──────────────────────────────────────────────────────────

func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// joinPreview joins up to N strings with ", "; if the slice is
// longer than N, appends " +M more". Used for the table cell
// preview so a 12-instance-type nodegroup doesn't blow up the row.
func joinPreview(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + " +" + strconv.Itoa(len(items)-n) + " more"
}

func emitNodegroupsRead(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, outcome audit.Outcome, op, reason string) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbEKSNodegroupsRead,
		Outcome: outcome,
		Cluster: c.Name,
		Reason:  reason,
		Extra: map[string]any{
			"op": op,
		},
	})
}

