import { useNavigate } from "react-router-dom";
import { cn } from "../../lib/cn";
import type { FleetClusterEntry, FleetStatus } from "../../lib/types";

/**
 * Status glyph + treatment table. Status is encoded dual-channel
 * (glyph + color), never color alone — accessibility requirement.
 */
const STATUS_META: Record<
  FleetStatus,
  { glyph: string; label: string; toneClass: string; cardClass: string }
> = {
  healthy: {
    glyph: "●",
    label: "healthy",
    toneClass: "text-green",
    cardClass: "",
  },
  degraded: {
    glyph: "◐",
    label: "degraded",
    toneClass: "text-yellow",
    cardClass: "border-l-[3px] border-l-yellow",
  },
  unreachable: {
    glyph: "✕",
    label: "unreachable",
    toneClass: "text-red",
    cardClass: "border-t-[3px] border-dashed border-t-red bg-red-soft/20",
  },
  unknown: {
    glyph: "○",
    label: "checking",
    toneClass: "text-ink-muted",
    cardClass: "opacity-80",
  },
  denied: {
    glyph: "⌀",
    label: "denied",
    toneClass: "text-ink-faint",
    cardClass: "opacity-60",
  },
};

interface ClusterCardProps {
  entry: FleetClusterEntry;
  isPinned: boolean;
  onTogglePin: (name: string) => void;
  onRetry: () => void;
}

export function ClusterCard({
  entry,
  isPinned,
  onTogglePin,
  onRetry,
}: ClusterCardProps) {
  const navigate = useNavigate();
  const meta = STATUS_META[entry.status];
  const drillIn = entry.status !== "denied";

  const handleClick = () => {
    if (!drillIn) return;
    navigate(`/clusters/${encodeURIComponent(entry.name)}/overview`);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (!drillIn) return;
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      handleClick();
    }
  };

  return (
    <div
      role={drillIn ? "button" : undefined}
      tabIndex={drillIn ? 0 : -1}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      aria-label={
        drillIn
          ? `Open ${entry.name} (${meta.label})`
          : `${entry.name} — ${meta.label}, no access`
      }
      className={cn(
        "group relative flex flex-col gap-3 rounded-md border border-border bg-surface px-4 py-3.5 text-[12.5px] transition-colors",
        drillIn && "cursor-pointer hover:border-border-strong hover:bg-surface-2",
        meta.cardClass,
      )}
    >
      {/* pin button — only visible on hover or when already pinned */}
      <button
        type="button"
        onClick={(e) => {
          e.stopPropagation();
          onTogglePin(entry.name);
        }}
        aria-label={isPinned ? `Unpin ${entry.name}` : `Pin ${entry.name}`}
        className={cn(
          "absolute right-2 top-2 rounded p-1 text-[12px] leading-none transition-opacity",
          isPinned
            ? "text-accent opacity-100"
            : "text-ink-faint opacity-40 hover:text-ink-muted hover:opacity-100",
        )}
      >
        {isPinned ? "★" : "☆"}
      </button>

      {/* identity row: glyph + name */}
      <div className="flex items-baseline gap-2 pr-7">
        <span
          className={cn(
            "text-[24px] leading-none",
            meta.toneClass,
            entry.status === "unknown" && "animate-pulse",
          )}
          aria-hidden
        >
          {meta.glyph}
        </span>
        <h3
          className="font-display text-[22px] leading-none tracking-[-0.01em] text-ink"
          style={{ fontWeight: 400 }}
        >
          {entry.name}
        </h3>
      </div>

      {/* location triplet */}
      <div className="font-mono text-[11px] text-ink-muted">
        {[
          entry.region,
          entry.backend?.toUpperCase(),
          entry.accountID && `acct …${entry.accountID.slice(-4)}`,
          entry.context && `ctx ${entry.context}`,
        ]
          .filter(Boolean)
          .join(" · ")}
        {entry.environment && (
          <span
            className="ml-2 inline-block rounded border border-border px-1 text-[10px] uppercase tracking-wide"
            title={`environment: ${entry.environment}`}
          >
            {entry.environment}
          </span>
        )}
      </div>

      {/* body — branches by status */}
      {entry.summary ? (
        <Vitals summary={entry.summary} />
      ) : entry.status === "denied" ? (
        <DeniedBody />
      ) : entry.status === "unknown" ? (
        <CheckingBody />
      ) : (
        <UnreachableBody error={entry.error?.message} />
      )}

      {/* hot signals — only when present */}
      {entry.hotSignals.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {entry.hotSignals.slice(0, 3).map((sig) => (
            <span
              key={sig.kind}
              className="inline-flex items-center gap-1 rounded border border-yellow/40 bg-yellow-soft px-1.5 py-0.5 font-mono text-[10.5px] text-yellow"
            >
              <span className="tabular">{sig.count}</span>
              <span>{prettyReason(sig.kind)}</span>
            </span>
          ))}
          {entry.hotSignals.length > 3 && (
            <span className="font-mono text-[10.5px] text-ink-muted">
              +{entry.hotSignals.length - 3} more
            </span>
          )}
        </div>
      )}

      {/* footer: freshness on the left, drill-in arrow OR retry on the right */}
      <div className="mt-auto flex items-center justify-between border-t border-border pt-2 font-mono text-[10.5px] text-ink-faint">
        <span>{relativeFromNow(entry.lastContact)}</span>
        {drillIn ? (
          <span className="text-ink-muted transition-transform group-hover:translate-x-1">
            →
          </span>
        ) : entry.status !== "denied" ? (
          <button
            type="button"
            onClick={(e) => {
              e.stopPropagation();
              onRetry();
            }}
            className="text-accent hover:underline"
          >
            retry
          </button>
        ) : null}
      </div>
    </div>
  );
}

function Vitals({ summary }: { summary: NonNullable<FleetClusterEntry["summary"]> }) {
  return (
    <div className="grid grid-cols-3 gap-2 rounded border border-border bg-bg/50 p-2 text-center">
      <Vital
        n={`${summary.nodes.ready}/${summary.nodes.total}`}
        label="nodes"
      />
      <Vital n={summary.pods.total} label="pods" />
      <Vital n={summary.namespaces} label="ns" />
    </div>
  );
}

function Vital({ n, label }: { n: number | string; label: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="font-mono text-[15px] tabular text-ink">{n}</span>
      <span className="font-mono text-[10px] uppercase tracking-wide text-ink-faint">
        {label}
      </span>
    </div>
  );
}

function DeniedBody() {
  return (
    <p className="rounded border border-dashed border-border px-2 py-3 text-center text-[11.5px] italic text-ink-muted">
      your role cannot access this cluster
    </p>
  );
}

function CheckingBody() {
  return (
    <div className="flex items-center justify-center gap-1 rounded border border-border bg-bg/50 px-2 py-4 text-[11.5px] text-ink-muted">
      <span className="animate-pulse">checking</span>
      <span className="animate-pulse">…</span>
    </div>
  );
}

function UnreachableBody({ error }: { error?: string }) {
  return (
    <div className="flex flex-col gap-1 rounded border border-red/30 bg-red-soft/40 px-2 py-2 text-[11.5px] text-red">
      <span className="font-mono text-[10.5px] uppercase tracking-wide">
        unreachable
      </span>
      {error && (
        <span className="line-clamp-2 font-mono text-[10.5px] text-ink-muted">
          {error}
        </span>
      )}
    </div>
  );
}

/** Convert "CrashLoopBackOff" → "crashloop". One-word lowercased token. */
function prettyReason(kind: string): string {
  const map: Record<string, string> = {
    CrashLoopBackOff: "crashloop",
    ImagePullBackOff: "imagepull",
    ErrImagePull: "imagepull",
    OOMKilled: "oomkilled",
    Evicted: "evicted",
    Pending: "pending",
    Unschedulable: "unschedulable",
    ContainerStatusUnknown: "unknown",
  };
  return map[kind] ?? kind.toLowerCase();
}

/** Return a short "Ns ago" / "Nm ago" string from an RFC3339 timestamp. */
function relativeFromNow(ts: string): string {
  const t = Date.parse(ts);
  if (Number.isNaN(t)) return "—";
  const sec = Math.max(0, Math.round((Date.now() - t) / 1000));
  if (sec < 60) return `checked ${sec}s ago`;
  const min = Math.round(sec / 60);
  if (min < 60) return `checked ${min}m ago`;
  const hr = Math.round(min / 60);
  return `checked ${hr}h ago`;
}
