// karpenter_test.go — pure-logic tests for the Karpenter dashboard
// helpers. The handler-level tests (auto-detect, parallel fan-out,
// audit emission) live in cmd/periscope/karpenter_handler_test.go;
// this file covers parser + cost-compute + grouping primitives that
// don't need a fake apiserver to exercise.

package k8s

import (
	"testing"
	"time"
)

// ─── parseFailedSchedulingEvent ────────────────────────────────────────────

func TestParseFailedSchedulingEvent_KarpenterSinglePool(t *testing.T) {
	// Karpenter renders serrors.Wrap with klog.KRef as `NodePool="<name>"`.
	msg := `Failed to schedule pod, incompatible with nodepool, daemonset overhead={"cpu":"100m"}, no instance type satisfied resources NodePool="default"`
	got := parseFailedSchedulingEvent(msg)
	if len(got) != 1 {
		t.Fatalf("want 1 reason, got %d: %+v", len(got), got)
	}
	if got[0].NodePool != "default" {
		t.Errorf("pool = %q, want %q", got[0].NodePool, "default")
	}
	if got[0].Reason == "" {
		t.Error("reason should not be empty")
	}
}

func TestParseFailedSchedulingEvent_MultiplePools(t *testing.T) {
	// Realistic multi-pool message — Karpenter wraps each per-pool
	// rejection with serrors.Wrap and concatenates via multierr.
	msg := `Failed to schedule pod, incompatible requirements, key node-role.kubernetes.io/control-plane NotIn [], NodePool="default" no instance types met resources, NodePool="spot-amd" insufficient capacity NodePool="gpu-l4" gpu=l4 not satisfied`
	got := parseFailedSchedulingEvent(msg)
	if len(got) != 3 {
		t.Fatalf("want 3 reasons, got %d: %+v", len(got), got)
	}
	wantPools := []string{"default", "spot-amd", "gpu-l4"}
	for i, p := range wantPools {
		if got[i].NodePool != p {
			t.Errorf("[%d] pool = %q, want %q", i, got[i].NodePool, p)
		}
		if got[i].Reason == "" {
			t.Errorf("[%d] reason empty for pool %s", i, p)
		}
	}
}

func TestParseFailedSchedulingEvent_KubeSchedulerStyleReturnsNil(t *testing.T) {
	// kube-scheduler emits messages without any NodePool= marker.
	// The parser should return nil so the caller renders the raw
	// reason string instead.
	msg := `0/3 nodes are available: 3 node(s) had untolerated taint {dedicated: ml}.`
	got := parseFailedSchedulingEvent(msg)
	if got != nil {
		t.Errorf("kube-scheduler style should return nil, got %+v", got)
	}
}

func TestParseFailedSchedulingEvent_EmptyMessage(t *testing.T) {
	if got := parseFailedSchedulingEvent(""); got != nil {
		t.Errorf("empty input → want nil, got %+v", got)
	}
}

func TestParseFailedSchedulingEvent_UnquotedNodePool(t *testing.T) {
	// Some logr backends omit the quotes on KRef rendering. The
	// parser accepts both styles.
	msg := `incompatible requirements NodePool=default no capacity`
	got := parseFailedSchedulingEvent(msg)
	if len(got) != 1 || got[0].NodePool != "default" {
		t.Errorf("unquoted pool not parsed: %+v", got)
	}
}

// ─── ComputeKarpenterCosts ─────────────────────────────────────────────────

func TestComputeKarpenterCosts_SpotPoolGetsSavingsPct(t *testing.T) {
	pools := []NodePoolView{{Name: "spot-amd"}}
	claims := []NodeClaimView{
		{Name: "n1", NodePool: "spot-amd", InstanceType: "m5.large", CapacityType: "spot", Zone: "eu-west-1a"},
		{Name: "n2", NodePool: "spot-amd", InstanceType: "m5.large", CapacityType: "spot", Zone: "eu-west-1a"},
	}
	metrics := &KarpenterMetrics{
		Prices: []karpenterPriceMetric{
			{InstanceType: "m5.large", CapacityType: "spot", Zone: "eu-west-1a", PricePerHour: 0.03},
			{InstanceType: "m5.large", CapacityType: "on-demand", Zone: "eu-west-1a", PricePerHour: 0.10},
		},
	}
	ComputeKarpenterCosts(pools, claims, metrics)

	if pools[0].NodeCount != 2 {
		t.Errorf("nodeCount = %d, want 2", pools[0].NodeCount)
	}
	if pools[0].Cost == nil {
		t.Fatal("cost should be set when metrics + claims present")
	}
	if pools[0].Cost.CurrentHourly != 0.06 {
		t.Errorf("currentHourly = %v, want 0.06", pools[0].Cost.CurrentHourly)
	}
	if pools[0].Cost.OnDemandHourly != 0.20 {
		t.Errorf("onDemandHourly = %v, want 0.20", pools[0].Cost.OnDemandHourly)
	}
	// (1 - 0.06/0.20) * 100 = 70
	if pools[0].Cost.SpotSavingsPct != 70 {
		t.Errorf("spotSavingsPct = %d, want 70", pools[0].Cost.SpotSavingsPct)
	}
}

func TestComputeKarpenterCosts_OnDemandPoolHasZeroSavings(t *testing.T) {
	pools := []NodePoolView{{Name: "od"}}
	claims := []NodeClaimView{
		{Name: "n1", NodePool: "od", InstanceType: "m5.large", CapacityType: "on-demand", Zone: "us-east-1a"},
	}
	metrics := &KarpenterMetrics{
		Prices: []karpenterPriceMetric{
			{InstanceType: "m5.large", CapacityType: "on-demand", Zone: "us-east-1a", PricePerHour: 0.10},
		},
	}
	ComputeKarpenterCosts(pools, claims, metrics)
	if pools[0].Cost == nil {
		t.Fatal("cost should be set")
	}
	if pools[0].Cost.SpotSavingsPct != 0 {
		t.Errorf("on-demand pool savings should be 0%%, got %d", pools[0].Cost.SpotSavingsPct)
	}
}

func TestComputeKarpenterCosts_NoMetricsLeavesCostNil(t *testing.T) {
	pools := []NodePoolView{{Name: "default"}}
	claims := []NodeClaimView{
		{Name: "n1", NodePool: "default", InstanceType: "m5.large"},
	}
	ComputeKarpenterCosts(pools, claims, nil)
	if pools[0].Cost != nil {
		t.Errorf("metrics=nil → Cost should remain nil, got %+v", pools[0].Cost)
	}
	// NodeCount should still be filled — it doesn't depend on metrics.
	if pools[0].NodeCount != 1 {
		t.Errorf("nodeCount = %d, want 1", pools[0].NodeCount)
	}
}

func TestComputeKarpenterCosts_PoolWithNoClaimsHasCostNil(t *testing.T) {
	pools := []NodePoolView{{Name: "empty"}}
	metrics := &KarpenterMetrics{
		Prices: []karpenterPriceMetric{
			{InstanceType: "m5.large", CapacityType: "spot", Zone: "eu-west-1a", PricePerHour: 0.03},
		},
	}
	ComputeKarpenterCosts(pools, []NodeClaimView{}, metrics)
	if pools[0].Cost != nil {
		t.Errorf("pool with no claims → Cost should be nil, got %+v", pools[0].Cost)
	}
	if pools[0].NodeCount != 0 {
		t.Errorf("nodeCount = %d, want 0", pools[0].NodeCount)
	}
}

func TestComputeKarpenterCosts_PoolUsageFromMetrics(t *testing.T) {
	pools := []NodePoolView{{Name: "default"}}
	metrics := &KarpenterMetrics{
		NodePoolUsage: map[string]map[string]float64{
			"default": {"cpu": 8, "memory": 17179869184},
		},
	}
	ComputeKarpenterCosts(pools, nil, metrics)
	if pools[0].Usage["cpu"] != "8" {
		t.Errorf("usage.cpu = %q, want 8", pools[0].Usage["cpu"])
	}
}

// ─── parseKarpenterMetrics ─────────────────────────────────────────────────

func TestParseKarpenterMetrics_ExtractsPriceFamily(t *testing.T) {
	exposition := `# HELP karpenter_cloudprovider_instance_type_offering_price_estimate Hourly price.
# TYPE karpenter_cloudprovider_instance_type_offering_price_estimate gauge
karpenter_cloudprovider_instance_type_offering_price_estimate{instance_type="m5.large",capacity_type="spot",zone="eu-west-1a"} 0.03
karpenter_cloudprovider_instance_type_offering_price_estimate{instance_type="m5.large",capacity_type="on-demand",zone="eu-west-1a"} 0.10
# HELP karpenter_nodepools_usage Current usage.
# TYPE karpenter_nodepools_usage gauge
karpenter_nodepools_usage{nodepool="default",resource_type="cpu"} 8
`
	m, err := parseKarpenterMetrics([]byte(exposition))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Prices) != 2 {
		t.Errorf("prices: got %d, want 2", len(m.Prices))
	}
	if m.NodePoolUsage["default"]["cpu"] != 8 {
		t.Errorf("usage default.cpu = %v, want 8", m.NodePoolUsage["default"]["cpu"])
	}
}

func TestParseKarpenterMetrics_HandlesMissingFamilies(t *testing.T) {
	// Karpenter that hasn't emitted any price metrics yet (e.g. a
	// fresh install with no NodePools) shouldn't error.
	exposition := `# HELP go_gc_duration_seconds GC duration.
# TYPE go_gc_duration_seconds summary
go_gc_duration_seconds{quantile="0"} 0.0001
`
	m, err := parseKarpenterMetrics([]byte(exposition))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(m.Prices) != 0 {
		t.Errorf("expected no prices, got %+v", m.Prices)
	}
}

func TestParseKarpenterMetrics_MalformedYieldsEmptyResultNotError(t *testing.T) {
	// Parser is intentionally lenient — unrecognizable lines are
	// silently skipped so a single garbled line does not block the
	// rest of the exposition. We assert "no error, empty result."
	m, err := parseKarpenterMetrics([]byte("this is not prometheus exposition\n##\n"))
	if err != nil {
		t.Fatalf("lenient parse should not error: %v", err)
	}
	if len(m.Prices) != 0 {
		t.Errorf("expected no prices for garbled input, got %+v", m.Prices)
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

func TestAgeString_BoundaryUnits(t *testing.T) {
	cases := []struct {
		seconds int
		want    string
	}{
		{45, "45s"},
		{60, "1m"},
		{90, "1m30s"},
		{3600, "1h"},
		{3660, "1h1m"},
		{86400, "1d"},
		{90000, "1d1h"},
	}
	for _, tc := range cases {
		got := ageString(timeSeconds(tc.seconds))
		if got != tc.want {
			t.Errorf("ageString(%ds) = %q, want %q", tc.seconds, got, tc.want)
		}
	}
}

func timeSeconds(s int) time.Duration {
	return time.Duration(s) * time.Second
}
