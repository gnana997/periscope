// karpenter_handler_test.go — handler-level coverage for the
// Karpenter dashboard endpoint (#118). Uses the package-var test
// seams in karpenter_handler.go to substitute fakes for every
// k8s.* dependency, so these tests run with no fixture beyond a
// stub registry + emitter sink.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// withKarpenterSeams installs canned values for every test seam in
// karpenter_handler.go and restores the originals on cleanup. Pass
// nil for a function to leave the production wiring in place.
type karpenterFakes struct {
	IsInstalled    func(context.Context, credentials.Provider, clusters.Cluster) (bool, error)
	BuildClients   func(context.Context, credentials.Provider, clusters.Cluster) (kubernetes.Interface, dynamic.Interface, error)
	ListPools      func(context.Context, dynamic.Interface) ([]k8s.NodePoolView, error)
	ListClaims     func(context.Context, dynamic.Interface) ([]k8s.NodeClaimView, error)
	ListPending    func(context.Context, kubernetes.Interface, func() metav1.Time) ([]k8s.PendingPodView, bool, error)
	ScrapeMetrics  func(context.Context, kubernetes.Interface) (*k8s.KarpenterMetrics, error)
	ComputeCosts   func(pools []k8s.NodePoolView, claims []k8s.NodeClaimView, m *k8s.KarpenterMetrics)
}

func withKarpenterSeams(t *testing.T, f karpenterFakes) {
	t.Helper()
	prevIs := karpenterIsInstalledFn
	prevBuild := karpenterBuildClientsFn
	prevPools := karpenterListPoolsFn
	prevClaims := karpenterListClaimsFn
	prevPending := karpenterListPendingFn
	prevMetrics := karpenterScrapeMetricsFn
	prevCosts := karpenterComputeCostsFn

	if f.IsInstalled != nil {
		karpenterIsInstalledFn = f.IsInstalled
	}
	if f.BuildClients != nil {
		karpenterBuildClientsFn = f.BuildClients
	}
	if f.ListPools != nil {
		karpenterListPoolsFn = f.ListPools
	}
	if f.ListClaims != nil {
		karpenterListClaimsFn = f.ListClaims
	}
	if f.ListPending != nil {
		karpenterListPendingFn = f.ListPending
	}
	if f.ScrapeMetrics != nil {
		karpenterScrapeMetricsFn = f.ScrapeMetrics
	}
	if f.ComputeCosts != nil {
		karpenterComputeCostsFn = f.ComputeCosts
	}

	t.Cleanup(func() {
		karpenterIsInstalledFn = prevIs
		karpenterBuildClientsFn = prevBuild
		karpenterListPoolsFn = prevPools
		karpenterListClaimsFn = prevClaims
		karpenterListPendingFn = prevPending
		karpenterScrapeMetricsFn = prevMetrics
		karpenterComputeCostsFn = prevCosts
	})
}

// fakeBuildClients returns nil clients — adequate when the test's
// list/scrape fakes don't actually use the client args.
func fakeBuildClients(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, dynamic.Interface, error) {
	return nil, nil, nil
}

// ─── tests ──────────────────────────────────────────────────────────────────

func TestKarpenterHandler_NotInstalledShortCircuits(t *testing.T) {
	withKarpenterSeams(t, karpenterFakes{
		IsInstalled: func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (bool, error) {
			return false, nil
		},
		// All other seams should NOT be called when not installed.
		BuildClients: func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, dynamic.Interface, error) {
			t.Fatal("BuildClients should not be called when CRDs absent")
			return nil, nil, nil
		},
	})

	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return karpenterHandler(reg, e) },
		http.MethodGet, "/api/clusters/test/karpenter",
		map[string]string{"cluster": "test"}, nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp k8s.KarpenterDashboard
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp.Available {
		t.Errorf("Available = true, want false (CRDs were absent)")
	}
	if len(resp.NodePools) != 0 || len(resp.NodeClaims) != 0 || len(resp.PendingPods) != 0 {
		t.Errorf("unexpected payload populated: %+v", resp)
	}

	events := sink.snapshot()
	if len(events) != 1 || events[0].Verb != audit.VerbKarpenterRead {
		t.Fatalf("expected 1 audit row with VerbKarpenterRead, got %+v", events)
	}
	if op, _ := events[0].Extra["op"].(string); op != "available_false" {
		t.Errorf("Extra.op = %q, want available_false", op)
	}
	if events[0].Outcome != audit.OutcomeSuccess {
		t.Errorf("outcome = %q, want success", events[0].Outcome)
	}
}

func TestKarpenterHandler_HappyPath(t *testing.T) {
	withKarpenterSeams(t, karpenterFakes{
		IsInstalled: func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (bool, error) {
			return true, nil
		},
		BuildClients: fakeBuildClients,
		ListPools: func(_ context.Context, _ dynamic.Interface) ([]k8s.NodePoolView, error) {
			weight := int32(10)
			return []k8s.NodePoolView{
				{Name: "default", Weight: &weight},
			}, nil
		},
		ListClaims: func(_ context.Context, _ dynamic.Interface) ([]k8s.NodeClaimView, error) {
			return []k8s.NodeClaimView{
				{Name: "n1", NodePool: "default", InstanceType: "m5.large", CapacityType: "spot"},
			}, nil
		},
		ListPending: func(_ context.Context, _ kubernetes.Interface, _ func() metav1.Time) ([]k8s.PendingPodView, bool, error) {
			return []k8s.PendingPodView{
				{Namespace: "ml", Name: "train", PendingFor: "4m"},
			}, false, nil
		},
		ScrapeMetrics: func(_ context.Context, _ kubernetes.Interface) (*k8s.KarpenterMetrics, error) {
			return &k8s.KarpenterMetrics{}, nil
		},
		ComputeCosts: func(_ []k8s.NodePoolView, _ []k8s.NodeClaimView, _ *k8s.KarpenterMetrics) {
			// no-op for this test; compute logic is covered in karpenter_test.go
		},
	})

	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return karpenterHandler(reg, e) },
		http.MethodGet, "/api/clusters/test/karpenter",
		map[string]string{"cluster": "test"}, nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp k8s.KarpenterDashboard
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Available {
		t.Error("Available should be true")
	}
	if len(resp.NodePools) != 1 || resp.NodePools[0].Name != "default" {
		t.Errorf("nodepools mismatch: %+v", resp.NodePools)
	}
	if len(resp.NodeClaims) != 1 {
		t.Errorf("expected 1 claim, got %d", len(resp.NodeClaims))
	}
	if len(resp.PendingPods) != 1 {
		t.Errorf("expected 1 pending pod, got %d", len(resp.PendingPods))
	}
	if !resp.MetricsAvailable {
		t.Error("MetricsAvailable should be true when scrape returned non-nil")
	}

	events := sink.snapshot()
	if len(events) != 1 || events[0].Verb != audit.VerbKarpenterRead || events[0].Outcome != audit.OutcomeSuccess {
		t.Fatalf("unexpected audit rows: %+v", events)
	}
	if op, _ := events[0].Extra["op"].(string); op != "list" {
		t.Errorf("Extra.op = %q, want list", op)
	}
}

func TestKarpenterHandler_MetricsFailureGracefullyDegrades(t *testing.T) {
	withKarpenterSeams(t, karpenterFakes{
		IsInstalled:  func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (bool, error) { return true, nil },
		BuildClients: fakeBuildClients,
		ListPools: func(_ context.Context, _ dynamic.Interface) ([]k8s.NodePoolView, error) {
			return []k8s.NodePoolView{{Name: "default"}}, nil
		},
		ListClaims:  func(_ context.Context, _ dynamic.Interface) ([]k8s.NodeClaimView, error) { return nil, nil },
		ListPending: func(_ context.Context, _ kubernetes.Interface, _ func() metav1.Time) ([]k8s.PendingPodView, bool, error) { return nil, false, nil },
		ScrapeMetrics: func(_ context.Context, _ kubernetes.Interface) (*k8s.KarpenterMetrics, error) {
			return nil, errors.New("services \"karpenter\" not found")
		},
		ComputeCosts: func(_ []k8s.NodePoolView, _ []k8s.NodeClaimView, _ *k8s.KarpenterMetrics) {},
	})

	reg := testRegistry(t)
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return karpenterHandler(reg, e) },
		http.MethodGet, "/api/clusters/test/karpenter",
		map[string]string{"cluster": "test"}, nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics failure should not fail the handler; got %d", rec.Code)
	}
	var resp k8s.KarpenterDashboard
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.MetricsAvailable {
		t.Errorf("MetricsAvailable should be false when scrape errored")
	}
	if !resp.Available {
		t.Errorf("Available should still be true")
	}
}

func TestKarpenterHandler_PendingFailureDegradesQuietly(t *testing.T) {
	withKarpenterSeams(t, karpenterFakes{
		IsInstalled:  func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (bool, error) { return true, nil },
		BuildClients: fakeBuildClients,
		ListPools: func(_ context.Context, _ dynamic.Interface) ([]k8s.NodePoolView, error) {
			return []k8s.NodePoolView{{Name: "default"}}, nil
		},
		ListClaims:    func(_ context.Context, _ dynamic.Interface) ([]k8s.NodeClaimView, error) { return nil, nil },
		ListPending:   func(_ context.Context, _ kubernetes.Interface, _ func() metav1.Time) ([]k8s.PendingPodView, bool, error) { return nil, false, errors.New("pods list forbidden") },
		ScrapeMetrics: func(_ context.Context, _ kubernetes.Interface) (*k8s.KarpenterMetrics, error) { return nil, nil },
		ComputeCosts:  func(_ []k8s.NodePoolView, _ []k8s.NodeClaimView, _ *k8s.KarpenterMetrics) {},
	})

	reg := testRegistry(t)
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return karpenterHandler(reg, e) },
		http.MethodGet, "/api/clusters/test/karpenter",
		map[string]string{"cluster": "test"}, nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("pending failure should not fail the handler; got %d", rec.Code)
	}
	var resp k8s.KarpenterDashboard
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.PendingPods) != 0 {
		t.Errorf("PendingPods should be empty on lookup failure, got %d", len(resp.PendingPods))
	}
}

func TestKarpenterHandler_NodePoolsListFailureReturns502(t *testing.T) {
	withKarpenterSeams(t, karpenterFakes{
		IsInstalled:  func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (bool, error) { return true, nil },
		BuildClients: fakeBuildClients,
		ListPools: func(_ context.Context, _ dynamic.Interface) ([]k8s.NodePoolView, error) {
			return nil, fmt.Errorf("apiserver timeout")
		},
		ListClaims:    func(_ context.Context, _ dynamic.Interface) ([]k8s.NodeClaimView, error) { return nil, nil },
		ListPending:   func(_ context.Context, _ kubernetes.Interface, _ func() metav1.Time) ([]k8s.PendingPodView, bool, error) { return nil, false, nil },
		ScrapeMetrics: func(_ context.Context, _ kubernetes.Interface) (*k8s.KarpenterMetrics, error) { return nil, nil },
	})

	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return karpenterHandler(reg, e) },
		http.MethodGet, "/api/clusters/test/karpenter",
		map[string]string{"cluster": "test"}, nil,
	)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "E_KARPENTER_LIST") {
		t.Errorf("body missing E_KARPENTER_LIST: %s", rec.Body.String())
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Outcome != audit.OutcomeFailure {
		t.Errorf("expected single failure audit row, got %+v", events)
	}
}

func TestKarpenterHandler_DetectErrorReturns500(t *testing.T) {
	withKarpenterSeams(t, karpenterFakes{
		IsInstalled: func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (bool, error) {
			return false, fmt.Errorf("apiserver unreachable")
		},
	})

	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return karpenterHandler(reg, e) },
		http.MethodGet, "/api/clusters/test/karpenter",
		map[string]string{"cluster": "test"}, nil,
	)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	events := sink.snapshot()
	if len(events) != 1 || events[0].Outcome != audit.OutcomeFailure {
		t.Errorf("expected single failure audit row, got %+v", events)
	}
	if op, _ := events[0].Extra["op"].(string); op != "detect_failed" {
		t.Errorf("Extra.op = %q, want detect_failed", op)
	}
}

func TestKarpenterHandler_ClusterNotFoundReturns404(t *testing.T) {
	reg := testRegistry(t)
	rec, _ := actionHandlerInvoke(t,
		func(e *audit.Emitter) credentials.Handler { return karpenterHandler(reg, e) },
		http.MethodGet, "/api/clusters/missing/karpenter",
		map[string]string{"cluster": "missing"}, nil,
	)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// ─── availability probe (v1.1.1 split) ───────────────────────────────────────

func TestKarpenterAvailabilityHandler_NotInstalledNoAudit(t *testing.T) {
	withKarpenterSeams(t, karpenterFakes{
		IsInstalled: func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (bool, error) {
			return false, nil
		},
		BuildClients: func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, dynamic.Interface, error) {
			t.Fatal("BuildClients must not be called by the availability probe")
			return nil, nil, nil
		},
	})

	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(_ *audit.Emitter) credentials.Handler {
			return karpenterAvailabilityHandler(reg)
		},
		http.MethodGet, "/api/clusters/test/karpenter/availability",
		map[string]string{"cluster": "test"}, nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if resp["available"] {
		t.Errorf("available = true, want false (CRDs were absent)")
	}

	// Critical: the availability probe MUST NOT emit an audit row.
	// Pre-v1.1.1 the sidebar fired the full /karpenter endpoint on
	// every cluster page mount, and the not-installed short-circuit
	// emitted a karpenter_read audit row that flooded the audit log
	// with rows that didn't reflect operator-intent action. The
	// split was made specifically to prevent that.
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("availability probe emitted %d audit row(s); want 0: %+v", len(events), events)
	}
}

func TestKarpenterAvailabilityHandler_InstalledNoAudit(t *testing.T) {
	withKarpenterSeams(t, karpenterFakes{
		IsInstalled: func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (bool, error) {
			return true, nil
		},
		BuildClients: func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (kubernetes.Interface, dynamic.Interface, error) {
			t.Fatal("BuildClients must not be called by the availability probe (lightweight by design)")
			return nil, nil, nil
		},
	})

	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(_ *audit.Emitter) credentials.Handler {
			return karpenterAvailabilityHandler(reg)
		},
		http.MethodGet, "/api/clusters/test/karpenter/availability",
		map[string]string{"cluster": "test"}, nil,
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if !resp["available"] {
		t.Errorf("available = false, want true (CRDs were present)")
	}
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("availability probe emitted %d audit row(s); want 0", len(events))
	}
}

func TestKarpenterAvailabilityHandler_DetectErrorNoAudit(t *testing.T) {
	withKarpenterSeams(t, karpenterFakes{
		IsInstalled: func(_ context.Context, _ credentials.Provider, _ clusters.Cluster) (bool, error) {
			return false, errors.New("apiserver unreachable")
		},
	})

	reg := testRegistry(t)
	rec, sink := actionHandlerInvoke(t,
		func(_ *audit.Emitter) credentials.Handler {
			return karpenterAvailabilityHandler(reg)
		},
		http.MethodGet, "/api/clusters/test/karpenter/availability",
		map[string]string{"cluster": "test"}, nil,
	)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	// Errors at the transport level still don't audit — the sidebar
	// handles "probe failed" by hiding the entry; auditing transport
	// failures would re-introduce the noise problem.
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("availability probe emitted %d audit row(s) on error path; want 0", len(events))
	}
}

func TestKarpenterAvailabilityHandler_ClusterNotFoundReturns404(t *testing.T) {
	reg := testRegistry(t)
	rec, _ := actionHandlerInvoke(t,
		func(_ *audit.Emitter) credentials.Handler {
			return karpenterAvailabilityHandler(reg)
		},
		http.MethodGet, "/api/clusters/missing/karpenter/availability",
		map[string]string{"cluster": "missing"}, nil,
	)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
