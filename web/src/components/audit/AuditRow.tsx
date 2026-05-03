import { useNavigate, useParams } from "react-router-dom";
import { cn } from "../../lib/cn";
import type { AuditOutcome, AuditRow as AuditRowT } from "../../lib/types";

const OUTCOME_META: Record<
  AuditOutcome,
  { glyph: string; label: string; band: string; tint: string }
> = {
  success: {
    glyph: "●",
    label: "success",
    band: "bg-green",
    tint: "",
  },
  failure: {
    glyph: "▲",
    label: "failure",
    band: "bg-yellow",
    tint: "",
  },
  denied: {
    glyph: "✕",
    label: "denied",
    band: "bg-red",
    tint: "bg-red-soft/15",
  },
};

interface AuditRowProps {
  row: AuditRowT;
}

/**
 * Three-line row anatomy:
 *
 *   ▌ │ HH:MM:SS  actor@example.com                    verb       ›
 *     │ cluster / namespace / resource/name
 *     │ ↳ reason (italic, only when populated)              req#abc12
 *
 * The 3px colored band on the left is the dual-channel outcome signal
 * (color + glyph in tooltip). Click anywhere → /clusters/:c/audit/:id.
 */
export function AuditRow({ row }: AuditRowProps) {
  const navigate = useNavigate();
  const { cluster: clusterParam } = useParams<{ cluster: string }>();
  const meta = OUTCOME_META[row.outcome] ?? OUTCOME_META.failure;

  const handleClick = () => {
    if (!clusterParam) return;
    navigate(
      `/clusters/${encodeURIComponent(clusterParam)}/audit/${row.id}`,
    );
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      handleClick();
    }
  };

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      aria-label={`${meta.label} — ${row.verb} by ${row.actor.email ?? row.actor.sub} at ${row.timestamp}`}
      className={cn(
        "group relative flex cursor-pointer overflow-hidden rounded-md border border-border bg-surface transition-colors",
        "hover:border-border-strong hover:bg-surface-2",
        meta.tint,
      )}
    >
      {/* outcome band */}
      <span
        aria-hidden
        className={cn("w-[3px] shrink-0", meta.band)}
        title={meta.label}
      />

      <div className="flex flex-1 flex-col gap-0.5 px-3.5 py-2.5 text-[12.5px]">
        {/* line 1: time, actor, verb, drill-in arrow */}
        <div className="flex items-baseline gap-3">
          <span className="font-mono tabular text-[11.5px] text-ink-faint">
            {formatTime(row.timestamp)}
          </span>
          <span className="truncate text-ink">
            {row.actor.email || row.actor.sub}
          </span>
          <span className="ml-auto inline-flex items-center gap-2">
            <VerbPill verb={row.verb} />
            <span
              aria-hidden
              className="text-ink-muted transition-transform group-hover:translate-x-1"
            >
              ›
            </span>
          </span>
        </div>

        {/* line 2: target breadcrumb */}
        {hasTarget(row) && (
          <div className="font-mono text-[11px] text-ink-muted">
            {targetBreadcrumb(row)}
          </div>
        )}

        {/* line 3: reason + request id */}
        {(row.reason || row.requestId) && (
          <div className="flex items-baseline justify-between gap-3 text-[11px]">
            {row.reason ? (
              <span className="truncate italic text-ink-muted">
                ↳ {row.reason}
              </span>
            ) : (
              <span />
            )}
            {row.requestId && (
              <span className="shrink-0 font-mono text-[10.5px] text-ink-faint">
                req#{shortId(row.requestId)}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function VerbPill({ verb }: { verb: string }) {
  return (
    <span className="inline-flex items-center rounded border border-border bg-bg/50 px-1.5 py-0.5 font-mono text-[10.5px] uppercase tracking-wide text-ink-muted">
      {verb}
    </span>
  );
}

function hasTarget(row: AuditRowT): boolean {
  return Boolean(
    row.cluster ||
      row.resource.namespace ||
      row.resource.name ||
      row.resource.resource,
  );
}

function targetBreadcrumb(row: AuditRowT): string {
  const segs: string[] = [];
  if (row.cluster) segs.push(row.cluster);
  if (row.resource.namespace) segs.push(row.resource.namespace);
  const tail = resourceTail(row.resource);
  if (tail) segs.push(tail);
  return segs.join(" / ");
}

function resourceTail(r: AuditRowT["resource"]): string {
  // Render as `resources/name` (e.g. pods/api-7d8) or just `resources`
  // when the action targets the kind without a name.
  if (r.resource && r.name) return `${r.resource}/${r.name}`;
  if (r.resource) return r.resource;
  if (r.name) return r.name;
  return "";
}

function shortId(id: string): string {
  // chi's RequestID format is "<actor>/<base32>-<sequence>". The actor
  // prefix is constant within a single user's session, so the prefix
  // doesn't disambiguate rows. Show the trailing sequence (or the last
  // 8 chars if the format isn't recognized) so the reader can tell
  // adjacent requests apart at a glance.
  const dash = id.lastIndexOf("-");
  if (dash !== -1 && dash < id.length - 1) {
    return id.slice(dash + 1);
  }
  if (id.length <= 8) return id;
  return id.slice(-8);
}

/** HH:MM:SS today; "Yesterday HH:MM" yesterday; YYYY-MM-DD HH:MM older. */
function formatTime(ts: string): string {
  const t = new Date(ts);
  if (Number.isNaN(t.getTime())) return ts;
  const now = new Date();
  const sameDay = t.toDateString() === now.toDateString();
  if (sameDay) {
    return t.toLocaleTimeString("en-GB", { hour12: false });
  }
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  if (t.toDateString() === y.toDateString()) {
    return `Yesterday ${t.toLocaleTimeString("en-GB", {
      hour12: false,
      hour: "2-digit",
      minute: "2-digit",
    })}`;
  }
  const yyyy = t.getFullYear();
  const mm = String(t.getMonth() + 1).padStart(2, "0");
  const dd = String(t.getDate()).padStart(2, "0");
  const hh = String(t.getHours()).padStart(2, "0");
  const mi = String(t.getMinutes()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd} ${hh}:${mi}`;
}
