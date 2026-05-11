package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jonboulle/clockwork"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/awsec2"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/cve"
)

// The package-level testRegistry helper (cani_handler_test.go) seeds
// a cluster named "test"; cve handler tests reuse that name end-to-end.
const cveTestCluster = "test"

// ── stub clients ────────────────────────────────────────────────────

type stubCveInsp struct {
	enabled bool
	digests map[string][]cve.Finding
	insts   map[string][]cve.Finding
}

func (s *stubCveInsp) IsEnabled(_ context.Context) (bool, error) { return s.enabled, nil }

func (s *stubCveInsp) ListFindingsByInstance(_ context.Context, ids []string) (map[string][]cve.Finding, error) {
	out := make(map[string][]cve.Finding, len(ids))
	for _, id := range ids {
		out[id] = append([]cve.Finding(nil), s.insts[id]...)
	}
	return out, nil
}

func (s *stubCveInsp) ListFindingsByImageDigest(_ context.Context, ds []string) (map[string][]cve.Finding, error) {
	out := make(map[string][]cve.Finding, len(ds))
	for _, d := range ds {
		out[d] = append([]cve.Finding(nil), s.digests[d]...)
	}
	return out, nil
}

type stubCveEC2 struct{ list []awsec2.InstanceMeta }

func (s *stubCveEC2) DescribeInstances(_ context.Context, ids []string) ([]awsec2.InstanceMeta, error) {
	if s.list != nil {
		return s.list, nil
	}
	out := make([]awsec2.InstanceMeta, 0, len(ids))
	for _, id := range ids {
		out = append(out, awsec2.InstanceMeta{InstanceID: id})
	}
	return out, nil
}

// cveFixtureOpts bundles every knob the CVE handler tests reach for.
type cveFixtureOpts struct {
	insp        *stubCveInsp
	ec2         *stubCveEC2
	pods        []corev1.Pod
	nodes       []corev1.Node
	skipHydrate bool
}

// buildCveFixture returns a manager wired against the provided pods +
// findings. After EnsureHydrated returns, the cold path has run; the
// pod informer indexer settles asynchronously, so tests that need
// the lister must call waitForCveLister.
func buildCveFixture(t *testing.T, o cveFixtureOpts) *cve.Manager {
	t.Helper()
	cs := fake.NewSimpleClientset()
	for i := range o.pods {
		_, _ = cs.CoreV1().Pods(o.pods[i].Namespace).Create(context.Background(), &o.pods[i], metav1.CreateOptions{})
	}
	for i := range o.nodes {
		_, _ = cs.CoreV1().Nodes().Create(context.Background(), &o.nodes[i], metav1.CreateOptions{})
	}
	factory := func(_ context.Context, _ clusters.Cluster) (kubernetes.Interface, error) {
		return cs, nil
	}
	mgr := cve.NewManager(
		o.insp, o.ec2, factory,
		clockwork.NewFakeClock(),
		cve.Config{
			RefreshInterval:           time.Hour,
			EvictAfter:                time.Hour,
			TTLScanInterval:           time.Hour,
			EvictionScanInterval:      time.Hour,
			MaxConcurrentDeltaFetches: 1,
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	t.Cleanup(mgr.Stop)
	if !o.skipHydrate {
		if _, err := mgr.EnsureHydrated(context.Background(), clusters.Cluster{Name: cveTestCluster}); err != nil {
			t.Fatalf("hydrate: %v", err)
		}
	}
	return mgr
}

// invokeCveHandler wires up the chi route context the handler reads
// and returns the response.
func invokeCveHandler(t *testing.T, h credentials.Handler, method, url string, params map[string]string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader = http.NoBody
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, url, rd)
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = credentials.WithSession(ctx, defaultTestSession)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	h(rec, req, defaultTestProvider())
	return rec
}

// unmarshalJSON decodes a response body into out, failing the test
// on parse error. Named separately from helm_preview_handler_test's
// mustJSON (which marshals).
func unmarshalJSON(t *testing.T, raw []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("json unmarshal: %v body=%s", err, raw)
	}
}

// waitForCveLister polls until the manager's PodLister returns a
// non-nil lister that has indexed at least min pods. The fake
// informer sync is async; without this the tests race the cache build.
func waitForCveLister(t *testing.T, mgr *cve.Manager, cluster string, min int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if l := mgr.PodLister(cluster); l != nil {
			pods, _ := l.List(labels.Everything())
			if len(pods) >= min {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pod lister never indexed %d pods", min)
}

// ── tests ───────────────────────────────────────────────────────────

func TestCveStatus_NilManager_EmptyState(t *testing.T) {
	reg := testRegistry(t)
	h := cveStatusHandler(reg, nil)
	rec := invokeCveHandler(t, h, http.MethodGet, "/cve/status",
		map[string]string{"cluster": cveTestCluster}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d body=%s", rec.Code, rec.Body)
	}
	var got cve.StatusResp
	unmarshalJSON(t, rec.Body.Bytes(), &got)
	if got.InspectorEnabled || !got.Hydrated {
		t.Errorf("empty state: want enabled=false hydrated=true, got %+v", got)
	}
}

func TestCveStatus_Hydrated_PopulatesCounts(t *testing.T) {
	reg := testRegistry(t)
	mgr := buildCveFixture(t, cveFixtureOpts{
		insp: &stubCveInsp{enabled: true, digests: map[string][]cve.Finding{
			"sha256:abc": {{CVE: "CVE-1", Severity: "HIGH"}},
		}},
		ec2: &stubCveEC2{},
		pods: []corev1.Pod{
			cvePod("default", "app", "111.dkr.ecr.us-east-1.amazonaws.com/app:v1", "sha256:abc"),
		},
	})
	h := cveStatusHandler(reg, mgr)
	rec := invokeCveHandler(t, h, http.MethodGet, "/cve/status",
		map[string]string{"cluster": cveTestCluster}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var got cve.StatusResp
	unmarshalJSON(t, rec.Body.Bytes(), &got)
	if !got.InspectorEnabled || !got.Hydrated {
		t.Errorf("got %+v", got)
	}
	if got.EntryCounts.Digests == 0 {
		t.Errorf("entryCounts.digests should be > 0: %+v", got.EntryCounts)
	}
	if got.LastHydrate.IsZero() {
		t.Errorf("lastHydrate should be set after hydrate")
	}
}

func TestCveByInstance_SeverityRollupAndAMI(t *testing.T) {
	reg := testRegistry(t)
	mgr := buildCveFixture(t, cveFixtureOpts{
		insp: &stubCveInsp{
			enabled: true,
			insts: map[string][]cve.Finding{
				"i-1": {
					{CVE: "A", Severity: "CRITICAL"},
					{CVE: "B", Severity: "HIGH"},
					{CVE: "C", Severity: "HIGH"},
					{CVE: "D", Severity: "MEDIUM"},
				},
			},
		},
		ec2: &stubCveEC2{list: []awsec2.InstanceMeta{
			{InstanceID: "i-1", AMI: "ami-test", Tags: map[string]string{"eks:nodegroup-name": "ng-a"}},
		}},
		nodes: []corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Spec: corev1.NodeSpec{ProviderID: "aws:///us-east-1a/i-1"}},
		},
	})
	h := cveByInstanceHandler(reg, mgr)
	rec := invokeCveHandler(t, h, http.MethodGet, "/cve/by-instance",
		map[string]string{"cluster": cveTestCluster}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var got cve.InstancesResp
	unmarshalJSON(t, rec.Body.Bytes(), &got)
	if len(got.Instances) != 1 {
		t.Fatalf("want 1 instance, got %d", len(got.Instances))
	}
	r := got.Instances[0]
	if r.SeverityCounts.Critical != 1 || r.SeverityCounts.High != 2 || r.SeverityCounts.Medium != 1 {
		t.Errorf("severity rollup: %+v", r.SeverityCounts)
	}
	if r.AMI != "ami-test" {
		t.Errorf("AMI: want ami-test, got %q", r.AMI)
	}
	if r.Owner.Kind != cve.OwnerManagedNodegroup || r.Owner.Name != "ng-a" {
		t.Errorf("owner: %+v", r.Owner)
	}
}

func TestCveByInstanceOne_NotFound(t *testing.T) {
	reg := testRegistry(t)
	mgr := buildCveFixture(t, cveFixtureOpts{
		insp: &stubCveInsp{enabled: true},
		ec2:  &stubCveEC2{},
	})
	h := cveByInstanceOneHandler(reg, mgr)
	rec := invokeCveHandler(t, h, http.MethodGet, "/cve/by-instance/i-missing",
		map[string]string{"cluster": cveTestCluster, "instanceID": "i-missing"}, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d body=%s", rec.Code, rec.Body)
	}
}

func TestCvePods_Pagination(t *testing.T) {
	reg := testRegistry(t)
	pods := make([]corev1.Pod, 250)
	for i := 0; i < 250; i++ {
		pods[i] = cvePod("ns", "pod-"+padN(i, 4),
			"111.dkr.ecr.us-east-1.amazonaws.com/app:v1", "sha256:abc")
	}
	mgr := buildCveFixture(t, cveFixtureOpts{
		insp: &stubCveInsp{enabled: true, digests: map[string][]cve.Finding{
			"sha256:abc": {{CVE: "X", Severity: "HIGH"}},
		}},
		ec2:  &stubCveEC2{},
		pods: pods,
	})
	waitForCveLister(t, mgr, cveTestCluster, 250)

	h := cvePodsHandler(reg, mgr)
	p1 := getCvePodsPage(t, h, "")
	if len(p1.Pods) != 100 || p1.Next == "" {
		t.Fatalf("page1: pods=%d next=%q", len(p1.Pods), p1.Next)
	}
	p2 := getCvePodsPage(t, h, p1.Next)
	if len(p2.Pods) != 100 || p2.Next == "" {
		t.Fatalf("page2: pods=%d next=%q", len(p2.Pods), p2.Next)
	}
	p3 := getCvePodsPage(t, h, p2.Next)
	if len(p3.Pods) != 50 || p3.Next != "" {
		t.Fatalf("page3: pods=%d next=%q", len(p3.Pods), p3.Next)
	}
}

func TestCvePods_ScanCoverageMix(t *testing.T) {
	reg := testRegistry(t)
	mgr := buildCveFixture(t, cveFixtureOpts{
		insp: &stubCveInsp{enabled: true, digests: map[string][]cve.Finding{
			"sha256:abc": {{CVE: "X", Severity: "HIGH"}},
		}},
		ec2: &stubCveEC2{},
		pods: []corev1.Pod{
			cvePod("ns", "full",
				"111.dkr.ecr.us-east-1.amazonaws.com/app:v1", "sha256:abc"),
			cvePod2Containers("ns", "partial",
				"111.dkr.ecr.us-east-1.amazonaws.com/app:v1", "sha256:abc",
				"docker.io/library/nginx:latest", "docker-pullable://nginx@sha256:zzz"),
			cvePod("ns", "none",
				"docker.io/library/nginx:latest", "docker-pullable://nginx@sha256:zzz"),
		},
	})
	waitForCveLister(t, mgr, cveTestCluster, 3)

	h := cvePodsHandler(reg, mgr)
	body := getCvePodsPage(t, h, "")
	coverage := map[string]cve.ScanCoverage{}
	for _, p := range body.Pods {
		coverage[p.Name] = p.ScanCoverage
	}
	if coverage["full"] != cve.CoverageFull {
		t.Errorf("full: %q", coverage["full"])
	}
	if coverage["partial"] != cve.CoveragePartial {
		t.Errorf("partial: %q", coverage["partial"])
	}
	if coverage["none"] != cve.CoverageNone {
		t.Errorf("none: %q", coverage["none"])
	}
}

func TestCveRefresh_EmitsAudit(t *testing.T) {
	reg := testRegistry(t)
	mgr := buildCveFixture(t, cveFixtureOpts{
		insp: &stubCveInsp{enabled: true},
		ec2:  &stubCveEC2{},
	})
	sink := &recordingSink{}
	h := cveRefreshHandler(reg, mgr, audit.New(sink))
	body, _ := json.Marshal(cve.RefreshReq{
		Digests:     []string{"sha256:abc"},
		InstanceIDs: []string{"i-1"},
	})
	rec := invokeCveHandler(t, h, http.MethodPost, "/cve/refresh",
		map[string]string{"cluster": cveTestCluster}, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", rec.Code, rec.Body)
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Verb != audit.VerbCveRefresh {
		t.Errorf("verb: %q", events[0].Verb)
	}
	if events[0].Cluster != cveTestCluster {
		t.Errorf("cluster: %q", events[0].Cluster)
	}
	if events[0].Outcome != audit.OutcomeSuccess {
		t.Errorf("outcome: %q", events[0].Outcome)
	}
}

func TestCveRefresh_HydrateInFlight_202(t *testing.T) {
	reg := testRegistry(t)
	mgr := buildCveFixture(t, cveFixtureOpts{
		insp:        &stubCveInsp{enabled: true},
		ec2:         &stubCveEC2{},
		skipHydrate: true,
	})
	sink := &recordingSink{}
	h := cveRefreshHandler(reg, mgr, audit.New(sink))
	rec := invokeCveHandler(t, h, http.MethodPost, "/cve/refresh",
		map[string]string{"cluster": cveTestCluster}, []byte(`{}`))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status: want 202, got %d body=%s", rec.Code, rec.Body)
	}
	if hdr := rec.Header().Get("Next-Poll"); hdr == "" {
		t.Errorf("Next-Poll header missing")
	}
}

func TestCveETag_NotModified(t *testing.T) {
	reg := testRegistry(t)
	mgr := buildCveFixture(t, cveFixtureOpts{
		insp: &stubCveInsp{enabled: true},
		ec2:  &stubCveEC2{},
	})
	h := cveByInstanceHandler(reg, mgr)
	rec1 := invokeCveHandler(t, h, http.MethodGet, "/cve/by-instance",
		map[string]string{"cluster": cveTestCluster}, nil)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call: %d", rec1.Code)
	}
	etag := rec1.Header().Get("ETag")
	if etag == "" {
		t.Fatal("ETag missing on first response")
	}
	req2 := httptest.NewRequest(http.MethodGet, "/cve/by-instance", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("cluster", cveTestCluster)
	ctx := context.WithValue(req2.Context(), chi.RouteCtxKey, rctx)
	ctx = credentials.WithSession(ctx, defaultTestSession)
	req2 = req2.WithContext(ctx)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h(rec2, req2, defaultTestProvider())
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("conditional: want 304, got %d body=%s", rec2.Code, rec2.Body)
	}
}

// ── pod / page helpers ──────────────────────────────────────────────

func cvePod(ns, name, image, digest string) corev1.Pod {
	imageID := ""
	if digest != "" {
		imageID = "docker-pullable://repo@" + digest
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: image}}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "app", Image: image, ImageID: imageID},
		}},
	}
}

func cvePod2Containers(ns, name, image1, digest1, image2, imageID2 string) corev1.Pod {
	imageID1 := ""
	if digest1 != "" {
		imageID1 = "docker-pullable://repo@" + digest1
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Image: image1},
			{Name: "sidecar", Image: image2},
		}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "app", Image: image1, ImageID: imageID1},
			{Name: "sidecar", Image: image2, ImageID: imageID2},
		}},
	}
}

func padN(n, w int) string {
	s := strconv.Itoa(n)
	for len(s) < w {
		s = "0" + s
	}
	return s
}

func getCvePodsPage(t *testing.T, h credentials.Handler, cursor string) cve.PodsResp {
	t.Helper()
	url := "/cve/pods"
	if cursor != "" {
		url += "?cursor=" + cursor
	}
	rec := invokeCveHandler(t, h, http.MethodGet, url,
		map[string]string{"cluster": cveTestCluster}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pods: %d body=%s", rec.Code, rec.Body)
	}
	var got cve.PodsResp
	unmarshalJSON(t, rec.Body.Bytes(), &got)
	return got
}
