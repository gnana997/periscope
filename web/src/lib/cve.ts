// cve — pure-logic helpers for the CVE / Inspector v2 surface (#166).
//
// All functions are side-effect-free and deterministic so the chip
// columns, SecurityTab variants, banner, and pod-summary page-walker
// can be exercised by lib/cve.test.ts without touching React or DOM.
//
// Anything component-shaped (JSX, hooks) stays out of this module —
// see severity.ts for the scoring/label core and severity.test.ts
// for the existing test template.

import { combineCounts, type ScanState } from "./severity";
import { ApiError } from "./api";
import type {
  CveContainerRow,
  CveFinding,
  CvePodRow,
  CvePodSummary,
  CvePodsResp,
  CveScanCoverage,
  CveSeverityCounts,
  CveStatusResp,
  Pod,
} from "./types";

// ── providerID parsing ─────────────────────────────────────────────

/** extractInstanceId returns the EC2 instance id ("i-0abc…") from a
 *  Kubernetes `Node.spec.providerID` ("aws:///us-east-1a/i-0abc…")
 *  or a Karpenter NodeClaim providerID.
 *
 *  Returns "" for:
 *    - empty / undefined input (bare-metal / kind / pre-Initialized claim),
 *    - any providerID that doesn't end in `/i-<hex>` (other cloud providers). */
export function extractInstanceId(providerID?: string | null): string {
  if (!providerID) return "";
  const m = providerID.match(/\/(i-[a-f0-9]+)$/);
  return m ? m[1] : "";
}

// ── pod-summary keys + page-walk reducer ───────────────────────────

/** podKey is the lookup key the Pods-page chip column reads from
 *  the per-cluster summary Map. Keep here so the column accessor and
 *  the page-walker can't drift. */
export function podKey(ns: string, name: string): string {
  return `${ns}/${name}`;
}

/** summarizePodRow projects one /cve/pods page-row into the compact
 *  shape the chip column needs. */
export function summarizePodRow(p: CvePodRow): CvePodSummary {
  return {
    namespace: p.namespace,
    name: p.name,
    counts: p.rolledUpSeverityCounts,
    coverage: p.scanCoverage,
  };
}

/** accumulatePodSummaries collapses a sequence of /cve/pods pages
 *  into a single `Map<ns/name, summary>` keyed by podKey. Stable
 *  under page replay (later pages win on duplicate keys, which only
 *  happens if the backend changed between requests — fresher wins). */
export function accumulatePodSummaries(
  pages: CvePodsResp[],
): Map<string, CvePodSummary> {
  const out = new Map<string, CvePodSummary>();
  for (const page of pages) {
    for (const p of page.pods) {
      out.set(podKey(p.namespace, p.name), summarizePodRow(p));
    }
  }
  return out;
}

// ── severity rollups ───────────────────────────────────────────────

/** countSeverities tallies an array of findings into a SeverityCounts
 *  bucket. "INFO" is treated as "INFORMATIONAL". Unknown severities
 *  are dropped (defensive — Inspector has historically introduced new
 *  buckets, and we'd rather under-count than crash on an unrecognised
 *  string). */
export function countSeverities(findings: CveFinding[]): CveSeverityCounts {
  const c: CveSeverityCounts = {
    critical: 0,
    high: 0,
    medium: 0,
    low: 0,
    informational: 0,
  };
  for (const f of findings) {
    const s = (f.severity ?? "").toUpperCase();
    if (s === "CRITICAL") c.critical++;
    else if (s === "HIGH") c.high++;
    else if (s === "MEDIUM") c.medium++;
    else if (s === "LOW") c.low++;
    else if (s === "INFORMATIONAL" || s === "INFO") c.informational++;
  }
  return c;
}

/** countVulnerable returns the number of pods in `pods` that have
 *  at least one critical or high finding in `map`. Used by the
 *  Pods-page "vulnerable only" filter chip count. Undefined `map`
 *  (chip data not yet loaded) returns 0 — the chip just renders
 *  with no count rather than as "0 vulnerable". */
export function countVulnerable(
  pods: Pod[],
  map: Map<string, { counts: CveSeverityCounts }> | undefined,
): number {
  if (!map) return 0;
  let n = 0;
  for (const p of pods) {
    const s = map.get(podKey(p.namespace, p.name));
    if (s && (s.counts.critical > 0 || s.counts.high > 0)) n++;
  }
  return n;
}

/** Minimal shape needed to aggregate severity across Karpenter
 *  NodeClaims (or any other instance-keyed entity). Kept structural
 *  so the helper doesn't drag in NodeClaimView from elsewhere. */
export interface InstanceKeyed {
  providerID?: string;
}

/** aggregateClaimSeverity rolls up per-instance severity counts for
 *  a list of NodeClaim-shaped objects keyed by their EC2 providerID.
 *  Claims without a usable providerID or without an entry in
 *  `cveByInstance` are skipped. */
export function aggregateClaimSeverity(
  claims: InstanceKeyed[],
  cveByInstance: Map<string, CveSeverityCounts>,
): CveSeverityCounts {
  const slices: CveSeverityCounts[] = [];
  for (const c of claims) {
    const id = extractInstanceId(c.providerID);
    if (!id) continue;
    const s = cveByInstance.get(id);
    if (s) slices.push(s);
  }
  return combineCounts(...slices);
}

// ── scan-state classification ──────────────────────────────────────

/** coverageToState maps a /cve/pods coverage flag to a chip ScanState.
 *  "full" is reported as `has-findings`; the chip downshifts to
 *  `clean` when the counts are zero so we don't render a misleading
 *  red label on a fully scanned but vulnerability-free pod. */
export function coverageToState(c: CveScanCoverage): ScanState {
  switch (c) {
    case "full":
      return "has-findings";
    case "partial":
      return "partial";
    case "none":
      return "non-ecr";
  }
}

/** containerScanState classifies a single CveContainerRow into the
 *  chip-level state. Falls through to `clean` only when the container
 *  is scanned AND every severity bucket is empty. */
export function containerScanState(c: CveContainerRow): ScanState {
  if (c.scanState === "non-ecr") return "non-ecr";
  if (c.scanState === "pending") return "pending";
  if (
    c.severityCounts &&
    c.severityCounts.critical +
      c.severityCounts.high +
      c.severityCounts.medium +
      c.severityCounts.low >
      0
  ) {
    return "has-findings";
  }
  return "clean";
}

// ── digest collection ──────────────────────────────────────────────

/** collectDigests returns the unique set of image digests across the
 *  given containers. Empty / missing digests (image-tag-only refs
 *  for non-ECR sidecars) are skipped. */
export function collectDigests(containers: CveContainerRow[]): string[] {
  const set = new Set<string>();
  for (const c of containers) {
    if (c.digest) set.add(c.digest);
  }
  return Array.from(set);
}

/** collectDigestsAcrossPods extends collectDigests to an array of
 *  pod rows (the workload SecurityTab uses this to build the
 *  invalidation key list when the operator hits refresh). */
export function collectDigestsAcrossPods(pods: CvePodRow[]): string[] {
  const set = new Set<string>();
  for (const p of pods) {
    for (const c of p.containers) {
      if (c.digest) set.add(c.digest);
    }
  }
  return Array.from(set);
}

// ── workload container dedup ───────────────────────────────────────

export interface DedupedContainer {
  row: CveContainerRow;
  podCount: number;
  name: string;
  digest?: string;
}

/** dedupContainersByDigest collapses replica containers in a
 *  WorkloadCveResp.pods list into one row per (container name,
 *  digest|image). The `podCount` annotation tells the operator how
 *  many replicas use that exact container shape.
 *
 *  Two containers with the same NAME but different digests
 *  (mid-rollout, multi-arch image) are kept separate so the operator
 *  sees both digests' findings rather than a misleading merge. */
export function dedupContainersByDigest(pods: CvePodRow[]): DedupedContainer[] {
  const byKey = new Map<string, { row: CveContainerRow; podCount: number }>();
  for (const p of pods) {
    for (const c of p.containers) {
      const key = `${c.name}|${c.digest ?? c.image}`;
      const entry = byKey.get(key);
      if (entry) {
        entry.podCount++;
      } else {
        byKey.set(key, { row: c, podCount: 1 });
      }
    }
  }
  return Array.from(byKey.values()).map((e) => ({
    row: e.row,
    podCount: e.podCount,
    name: e.row.name,
    digest: e.row.digest,
  }));
}

// ── timestamp formatting ───────────────────────────────────────────

/** humanizeAge returns "Xs ago" / "Xm ago" / "Xh ago" / "Xd ago" for
 *  an ISO timestamp. `now` is injected (defaults to Date.now()) so
 *  the tests can pin a deterministic clock without monkey-patching
 *  global Date. Returns the input string verbatim on a parse error
 *  rather than throwing — the header should never crash because of
 *  a bad timestamp from the backend. */
export function humanizeAge(iso: string, now: number = Date.now()): string {
  try {
    const then = new Date(iso).getTime();
    if (Number.isNaN(then)) return iso;
    const diffSec = Math.max(0, Math.floor((now - then) / 1000));
    if (diffSec < 60) return `${diffSec}s ago`;
    if (diffSec < 3600) return `${Math.floor(diffSec / 60)}m ago`;
    if (diffSec < 86400) return `${Math.floor(diffSec / 3600)}h ago`;
    return `${Math.floor(diffSec / 86400)}d ago`;
  } catch {
    return iso;
  }
}

// ── empty-state banner decision ────────────────────────────────────

/** isBannerVisible decides whether the SecurityEmptyBanner should
 *  render for a given cluster. The component layers a useState bump
 *  on top of this to re-derive dismissed-state on click; the actual
 *  visibility predicate is pure and testable here.
 *
 *  Hides when:
 *    - `cluster` is empty (defensive — between cluster switches),
 *    - `status` is still loading (avoid flicker on first mount),
 *    - Inspector v2 is enabled on the cluster,
 *    - the operator has dismissed the banner for this cluster. */
export function isBannerVisible(args: {
  cluster: string;
  status: CveStatusResp | undefined;
  dismissed: boolean;
}): boolean {
  const { cluster, status, dismissed } = args;
  if (!cluster) return false;
  if (dismissed) return false;
  if (!status) return false;
  if (status.inspectorEnabled) return false;
  return true;
}

// ── error-classification predicates ────────────────────────────────

/** isInspectorDisabled — placeholder predicate for the inspector-
 *  disabled empty-state signal. The backend currently uses HTTP 200
 *  + inspectorEnabled:false rather than a 4xx, so a thrown ApiError
 *  is genuinely an error (network / 5xx) and not the empty-state
 *  signal. Kept as a stable seam: when/if the backend adopts a
 *  status code for this case, the predicate gets a body without the
 *  call sites changing. */
export function isInspectorDisabled(err: unknown): boolean {
  if (!(err instanceof ApiError)) return false;
  return false;
}
