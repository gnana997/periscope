package awsinspector

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/inspector2"
	itypes "github.com/aws/aws-sdk-go-v2/service/inspector2/types"
	"github.com/aws/smithy-go"
)

// stubAPI is a hand-rolled fake for the inspector2 API. We script the
// pages it returns plus an optional error for one call; that's enough
// for batching, pagination, AccessDenied, and projection coverage.
type stubAPI struct {
	pages         [][]itypes.Finding
	pageIdx       int
	coverageErr   error
	listFindings  int
	listCoverages int

	// captureFilters records the FilterCriteria from each ListFindings
	// call so batching tests can assert chunking.
	captureFilters []*itypes.FilterCriteria
}

func (s *stubAPI) ListFindings(_ context.Context, in *inspector2.ListFindingsInput, _ ...func(*inspector2.Options)) (*inspector2.ListFindingsOutput, error) {
	s.listFindings++
	s.captureFilters = append(s.captureFilters, in.FilterCriteria)
	if s.pageIdx >= len(s.pages) {
		return &inspector2.ListFindingsOutput{}, nil
	}
	out := &inspector2.ListFindingsOutput{Findings: s.pages[s.pageIdx]}
	s.pageIdx++
	if s.pageIdx < len(s.pages) {
		tok := "next"
		out.NextToken = &tok
	}
	return out, nil
}

func (s *stubAPI) ListCoverage(_ context.Context, _ *inspector2.ListCoverageInput, _ ...func(*inspector2.Options)) (*inspector2.ListCoverageOutput, error) {
	s.listCoverages++
	if s.coverageErr != nil {
		return nil, s.coverageErr
	}
	return &inspector2.ListCoverageOutput{}, nil
}

type apiErr struct{ code string }

func (e apiErr) Error() string                 { return e.code }
func (e apiErr) ErrorCode() string             { return e.code }
func (e apiErr) ErrorMessage() string          { return e.code }
func (e apiErr) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func TestIsEnabled_AccessDenied(t *testing.T) {
	api := &stubAPI{coverageErr: apiErr{code: "AccessDeniedException"}}
	c := NewWithAPI(api, "us-east-1")
	ok, err := c.IsEnabled(context.Background())
	if err != nil || ok {
		t.Fatalf("want (false, nil), got (%v, %v)", ok, err)
	}
}

func TestIsEnabled_OK(t *testing.T) {
	api := &stubAPI{}
	c := NewWithAPI(api, "us-east-1")
	ok, err := c.IsEnabled(context.Background())
	if err != nil || !ok {
		t.Fatalf("want (true, nil), got (%v, %v)", ok, err)
	}
}

func TestIsEnabled_OtherErrorPropagates(t *testing.T) {
	api := &stubAPI{coverageErr: errors.New("network down")}
	c := NewWithAPI(api, "us-east-1")
	if _, err := c.IsEnabled(context.Background()); err == nil {
		t.Fatal("want error, got nil")
	}
}

// instanceFinding builds a stub SDK finding pinned to an EC2
// instance resource so batching/pagination tests exercise the new
// per-resource projection.
func instanceFinding(instanceID string) itypes.Finding {
	id := instanceID
	arn := "arn:aws:inspector2:us-east-1:111:finding/" + instanceID
	title := "t"
	first := time.Unix(0, 0)
	last := time.Unix(1, 0)
	cve := "CVE-2026-0001"
	vd := &itypes.PackageVulnerabilityDetails{VulnerabilityId: &cve}
	return itypes.Finding{
		FindingArn:                  &arn,
		Title:                       &title,
		FirstObservedAt:             &first,
		LastObservedAt:              &last,
		Severity:                    itypes.SeverityHigh,
		PackageVulnerabilityDetails: vd,
		Resources: []itypes.Resource{{
			Type: itypes.ResourceTypeAwsEc2Instance,
			Id:   &id,
		}},
	}
}

func TestListFindingsByInstance_BatchingAndPagination(t *testing.T) {
	// 120 instance IDs → 3 chunks of 50/50/20. Findings on each
	// page are pinned to the first instance in the input list so
	// the per-resource projection has a stable join key for the
	// assertion at the end.
	ids := make([]string, 120)
	for i := range ids {
		ids[i] = "i-" + strings.Repeat("0", 8-len(itoa(i))) + itoa(i)
	}
	page := func(n int) []itypes.Finding {
		out := make([]itypes.Finding, n)
		for i := range out {
			out[i] = instanceFinding(ids[0])
		}
		return out
	}
	// 6 pages of 10 findings each: 60 results total — all attributed
	// to ids[0] by the stub.
	api := &stubAPI{pages: [][]itypes.Finding{
		page(10), page(10), page(10), page(10), page(10), page(10),
	}}
	c := NewWithAPI(api, "us-east-1")
	got, err := c.ListFindingsByInstance(context.Background(), ids)
	if err != nil {
		t.Fatalf("ListFindingsByInstance: %v", err)
	}
	if total := len(got[ids[0]]); total != 60 {
		t.Errorf("findings on ids[0]: want 60, got %d", total)
	}
	// Every requested ID is present in the map (nil slice when no
	// findings).
	if len(got) != 120 {
		t.Errorf("map size: want 120 (all keys present), got %d", len(got))
	}
	// 3 chunks expected (50+50+20). The 6 seeded pages all drain
	// inside chunk 1 (paginator keeps calling until NextToken is
	// empty), then chunks 2+3 each make a single empty-page call.
	// Total: 6 paginated calls + 2 empty starts = 8.
	if got, want := len(api.captureFilters), 8; got != want {
		t.Errorf("ListFindings calls: want %d, got %d", want, got)
	}
	chunkSizes := []int{50, 50, 50, 50, 50, 50, 50, 20}
	for i, want := range chunkSizes {
		f := api.captureFilters[i]
		if f == nil || len(f.ResourceId) != want {
			t.Errorf("call %d: want %d IDs in filter, got %d", i, want, len(f.ResourceId))
		}
	}
}

func TestListFindingsByDigest_DedupsAndSkipsEmpty(t *testing.T) {
	digests := []string{
		"sha256:aaaa", "sha256:bbbb", "sha256:aaaa", "", "sha256:bbbb",
	}
	api := &stubAPI{}
	c := NewWithAPI(api, "us-east-1")
	got, err := c.ListFindingsByImageDigest(context.Background(), digests)
	if err != nil {
		t.Fatalf("ListFindingsByImageDigest: %v", err)
	}
	if len(api.captureFilters) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(api.captureFilters))
	}
	if got := api.captureFilters[0].EcrImageHash; len(got) != 2 {
		t.Errorf("dedup: want 2 filters, got %d", len(got))
	}
	if _, ok := got["sha256:aaaa"]; !ok {
		t.Errorf("ensureKeys missed sha256:aaaa: %v", got)
	}
	if _, ok := got["sha256:bbbb"]; !ok {
		t.Errorf("ensureKeys missed sha256:bbbb: %v", got)
	}
}

func TestProjectFinding_PicksHighestCVSS(t *testing.T) {
	arn := "arn:test"
	cve := "CVE-2026-9999"
	name1 := "openssl"
	name2 := "libcrypto"
	ver := "1.0.0"
	fix := "1.0.1"
	score1, score2, score3 := 7.4, 9.1, 5.5
	hash := "sha256:imagehash"

	vd := &itypes.PackageVulnerabilityDetails{
		VulnerabilityId: &cve,
		Cvss: []itypes.CvssScore{
			{BaseScore: &score1},
			{BaseScore: &score2}, // highest — should win
			{BaseScore: &score3},
		},
		VulnerablePackages: []itypes.VulnerablePackage{
			{Name: &name1, Version: &ver, FixedInVersion: &fix},
			{Name: &name2, Version: &ver, FixedInVersion: &fix},
		},
	}
	resources := []itypes.Resource{{
		Type: itypes.ResourceTypeAwsEcrContainerImage,
		Id:   &hash,
		Details: &itypes.ResourceDetails{
			AwsEcrContainerImage: &itypes.AwsEcrContainerImageDetails{ImageHash: &hash},
		},
	}}
	projected := projectFinding(itypes.Finding{
		FindingArn:                  &arn,
		Severity:                    itypes.SeverityCritical,
		PackageVulnerabilityDetails: vd,
		Resources:                   resources,
	}, "eu-west-1")
	if len(projected) != 1 {
		t.Fatalf("projectFinding: want 1, got %d", len(projected))
	}
	got := projected[0]
	if got.CVSSv3Score != 9.1 {
		t.Errorf("cvss: want 9.1, got %v", got.CVSSv3Score)
	}
	if got.CVE != "CVE-2026-9999" {
		t.Errorf("cve: got %q", got.CVE)
	}
	// Multi-package findings concatenate names so chips don't
	// silently under-report.
	if got.PackageName != "openssl, libcrypto" {
		t.Errorf("package name: want %q, got %q", "openssl, libcrypto", got.PackageName)
	}
	if got.FixedVersion != "1.0.1" {
		t.Errorf("fixed: got %q", got.FixedVersion)
	}
	if got.ResourceID != hash {
		t.Errorf("resource id: want %q, got %q", hash, got.ResourceID)
	}
	if !strings.Contains(got.InspectorURL, "eu-west-1") || !strings.Contains(got.InspectorURL, "arn:test") {
		t.Errorf("inspector url: %q", got.InspectorURL)
	}
}

func TestProjectFinding_FansOutAcrossResources(t *testing.T) {
	// One SDK finding with two resources → two store rows. Mirrors
	// the rare case where Inspector reports a CVE that matches the
	// same package on two co-scanned resources.
	arn := "arn:test"
	hashA := "sha256:aaaa"
	hashB := "sha256:bbbb"
	mk := func(h *string) itypes.Resource {
		return itypes.Resource{
			Type: itypes.ResourceTypeAwsEcrContainerImage,
			Id:   h,
			Details: &itypes.ResourceDetails{
				AwsEcrContainerImage: &itypes.AwsEcrContainerImageDetails{ImageHash: h},
			},
		}
	}
	got := projectFinding(itypes.Finding{
		FindingArn: &arn,
		Severity:   itypes.SeverityHigh,
		Resources:  []itypes.Resource{mk(&hashA), mk(&hashB)},
	}, "us-east-1")
	if len(got) != 2 {
		t.Fatalf("want 2 projections, got %d", len(got))
	}
	if got[0].ResourceID != hashA || got[1].ResourceID != hashB {
		t.Errorf("resource ids: %q, %q", got[0].ResourceID, got[1].ResourceID)
	}
}

func TestProjectFinding_SkipsResourcelessFindings(t *testing.T) {
	arn := "arn:test"
	if got := projectFinding(itypes.Finding{FindingArn: &arn}, "us-east-1"); got != nil {
		t.Errorf("findings without Resources must be skipped, got %v", got)
	}
}

func TestDedup(t *testing.T) {
	got := dedup([]string{"a", "", "a", "b", "a", "c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("dedup: want %v, got %v", want, got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("dedup[%d]: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestChunked(t *testing.T) {
	var chunks [][]int
	for c := range chunked([]int{1, 2, 3, 4, 5, 6, 7}, 3) {
		chunks = append(chunks, c)
	}
	if len(chunks) != 3 || len(chunks[0]) != 3 || len(chunks[2]) != 1 {
		t.Errorf("chunked: %v", chunks)
	}
}

// itoa is a tiny integer→string helper so the test doesn't pull in
// strconv (keeps the deps page reviewable).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
