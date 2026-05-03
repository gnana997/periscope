import { useMemo } from "react";
import type { AuditRow } from "../../lib/types";

interface AuditTimelineStripProps {
  rows: AuditRow[];
  /** RFC3339 — defines the strip's left edge. */
  from?: string;
  /** RFC3339 — defines the strip's right edge. Defaults to "now". */
  to?: string;
}

const BUCKET_COUNT = 60;

/**
 * AuditTimelineStrip — a tiny stacked-bar histogram showing event
 * density across the time range.
 *
 * Each bar is a vertical column representing one of BUCKET_COUNT
 * equal-width time slices. Within each column, denials stack on the
 * red band, failures on yellow, successes on green. The eye scans for
 * red columns instinctively.
 *
 * Hover any column → tooltip with bucket time range + counts.
 *
 * Renders nothing when the time range is invalid or the result set
 * is empty.
 */
export function AuditTimelineStrip({
  rows,
  from,
  to,
}: AuditTimelineStripProps) {
  const buckets = useMemo(
    () => bucketize(rows, from, to, BUCKET_COUNT),
    [rows, from, to],
  );

  if (!buckets || buckets.maxTotal === 0) {
    return null;
  }

  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-border bg-surface px-3 py-2.5">
      <div className="flex items-baseline justify-between">
        <span className="font-mono text-[10.5px] uppercase tracking-wide text-ink-faint">
          density
        </span>
        <span className="font-mono text-[10.5px] text-ink-faint tabular">
          {buckets.totalEvents} events · peak {buckets.maxTotal}/bucket
        </span>
      </div>
      <div
        className="flex h-[44px] items-end gap-px"
        role="img"
        aria-label={`Event density across ${BUCKET_COUNT} buckets, peaking at ${buckets.maxTotal} events per bucket.`}
      >
        {buckets.bars.map((b, i) => {
          const heightPct = b.total / buckets.maxTotal;
          // Pixel height — keep at least 1px on non-empty buckets so
          // they're visible against the surface.
          const px = Math.max(b.total > 0 ? 2 : 0, Math.round(heightPct * 44));
          // Stack proportions inside the bar
          const successPx = b.total ? Math.round((b.success / b.total) * px) : 0;
          const failurePx = b.total ? Math.round((b.failure / b.total) * px) : 0;
          // Denied takes the remaining height — avoids cumulative rounding gaps
          const deniedPx = px - successPx - failurePx;
          return (
            <div
              key={i}
              title={tooltipFor(b)}
              className="flex flex-1 cursor-help flex-col-reverse"
              style={{ minWidth: "2px" }}
            >
              {successPx > 0 && (
                <div style={{ height: `${successPx}px` }} className="bg-green" />
              )}
              {failurePx > 0 && (
                <div
                  style={{ height: `${failurePx}px` }}
                  className="bg-yellow"
                />
              )}
              {deniedPx > 0 && (
                <div style={{ height: `${deniedPx}px` }} className="bg-red" />
              )}
            </div>
          );
        })}
      </div>
      <div className="flex justify-between font-mono text-[10px] text-ink-faint tabular">
        <span>{shortTime(buckets.from)}</span>
        <span>{shortTime(buckets.to)}</span>
      </div>
    </div>
  );
}

interface Bar {
  fromMs: number;
  toMs: number;
  success: number;
  failure: number;
  denied: number;
  total: number;
}

interface BucketResult {
  bars: Bar[];
  maxTotal: number;
  totalEvents: number;
  from: number;
  to: number;
}

function bucketize(
  rows: AuditRow[],
  fromIso: string | undefined,
  toIso: string | undefined,
  count: number,
): BucketResult | null {
  // Default to "show me the spread of rows we have" if from/to aren't set.
  let fromMs = fromIso ? Date.parse(fromIso) : NaN;
  let toMs = toIso ? Date.parse(toIso) : NaN;

  if (Number.isNaN(fromMs) || Number.isNaN(toMs)) {
    if (rows.length === 0) return null;
    const all = rows
      .map((r) => Date.parse(r.timestamp))
      .filter((n) => !Number.isNaN(n))
      .sort((a, b) => a - b);
    if (all.length === 0) return null;
    fromMs = all[0];
    toMs = all[all.length - 1];
  }
  if (toMs <= fromMs) return null;

  const span = toMs - fromMs;
  const bucketSize = span / count;

  const bars: Bar[] = Array.from({ length: count }, (_, i) => ({
    fromMs: fromMs + i * bucketSize,
    toMs: fromMs + (i + 1) * bucketSize,
    success: 0,
    failure: 0,
    denied: 0,
    total: 0,
  }));

  for (const r of rows) {
    const t = Date.parse(r.timestamp);
    if (Number.isNaN(t) || t < fromMs || t > toMs) continue;
    let idx = Math.floor((t - fromMs) / bucketSize);
    if (idx >= count) idx = count - 1;
    if (idx < 0) idx = 0;
    const b = bars[idx];
    if (r.outcome === "success") b.success++;
    else if (r.outcome === "failure") b.failure++;
    else if (r.outcome === "denied") b.denied++;
    b.total++;
  }

  let maxTotal = 0;
  let totalEvents = 0;
  for (const b of bars) {
    if (b.total > maxTotal) maxTotal = b.total;
    totalEvents += b.total;
  }

  return { bars, maxTotal, totalEvents, from: fromMs, to: toMs };
}

function tooltipFor(b: Bar): string {
  if (b.total === 0) return `${shortTime(b.fromMs)}: no events`;
  const parts = [
    `${shortTime(b.fromMs)}–${shortTime(b.toMs)}: ${b.total} event${b.total === 1 ? "" : "s"}`,
  ];
  if (b.success) parts.push(`${b.success} success`);
  if (b.failure) parts.push(`${b.failure} failure`);
  if (b.denied) parts.push(`${b.denied} denied`);
  return parts.join(" · ");
}

function shortTime(ms: number): string {
  const d = new Date(ms);
  if (Number.isNaN(d.getTime())) return "—";
  // HH:MM if today; MM-DD HH:MM otherwise.
  const now = new Date();
  if (d.toDateString() === now.toDateString()) {
    return d.toLocaleTimeString("en-GB", {
      hour12: false,
      hour: "2-digit",
      minute: "2-digit",
    });
  }
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  const hh = String(d.getHours()).padStart(2, "0");
  const mi = String(d.getMinutes()).padStart(2, "0");
  return `${mm}-${dd} ${hh}:${mi}`;
}
