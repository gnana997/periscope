// Package awsinspector wraps the AWS Inspector v2 SDK with the
// minimal surface the CVE module needs: probe-if-enabled, list findings
// by EC2 instance ID, list findings by ECR image digest.
//
// All Inspector v2 list operations live behind a tiny interface (API)
// so unit tests can stub the SDK without making real AWS calls.
//
// Batching: the SDK's ListFindings takes a single FilterCriteria; we
// chunk the input ID list into groups of 50 (the BatchGet hard cap,
// reused here for symmetry with the rest of the CVE module) and run
// one paginated ListFindings call per chunk, merging results.
package awsinspector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/inspector2"
	itypes "github.com/aws/aws-sdk-go-v2/service/inspector2/types"
	"github.com/aws/smithy-go"
)

// BatchSize is the per-call ID chunk size. Inspector's BatchGet APIs
// cap at 50; we apply the same ceiling to ListFindings filter
// cardinality so a 1k-instance cluster turns into 20 paginated calls
// instead of one giant 1k-entry filter (which would either be rejected
// or generate a very large response payload).
const BatchSize = 50

// Finding is the flat CVE record the CVE store keeps in memory. It is
// the package's public DTO; callers should never touch the SDK types
// directly.
//
// ResourceID is the join key the cve store uses to bucket findings
// into per-resource entries: the EC2 instance ID (i-…) for instance
// findings, the bare ECR image hash (sha256:…) for image findings.
// Without it, every entry would receive every finding (the very bug
// the v1.1 review caught).
//
// PackageName / PackageVersion / FixedVersion summarise the
// VulnerablePackages slice on the SDK finding. When a single finding
// covers more than one package (e.g. openssl + libcrypto), package
// names are concatenated with a comma so the chip surface does not
// silently under-report; PackageVersion / FixedVersion track the
// first package's values so the existing detail-drawer copy keeps
// working.
//
// Description / Remediation / EPSSScore / ExploitAvailable / FixAvailable
// carry the operator-actionable detail Inspector ships beyond the chip
// surface — surfaced by the drill-down endpoints (#165) so the SPA can
// render "what is this CVE" + "how do I fix it" inline without a second
// Inspector round-trip.
type Finding struct {
	ResourceID      string    `json:"resourceId"`
	ARN             string    `json:"arn"`
	Title           string    `json:"title"`
	CVE             string    `json:"cve"`
	Severity        string    `json:"severity"`
	CVSSv3Score     float64   `json:"cvssV3Score"`
	PackageName     string    `json:"packageName"`
	PackageVersion  string    `json:"packageVersion"`
	FixedVersion    string    `json:"fixedVersion"`
	FirstObservedAt time.Time `json:"firstObservedAt"`
	LastObservedAt  time.Time `json:"lastObservedAt"`
	Description     string    `json:"description,omitempty"`
	Remediation     string    `json:"remediation,omitempty"`     // Inspector recommendation text
	RemediationURL  string    `json:"remediationUrl,omitempty"`  // CVE vendor / NVD link
	EPSSScore       float64   `json:"epssScore,omitempty"`       // Exploit Prediction Scoring System probability
	ExploitAvailable string   `json:"exploitAvailable,omitempty"` // YES / NO / empty
	FixAvailable    string    `json:"fixAvailable,omitempty"`    // YES / NO / PARTIAL / empty
	InspectorURL    string    `json:"inspectorUrl"`
}

// API is the subset of the Inspector v2 client surface this package
// uses. Defined as an interface so tests can substitute a stub.
type API interface {
	ListFindings(ctx context.Context, in *inspector2.ListFindingsInput, opts ...func(*inspector2.Options)) (*inspector2.ListFindingsOutput, error)
	ListCoverage(ctx context.Context, in *inspector2.ListCoverageInput, opts ...func(*inspector2.Options)) (*inspector2.ListCoverageOutput, error)
}

// Client is a thin wrapper around the Inspector v2 SDK with batching
// + pagination baked in.
type Client struct {
	api    API
	region string
}

// New builds a Client backed by the live SDK using the given aws.Config.
// The Region carried on the Config determines which Inspector regional
// endpoint is dialled and which AWS console deep-link is built into
// each Finding's InspectorURL.
func New(cfg aws.Config) *Client {
	return &Client{api: inspector2.NewFromConfig(cfg), region: cfg.Region}
}

// NewWithAPI is the test seam — production code calls New.
func NewWithAPI(api API, region string) *Client {
	return &Client{api: api, region: region}
}

// IsEnabled reports whether Inspector v2 is enabled in the caller's
// account. Probes via ListCoverage with a 1-page limit. Returns
// (false, nil) when the IAM role lacks inspector2:* (AccessDenied) —
// that's the most common "not enabled yet" signal during a v1.0.x →
// v1.1 upgrade where the operator has not added the new IAM
// permissions. Any other error is surfaced so the caller can log it.
func (c *Client) IsEnabled(ctx context.Context) (bool, error) {
	maxResults := int32(1)
	out, err := c.api.ListCoverage(ctx, &inspector2.ListCoverageInput{MaxResults: &maxResults})
	if err != nil {
		if isAccessDenied(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspector list coverage: %w", err)
	}
	// An account with Inspector enabled but no covered resources is
	// still a healthy "enabled" state — the store will populate as
	// resources start being scanned. Don't treat empty CoveredResources
	// as disabled.
	_ = out
	return true, nil
}

// ListFindingsByInstance returns findings grouped by EC2 instance ID.
// Inputs are deduped and batched into BatchSize-sized chunks; each
// chunk is paginated end-to-end before the next chunk starts. Every
// requested ID is present in the result map (with an empty slice
// when Inspector returned no findings for it) so callers can iterate
// the input list and trust the lookup.
func (c *Client) ListFindingsByInstance(ctx context.Context, instanceIDs []string) (map[string][]Finding, error) {
	ids := dedup(instanceIDs)
	out, err := c.listByIDs(ctx, ids, filterByInstance)
	ensureKeys(out, ids)
	return out, err
}

// ListFindingsByImageDigest returns findings grouped by ECR image
// digest (the bare sha256:... hash, not the docker-pullable:// URI).
// The caller is responsible for stripping the prefix before calling
// — see (cve).normalizeImageID. Same per-key guarantee as
// ListFindingsByInstance.
func (c *Client) ListFindingsByImageDigest(ctx context.Context, digests []string) (map[string][]Finding, error) {
	ids := dedup(digests)
	out, err := c.listByIDs(ctx, ids, filterByDigest)
	ensureKeys(out, ids)
	return out, err
}

// listByIDs is the shared batching + pagination loop. mkFilter builds
// the FilterCriteria for a chunk of IDs. Returns findings grouped by
// their ResourceID (each SDK finding is fanned out across its
// Resources[] entries, see projectFinding).
func (c *Client) listByIDs(ctx context.Context, ids []string, mkFilter func([]string) *itypes.FilterCriteria) (map[string][]Finding, error) {
	out := make(map[string][]Finding)
	if len(ids) == 0 {
		return out, nil
	}
	for chunk := range chunked(ids, BatchSize) {
		input := &inspector2.ListFindingsInput{FilterCriteria: mkFilter(chunk)}
		paginator := inspector2.NewListFindingsPaginator(c.api, input)
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				// AccessDenied mid-fetch matches the "Inspector got
				// disabled" race. Treat the same as IsEnabled=false:
				// return what we have and an error the manager can
				// log and swallow.
				return out, fmt.Errorf("inspector list findings: %w", err)
			}
			for i := range page.Findings {
				for _, f := range projectFinding(page.Findings[i], c.region) {
					out[f.ResourceID] = append(out[f.ResourceID], f)
				}
			}
		}
	}
	return out, nil
}

// ensureKeys guarantees every requested ID is present in m, with an
// empty slice when Inspector returned no findings. Callers iterating
// the input list (for upserts) can then trust m[id] without a nil
// check.
func ensureKeys(m map[string][]Finding, ids []string) {
	for _, id := range ids {
		if _, ok := m[id]; !ok {
			m[id] = nil
		}
	}
}

// filterByInstance builds a FilterCriteria matching findings whose
// resource is one of the given EC2 instance IDs. StringFilter entries
// in the same field are OR'd by the API.
func filterByInstance(instanceIDs []string) *itypes.FilterCriteria {
	filters := make([]itypes.StringFilter, 0, len(instanceIDs))
	for _, id := range instanceIDs {
		id := id
		filters = append(filters, itypes.StringFilter{
			Comparison: itypes.StringComparisonEquals,
			Value:      &id,
		})
	}
	return &itypes.FilterCriteria{ResourceId: filters}
}

// filterByDigest builds a FilterCriteria matching findings on ECR
// image hashes. Inspector exposes the digest as EcrImageHash on the
// finding's resource details.
func filterByDigest(digests []string) *itypes.FilterCriteria {
	filters := make([]itypes.StringFilter, 0, len(digests))
	for _, d := range digests {
		d := d
		filters = append(filters, itypes.StringFilter{
			Comparison: itypes.StringComparisonEquals,
			Value:      &d,
		})
	}
	return &itypes.FilterCriteria{EcrImageHash: filters}
}

// projectFinding flattens an Inspector v2 SDK finding into our DTOs,
// returning ONE Finding per Resources[] entry on the source. A
// finding with two resources (rare, but happens when a CVE matches
// the same package on two co-scanned images) yields two store rows
// with the shared CVE metadata and per-resource ResourceID. Findings
// with zero recognisable resources are skipped — the store has no
// meaningful key to file them under.
func projectFinding(f itypes.Finding, region string) []Finding {
	if len(f.Resources) == 0 {
		return nil
	}
	base := Finding{
		ARN:      deref(f.FindingArn),
		Title:    deref(f.Title),
		Severity: string(f.Severity),
	}
	if f.FirstObservedAt != nil {
		base.FirstObservedAt = *f.FirstObservedAt
	}
	if f.LastObservedAt != nil {
		base.LastObservedAt = *f.LastObservedAt
	}
	if vd := f.PackageVulnerabilityDetails; vd != nil {
		base.CVE = deref(vd.VulnerabilityId)
		// Pick the highest score across reported sources so the
		// store keeps the worst case — operators want the chip to
		// reflect "how bad is this", not "what does NVD think
		// specifically". Source-attribution lives on the detail
		// drawer in v1.2.
		for _, s := range vd.Cvss {
			if s.BaseScore == nil {
				continue
			}
			if *s.BaseScore > base.CVSSv3Score {
				base.CVSSv3Score = *s.BaseScore
			}
		}
		// One finding can list multiple vulnerable packages; concat
		// names with a comma so the chip surface doesn't silently
		// hide co-vulnerable libs (e.g. openssl + libcrypto). Keep
		// the first package's version + fix on the flat fields so
		// the existing single-package UI still renders.
		if len(vd.VulnerablePackages) > 0 {
			names := make([]string, 0, len(vd.VulnerablePackages))
			for _, p := range vd.VulnerablePackages {
				if n := deref(p.Name); n != "" {
					names = append(names, n)
				}
			}
			base.PackageName = strings.Join(names, ", ")
			first := vd.VulnerablePackages[0]
			base.PackageVersion = deref(first.Version)
			base.FixedVersion = deref(first.FixedInVersion)
		}
	}
	base.InspectorURL = buildConsoleURL(region, deref(f.FindingArn))

	// Operator-actionable detail beyond the chip surface.
	base.Description = deref(f.Description)
	if f.Remediation != nil && f.Remediation.Recommendation != nil {
		base.Remediation = deref(f.Remediation.Recommendation.Text)
		base.RemediationURL = deref(f.Remediation.Recommendation.Url)
	}
	if f.Epss != nil {
		base.EPSSScore = f.Epss.Score
	}
	base.ExploitAvailable = string(f.ExploitAvailable)
	base.FixAvailable = string(f.FixAvailable)

	out := make([]Finding, 0, len(f.Resources))
	for _, r := range f.Resources {
		id := resourceJoinKey(r)
		if id == "" {
			continue
		}
		c := base
		c.ResourceID = id
		out = append(out, c)
	}
	return out
}

// resourceJoinKey returns the cve-store join key for an Inspector
// resource entry: instance ID for EC2, bare image hash for ECR,
// empty otherwise (caller skips). Mirrors the Resource.Type → store
// bucket the cve module already uses.
func resourceJoinKey(r itypes.Resource) string {
	switch r.Type {
	case itypes.ResourceTypeAwsEcrContainerImage:
		if r.Details != nil && r.Details.AwsEcrContainerImage != nil {
			return deref(r.Details.AwsEcrContainerImage.ImageHash)
		}
	case itypes.ResourceTypeAwsEc2Instance:
		return deref(r.Id)
	}
	return ""
}

// buildConsoleURL produces an AWS console deep-link to the finding.
// Format mirrors the live console as of 2026; falls back to the
// Inspector landing page when the finding ARN is empty.
func buildConsoleURL(region, findingArn string) string {
	if region == "" {
		region = "us-east-1"
	}
	if findingArn == "" {
		return fmt.Sprintf("https://%s.console.aws.amazon.com/inspector/v2/home?region=%s", region, region)
	}
	return fmt.Sprintf("https://%s.console.aws.amazon.com/inspector/v2/home?region=%s#/findings?findingArn=%s",
		region, region, findingArn)
}

// isAccessDenied recognises the SDK shape both for the typed
// AccessDeniedException and the generic ErrorCode form returned by
// some endpoints when the IAM role is missing inspector2 permissions.
func isAccessDenied(err error) bool {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		switch ae.ErrorCode() {
		case "AccessDeniedException", "AccessDenied", "UnauthorizedException":
			return true
		}
	}
	return false
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// dedup returns the input slice with duplicate entries removed, order
// preserved. Inspector calls cost real money per scan; sending the
// same ID twice in one filter is wasted bytes.
func dedup(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// chunked yields successive size-len subslices of s. Implemented as a
// range-func iterator (Go 1.23+) so call sites read like a for-range
// without an index-math helper.
func chunked[T any](s []T, size int) func(yield func([]T) bool) {
	return func(yield func([]T) bool) {
		for i := 0; i < len(s); i += size {
			end := i + size
			if end > len(s) {
				end = len(s)
			}
			if !yield(s[i:end]) {
				return
			}
		}
	}
}
