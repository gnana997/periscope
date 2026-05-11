// SeverityChip — compact + verbose chip variants for the CVE
// surface (#166).
//
// JSX shell over `lib/severity.ts`. Two modes:
//   - compact: 2C · 5H · 12M  (list-row column form). Tooltip shows
//     the full breakdown + scannedAt.
//   - verbose: 2 critical · 5 high · 12 medium · 3 low · 0 info
//     (Security tab header). No tooltip — already verbose.
//
// State branching: when ScanState !== "has-findings", the chip
// renders a muted state label (`clean`, `partial scan`, `pending`,
// `not scanned (non-ECR)`, `not scanned`) so non-scanned containers
// don't read as misleadingly "clean".

import type { CveSeverityCounts } from "../../lib/types";
import { Tooltip } from "../Tooltip";
import { cn } from "../../lib/cn";
import {
  compactLabel,
  hasAnyFindings,
  type ScanState,
  stateLabel,
  verboseLabel,
} from "../../lib/severity";

interface BaseProps {
  counts: CveSeverityCounts;
  state: ScanState;
  scannedAt?: string;
  className?: string;
}

type SeverityChipProps =
  | (BaseProps & { mode: "compact" })
  | (BaseProps & { mode: "verbose" });

export function SeverityChip(props: SeverityChipProps) {
  const { counts, state, scannedAt, className } = props;

  // When the entity has no findings AT ALL, the state determines
  // what we render. `has-findings` is the only state that uses the
  // count labels; everything else gets a muted state phrase.
  if (state !== "has-findings" || !hasAnyFindings(counts)) {
    const muted = state === "clean" || state === "non-ecr" || state === "unscanned" || state === "pending";
    return (
      <span
        className={cn(
          "inline-flex items-center font-mono text-[11.5px]",
          muted ? "text-ink-faint" : "text-yellow",
          className,
        )}
      >
        · {stateLabel(state)} ·
      </span>
    );
  }

  if (props.mode === "verbose") {
    return (
      <span
        className={cn(
          "inline-flex flex-wrap items-center gap-x-2 font-mono text-[11.5px] text-ink-muted",
          className,
        )}
      >
        {counts.critical > 0 && <ChipBucket count={counts.critical} label="critical" tone="critical" />}
        {counts.high > 0 && <ChipBucket count={counts.high} label="high" tone="high" />}
        {counts.medium > 0 && <ChipBucket count={counts.medium} label="medium" tone="medium" />}
        {counts.low > 0 && <ChipBucket count={counts.low} label="low" tone="low" />}
        {counts.informational > 0 && <ChipBucket count={counts.informational} label="info" tone="info" />}
        {scannedAt && (
          <span className="text-ink-faint">· scanned {scannedAt}</span>
        )}
      </span>
    );
  }

  // compact: 2C · 5H · 12M, tooltip shows full breakdown.
  return (
    <Tooltip content={tooltipBody(counts, scannedAt)}>
      <span
        className={cn(
          "inline-flex items-center gap-x-1.5 font-mono text-[11.5px]",
          className,
        )}
      >
        {counts.critical > 0 && (
          <span className="text-red">{counts.critical}C</span>
        )}
        {counts.high > 0 && (
          <span className="text-yellow">{counts.high}H</span>
        )}
        {counts.medium > 0 && (
          <span className="text-ink-muted">{counts.medium}M</span>
        )}
        {!hasTop3Visible(counts) && compactLabel(counts) === "" && (
          <span className="text-ink-faint">{verboseLabel(counts) || "—"}</span>
        )}
      </span>
    </Tooltip>
  );
}

function ChipBucket({
  count,
  label,
  tone,
}: {
  count: number;
  label: string;
  tone: "critical" | "high" | "medium" | "low" | "info";
}) {
  const toneClass =
    tone === "critical"
      ? "text-red"
      : tone === "high"
        ? "text-yellow"
        : tone === "medium"
          ? "text-ink"
          : "text-ink-faint";
  return (
    <span className={cn("inline-flex items-baseline gap-1", toneClass)}>
      <span className="tabular-nums">{count}</span>
      <span className="text-ink-faint">{label}</span>
    </span>
  );
}

function hasTop3Visible(c: CveSeverityCounts): boolean {
  return c.critical > 0 || c.high > 0 || c.medium > 0;
}

/** tooltipBody is rendered as Tooltip content — a plain ReactNode is
 *  acceptable, but we prefer a short string when no scan time is
 *  attached. The Tooltip component handles empty content by
 *  returning the children directly, so we always return a
 *  meaningful string. */
function tooltipBody(c: CveSeverityCounts, scannedAt?: string): string {
  const parts: string[] = [];
  if (c.critical > 0) parts.push(`${c.critical} critical`);
  if (c.high > 0) parts.push(`${c.high} high`);
  if (c.medium > 0) parts.push(`${c.medium} medium`);
  if (c.low > 0) parts.push(`${c.low} low`);
  if (c.informational > 0) parts.push(`${c.informational} info`);
  if (parts.length === 0) parts.push("clean");
  if (scannedAt) parts.push(`scanned ${scannedAt}`);
  return parts.join(" · ");
}
