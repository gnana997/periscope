package cve

import "time"

// Wire-only DTOs for the HTTP API. The store's internal types
// (ref counts, channels, embedded mutex on Store) never serialise —
// these struct fields have JSON tags and only carry what the SPA
// reads.

// WireSeverityCounts is the JSON envelope around the in-memory
// SeverityCounts aggregator. Splitting it lets the aggregator stay
// representation-agnostic (severity.go) while the wire type carries
// JSON tags compatible with the SPA's existing chip code.
type WireSeverityCounts struct {
	Critical      int `json:"critical"`
	High          int `json:"high"`
	Medium        int `json:"medium"`
	Low           int `json:"low"`
	Informational int `json:"informational"`
}

// WireSeverity returns a WireSeverityCounts view of c for direct JSON
// encoding. The two types share field layout — they intentionally do, so
// the wire shape can pick up its JSON tags without the aggregator having
// to know about them — so a plain conversion is sufficient.
func WireSeverity(c SeverityCounts) WireSeverityCounts {
	return WireSeverityCounts(c)
}

// OwnerRef is the projection of (OwnerKind, OwnerName) used on the
// wire so the SPA can render "managed-nodegroup: ng-prod" or
// "karpenter-nodeclaim: default-9f3kz" without re-deriving the kind.
type OwnerRef struct {
	Kind OwnerKind `json:"kind"`
	Name string    `json:"name,omitempty"`
}

// InstanceRow is the per-EC2-instance wire row for /cve/by-instance.
type InstanceRow struct {
	InstanceID     string             `json:"instanceId"`
	Owner          OwnerRef           `json:"owner"`
	AMI            string             `json:"ami,omitempty"`
	SeverityCounts WireSeverityCounts `json:"severityCounts"`
	LastFetchedAt  time.Time          `json:"lastFetchedAt"`
}

// InstancesResp is the envelope for /cve/by-instance. inspectorEnabled
// + hydrated mirror the /cve/status flags so the SPA can render the
// empty-state hint from a single response.
type InstancesResp struct {
	Instances        []InstanceRow `json:"instances"`
	InspectorEnabled bool          `json:"inspectorEnabled"`
	Hydrated         bool          `json:"hydrated"`
}

// FindingsResp is the envelope for /cve/by-instance/{id} and
// /cve/by-digest/{digest}. Each Finding already carries its own
// pre-built Inspector deep-link URL (see awsinspector.Finding).
type FindingsResp struct {
	Findings         []Finding `json:"findings"`
	LastFetchedAt    time.Time `json:"lastFetchedAt"`
	InspectorEnabled bool      `json:"inspectorEnabled"`
	Hydrated         bool      `json:"hydrated"`
}

// ContainerRow is the per-container wire row inside a PodRow.
// SeverityCounts is a pointer so containers in ScanStateNonECR /
// ScanStatePending can omit the field entirely (the SPA renders a
// "not scanned" pill instead of zeros).
type ContainerRow struct {
	Name           string              `json:"name"`
	Image          string              `json:"image"`
	Digest         string              `json:"digest,omitempty"`
	ScanState      ScanState           `json:"scanState"`
	SeverityCounts *WireSeverityCounts `json:"severityCounts,omitempty"`
	// Packages is the per-package grouped + prioritized projection
	// of this container's findings. Server-side grouping means SPA
	// and MCP/AI-agent tool calls share one source of truth. Omitted
	// (nil) on the listing endpoint to keep response sizes small;
	// populated on detail + by-workload endpoints.
	Packages []PackageGroup `json:"packages,omitempty"`
}

// ScanCoverage classifies a pod's overall scan completeness for the
// /cve/pods surface. Computed by rollupCoverage in cve_handler.go.
type ScanCoverage string

const (
	CoverageFull    ScanCoverage = "full"    // every container is scanned ECR
	CoveragePartial ScanCoverage = "partial" // at least one scanned + at least one non-ecr/pending
	CoverageNone    ScanCoverage = "none"    // zero containers scanned
)

// PodRow is the per-pod wire row for /cve/pods.
type PodRow struct {
	Namespace              string             `json:"namespace"`
	Name                   string             `json:"name"`
	Containers             []ContainerRow     `json:"containers"`
	RolledUpSeverityCounts WireSeverityCounts `json:"rolledUpSeverityCounts"`
	ScanCoverage           ScanCoverage       `json:"scanCoverage"`
}

// PodsResp is the envelope for /cve/pods. Next is a base64-encoded
// "namespace/name" cursor; the SPA round-trips it via the ?cursor=
// query param.
type PodsResp struct {
	Pods             []PodRow `json:"pods"`
	Next             string   `json:"next,omitempty"`
	InspectorEnabled bool     `json:"inspectorEnabled"`
	Hydrated         bool     `json:"hydrated"`
}

// EntryCountsWire is the JSON projection of (Store.EntryCounts()).
type EntryCountsWire struct {
	Digests   int `json:"digests"`
	Instances int `json:"instances"`
}

// StatusResp is the envelope for /cve/status. LastHydrate is zero
// (and omitted) when the store has not been hydrated yet.
type StatusResp struct {
	InspectorEnabled bool            `json:"inspectorEnabled"`
	Hydrated         bool            `json:"hydrated"`
	LastHydrate      time.Time       `json:"lastHydrate,omitempty"`
	EntryCounts      EntryCountsWire `json:"entryCounts"`
}

// RefreshReq is the POST body for /cve/refresh. Both fields are
// optional; an empty request is a no-op that still emits one audit
// row (operator may want to log "I checked CVE state" without forcing
// a fetch).
type RefreshReq struct {
	Digests     []string `json:"digests"`
	InstanceIDs []string `json:"instanceIds"`
}

// WorkloadRef is the echoed identity of the queried workload on
// /cve/by-workload responses. Lets the SPA assert it got data back
// for the right entity without re-parsing the URL.
type WorkloadRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// WorkloadCveResp is the envelope for /cve/by-workload. Pods is one
// PodRow per matched pod (no server-side dedup — the SPA collapses
// duplicate digests across replicas client-side via useMemo, per the
// v1.1 design decision). RolledUpSeverityCounts + ScanCoverage are
// the workload-level aggregates so the chip surface on the workload
// row can render without re-walking pods.
type WorkloadCveResp struct {
	Workload               WorkloadRef        `json:"workload"`
	Pods                   []PodRow           `json:"pods"`
	RolledUpSeverityCounts WireSeverityCounts `json:"rolledUpSeverityCounts"`
	ScanCoverage           ScanCoverage       `json:"scanCoverage"`
	InspectorEnabled       bool               `json:"inspectorEnabled"`
	Hydrated               bool               `json:"hydrated"`
}
