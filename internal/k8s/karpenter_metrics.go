// karpenter_metrics.go — scrape karpenter-controller's /metrics
// endpoint via the apiserver service-proxy verb.
//
// Why apiserver proxy and not a direct HTTP client?
//   - Periscope runs in three deployment shapes (in-cluster, agent
//     reverse-tunnel, kubeconfig-from-laptop). Direct HTTP to a
//     cluster-internal Service only works in the first.
//   - cs.CoreV1().Services(ns).ProxyGet(...) routes through the
//     apiserver, so impersonation headers apply, RBAC `services/proxy`
//     gates access, and the same code path works from any backend.
//   - No new HTTP client to harden against SSRF / TLS / timeouts.
//
// Why a hand-rolled line parser instead of prometheus/common/expfmt?
// We only need 3 metric families (price estimate + nodepool usage +
// nodepool limit) — not worth depending on the full Prometheus
// parser (which in prometheus/common v0.67+ requires a global
// validation-scheme initialization that bites you at parse time).
// The exposition grammar is trivial: `<name>{<labels>} <value>`,
// optionally a trailing timestamp. Comments + HELP/TYPE lines are
// ignored.
//
// Graceful degradation: any failure (no Service, no /metrics, parse
// error) returns (nil, err). The handler logs at WARN and continues
// with `MetricsAvailable: false`.

package k8s

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/client-go/kubernetes"
)

// karpenterPriceMetric is one (instance_type, capacity_type, zone)
// observation from the Karpenter controller's price estimator.
//
//   instance_type ↔ node.kubernetes.io/instance-type
//   capacity_type ↔ karpenter.sh/capacity-type    (spot | on-demand)
//   zone          ↔ topology.kubernetes.io/zone
type karpenterPriceMetric struct {
	InstanceType string
	CapacityType string
	Zone         string
	PricePerHour float64
}

// KarpenterMetrics holds the parsed Karpenter exposition fields the
// cost computation needs.
type KarpenterMetrics struct {
	Prices        []karpenterPriceMetric
	NodePoolUsage map[string]map[string]float64
	NodePoolLimit map[string]map[string]float64
}

// targetMetrics is the closed set of metric names the parser
// extracts. Anything else in the exposition (go runtime metrics,
// process metrics, controller-runtime metrics, etc.) is ignored.
var targetMetrics = map[string]struct{}{
	"karpenter_cloudprovider_instance_type_offering_price_estimate": {},
	"karpenter_nodepools_usage":                                     {},
	"karpenter_nodepools_limit":                                     {},
}

// ScrapeKarpenterMetrics fetches the controller's /metrics
// exposition via the apiserver proxy and parses out the metrics
// the dashboard needs.
func ScrapeKarpenterMetrics(ctx context.Context, cs kubernetes.Interface) (*KarpenterMetrics, error) {
	raw, err := cs.CoreV1().
		Services(karpenterControllerNamespace).
		ProxyGet("http", karpenterControllerService, karpenterMetricsPort, "/metrics", nil).
		DoRaw(ctx)
	if err != nil {
		return nil, fmt.Errorf("scrape karpenter metrics: %w", err)
	}
	return parseKarpenterMetrics(raw)
}

// parseKarpenterMetrics parses the Prometheus exposition text
// format. Pure function so tests can feed planted bytes without an
// apiserver fixture.
func parseKarpenterMetrics(raw []byte) (*KarpenterMetrics, error) {
	out := &KarpenterMetrics{
		NodePoolUsage: map[string]map[string]float64{},
		NodePoolLimit: map[string]map[string]float64{},
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	// /metrics responses can be large on busy controllers; expand
	// the per-line buffer well past the default 64 KiB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, value, ok := parseExpositionLine(line)
		if !ok {
			continue
		}
		if _, want := targetMetrics[name]; !want {
			continue
		}
		switch name {
		case "karpenter_cloudprovider_instance_type_offering_price_estimate":
			pm := karpenterPriceMetric{
				InstanceType: labels["instance_type"],
				CapacityType: labels["capacity_type"],
				Zone:         labels["zone"],
				PricePerHour: value,
			}
			if pm.InstanceType == "" {
				continue
			}
			out.Prices = append(out.Prices, pm)
		case "karpenter_nodepools_usage":
			recordPoolMetric(out.NodePoolUsage, labels, value)
		case "karpenter_nodepools_limit":
			recordPoolMetric(out.NodePoolLimit, labels, value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan exposition: %w", err)
	}
	return out, nil
}

// recordPoolMetric writes one (nodepool, resource_type) → value
// observation into the nested map. Skips entries missing either
// label so partial / malformed lines don't poison the result.
func recordPoolMetric(dst map[string]map[string]float64, labels map[string]string, v float64) {
	pool := labels["nodepool"]
	rt := labels["resource_type"]
	if pool == "" || rt == "" {
		return
	}
	if _, ok := dst[pool]; !ok {
		dst[pool] = map[string]float64{}
	}
	dst[pool][rt] = v
}

// parseExpositionLine extracts (metricName, labels, value) from one
// non-comment line of Prometheus text exposition.
//
// Grammar handled (subset — the +Inf / NaN cases and the optional
// timestamp suffix are not relevant for the gauges we read):
//
//   metric_name{label1="value1",label2="value2"} 12.34
//   metric_name 12.34
//
// Returns ok=false on any structural deviation (the line is then
// silently skipped — the parser is a best-effort filter, not a
// strict validator).
func parseExpositionLine(line string) (name string, labels map[string]string, value float64, ok bool) {
	// Split into name+labels block and value block.
	openBrace := strings.Index(line, "{")
	var preValue string
	if openBrace >= 0 {
		closeBrace := strings.Index(line, "}")
		if closeBrace < 0 || closeBrace < openBrace {
			return "", nil, 0, false
		}
		name = line[:openBrace]
		labels = parseLabelList(line[openBrace+1 : closeBrace])
		preValue = strings.TrimSpace(line[closeBrace+1:])
	} else {
		// Bare `name value` form (no labels).
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", nil, 0, false
		}
		name = fields[0]
		labels = map[string]string{}
		preValue = strings.Join(fields[1:], " ")
	}
	// preValue is "<value>" or "<value> <timestamp>" — take the first field.
	parts := strings.Fields(preValue)
	if len(parts) == 0 {
		return "", nil, 0, false
	}
	v, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return "", nil, 0, false
	}
	return strings.TrimSpace(name), labels, v, true
}

// parseLabelList parses the contents BETWEEN the `{` and `}` of a
// metric line. Handles the standard `key="value"` form with
// comma-separated entries; quotes inside values are preserved as-is
// (Karpenter doesn't escape any of the labels we care about).
func parseLabelList(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range splitLabelEntries(s) {
		eq := strings.Index(kv, "=")
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(kv[:eq])
		val := strings.TrimSpace(kv[eq+1:])
		val = strings.TrimPrefix(val, `"`)
		val = strings.TrimSuffix(val, `"`)
		if key != "" {
			out[key] = val
		}
	}
	return out
}

// splitLabelEntries respects quoted values when splitting on commas.
// Karpenter's labels don't contain commas in values, but a robust
// parser should still handle the common case.
func splitLabelEntries(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch ch {
		case '"':
			inQuotes = !inQuotes
			cur.WriteByte(ch)
		case ',':
			if inQuotes {
				cur.WriteByte(ch)
			} else {
				if cur.Len() > 0 {
					out = append(out, cur.String())
				}
				cur.Reset()
			}
		default:
			cur.WriteByte(ch)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// ─── cost compute ──────────────────────────────────────────────────────────

// ComputeKarpenterCosts attaches per-NodePool $/hr + spot-savings to
// the supplied NodePoolView slice IN PLACE. Pools with no claims
// (or claims whose label triplet doesn't match any price entry) get
// Cost=nil — callers should not assume `pools[i].Cost != nil`.
//
// Also fills NodePoolView.Usage when the metrics carried it, and
// updates NodePoolView.NodeCount from the claims slice (this happens
// here rather than at list-time so a single pass over claims drives
// both the count and the cost roll-up).
func ComputeKarpenterCosts(pools []NodePoolView, claims []NodeClaimView, m *KarpenterMetrics) {
	byPool := map[string][]NodeClaimView{}
	for _, c := range claims {
		byPool[c.NodePool] = append(byPool[c.NodePool], c)
	}
	priceKey := func(it, ct, z string) string { return it + "|" + ct + "|" + z }
	priceByKey := map[string]float64{}
	if m != nil {
		for _, p := range m.Prices {
			priceByKey[priceKey(p.InstanceType, p.CapacityType, p.Zone)] = p.PricePerHour
		}
	}

	for i := range pools {
		pool := &pools[i]
		pool.NodeCount = len(byPool[pool.Name])
		if m != nil {
			if usage, ok := m.NodePoolUsage[pool.Name]; ok && len(usage) > 0 {
				pool.Usage = floatMapToString(usage)
			}
		}
		if m == nil {
			continue
		}
		poolClaims := byPool[pool.Name]
		if len(poolClaims) == 0 {
			continue
		}
		var current, onDemand float64
		matched := 0
		for _, c := range poolClaims {
			if p, ok := priceByKey[priceKey(c.InstanceType, c.CapacityType, c.Zone)]; ok {
				current += p
				matched++
			}
			if p, ok := priceByKey[priceKey(c.InstanceType, "on-demand", c.Zone)]; ok {
				onDemand += p
			} else if p, ok := priceByKey[priceKey(c.InstanceType, c.CapacityType, c.Zone)]; ok {
				// On-demand price missing — fall back to current. Spot
				// savings then computes as 0% (honest about "we couldn't
				// price the alternative").
				onDemand += p
			}
		}
		if matched == 0 {
			continue
		}
		cost := NodePoolCost{
			CurrentHourly:  current,
			OnDemandHourly: onDemand,
		}
		if onDemand > 0 && onDemand >= current {
			cost.SpotSavingsPct = int((1 - current/onDemand) * 100)
		}
		pool.Cost = &cost
	}
}

func floatMapToString(in map[string]float64) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = strconv.FormatFloat(v, 'f', -1, 64)
	}
	return out
}
