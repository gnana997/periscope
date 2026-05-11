// severity — pure logic for the CVE chip surface (#166).
//
// Kept independent of any JSX / React so the SPA's node-only vitest
// can exercise it directly without RTL / jsdom. The SeverityChip
// component is a thin shell over the functions below; the test file
// next to this module covers score/label/state classification.

export interface SeverityCounts {
  critical: number;
  high: number;
  medium: number;
  low: number;
  informational: number;
}

/** ScanState classifies what the chip is allowed to render. The
 *  five non-empty buckets in SeverityCounts only matter when the
 *  state is `has-findings`; for every other state the chip renders a
 *  short label instead of count glyphs.
 *
 *  - `clean`:        scanned, zero findings.
 *  - `has-findings`: scanned, ≥ 1 finding in any bucket.
 *  - `partial`:      at least one container scanned + at least one not
 *                    scanned (workload / pod aggregate).
 *  - `pending`:      ECR image but the pod's containerStatus has no
 *                    digest yet (mid-pull).
 *  - `non-ecr`:      operator-written image is not in ECR — Inspector
 *                    v2 doesn't cover it. The chip renders a muted
 *                    label rather than zeros.
 *  - `unscanned`:    the cache doesn't have data for this entity yet
 *                    (cold-path hydrate in flight, or `inspectorEnabled
 *                    === false`). Looks like `· — ·`. */
export type ScanState =
  | "clean"
  | "has-findings"
  | "partial"
  | "pending"
  | "non-ecr"
  | "unscanned";

/** zero is the additive identity. Used as the starting value of
 *  combineCounts and as the default for entities with no findings
 *  yet. */
export const zero: SeverityCounts = {
  critical: 0,
  high: 0,
  medium: 0,
  low: 0,
  informational: 0,
};

/** severityScore reduces a SeverityCounts to a single sortable
 *  number that ranks worse-than-bad before less-than-bad.
 *
 *  critical*1000 + high*100 + medium*10 + low
 *
 *  Informational is intentionally excluded — sorting by info would
 *  surface uninteresting noise above genuine criticals. */
export function severityScore(c: SeverityCounts): number {
  return c.critical * 1000 + c.high * 100 + c.medium * 10 + c.low;
}

/** worstSeverity returns the highest-priority bucket with a non-
 *  zero count, or null if everything is zero. Used by the detail-tab
 *  indicator dot (critical / high → coloured dot; medium / low / info
 *  → no dot). */
export function worstSeverity(
  c: SeverityCounts,
): "critical" | "high" | "medium" | "low" | null {
  if (c.critical > 0) return "critical";
  if (c.high > 0) return "high";
  if (c.medium > 0) return "medium";
  if (c.low > 0) return "low";
  return null;
}

/** combineCounts folds N count slices into one. Used by the
 *  workload-level rollup (Deployment → sum across replicas) and the
 *  Karpenter NodePool header (sum across NodeClaim members). */
export function combineCounts(
  ...slices: ReadonlyArray<SeverityCounts>
): SeverityCounts {
  const out: SeverityCounts = { ...zero };
  for (const s of slices) {
    out.critical += s.critical;
    out.high += s.high;
    out.medium += s.medium;
    out.low += s.low;
    out.informational += s.informational;
  }
  return out;
}

/** compactLabel is the list-row column shape: `2C · 5H · 12M`.
 *  Drops low + informational from the inline view to keep the
 *  column narrow; tooltip surfaces the full breakdown. Returns an
 *  empty string when all top-3 buckets are zero — callers render
 *  the ScanState's text label instead. */
export function compactLabel(c: SeverityCounts): string {
  const parts: string[] = [];
  if (c.critical > 0) parts.push(`${c.critical}C`);
  if (c.high > 0) parts.push(`${c.high}H`);
  if (c.medium > 0) parts.push(`${c.medium}M`);
  return parts.join(" · ");
}

/** verboseLabel is the tab-header shape: full breakdown, spelled
 *  out. Empty buckets are dropped from the joined string so an
 *  entity with only criticals renders `2 critical`, not
 *  `2 critical · 0 high · 0 medium · 0 low · 0 info`. */
export function verboseLabel(c: SeverityCounts): string {
  const parts: string[] = [];
  if (c.critical > 0) parts.push(`${c.critical} critical`);
  if (c.high > 0) parts.push(`${c.high} high`);
  if (c.medium > 0) parts.push(`${c.medium} medium`);
  if (c.low > 0) parts.push(`${c.low} low`);
  if (c.informational > 0) parts.push(`${c.informational} info`);
  return parts.join(" · ");
}

/** hasAnyFindings returns true when at least one bucket is non-zero.
 *  The clean-vs-has-findings decision for the chip; tests should use
 *  this directly rather than re-deriving from severityScore. */
export function hasAnyFindings(c: SeverityCounts): boolean {
  return (
    c.critical > 0 ||
    c.high > 0 ||
    c.medium > 0 ||
    c.low > 0 ||
    c.informational > 0
  );
}

/** A short human label for a ScanState, used when counts are empty
 *  or not applicable. Kept here so the chip component stays JSX-
 *  only. */
export function stateLabel(state: ScanState): string {
  switch (state) {
    case "clean":
      return "clean";
    case "partial":
      return "partial scan";
    case "pending":
      return "scan pending";
    case "non-ecr":
      return "not scanned (non-ECR)";
    case "unscanned":
      return "not scanned";
    case "has-findings":
      // Caller should be rendering compactLabel/verboseLabel
      // instead, but return a safe fallback so the component never
      // renders an empty string.
      return "vulnerable";
  }
}
