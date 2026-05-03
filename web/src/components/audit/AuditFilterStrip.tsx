import { useState } from "react";
import { cn } from "../../lib/cn";
import type { AuditOutcome, AuditQueryParams } from "../../lib/types";

const OUTCOMES: Array<{ value: AuditOutcome | "all"; label: string }> = [
  { value: "all", label: "all" },
  { value: "success", label: "success" },
  { value: "failure", label: "failure" },
  { value: "denied", label: "denied" },
];

const VERBS = [
  "apply",
  "delete",
  "trigger",
  "exec_open",
  "exec_close",
  "secret_reveal",
  "log_open",
] as const;

const TIME_PRESETS: Array<{ label: string; minutes: number }> = [
  { label: "15m", minutes: 15 },
  { label: "1h", minutes: 60 },
  { label: "6h", minutes: 60 * 6 },
  { label: "24h", minutes: 60 * 24 },
  { label: "7d", minutes: 60 * 24 * 7 },
];

interface AuditFilterStripProps {
  filters: AuditQueryParams;
  onChange: (next: AuditQueryParams) => void;
  /** When true, render the actor search input. Hidden for scope=self. */
  showActorFilter: boolean;
}

/**
 * AuditFilterStrip — the audit page's primary filter UI.
 *
 * Layout (always visible):
 *   row 1:  outcome pills · verb pills (wraps) · time-range picker
 *   row 2:  actor search (scope=all only) · [more filters ▾]
 *
 * "More filters" disclosure (collapsed by default):
 *   namespace · resource name · request id
 *
 * Filter changes flow upward via onChange so the parent owns URL
 * state — every filter serializes to query params for permalink-able
 * audit views.
 */
export function AuditFilterStrip({
  filters,
  onChange,
  showActorFilter,
}: AuditFilterStripProps) {
  const [moreOpen, setMoreOpen] = useState(
    Boolean(filters.namespace || filters.name || filters.requestId),
  );

  const setField = (key: keyof AuditQueryParams, value: string | undefined) => {
    onChange({
      ...filters,
      [key]: value && value !== "" ? value : undefined,
      // Reset offset on any filter change so users don't paginate into
      // an empty page after narrowing the result set.
      offset: undefined,
    });
  };

  const setOutcome = (v: AuditOutcome | "all") => {
    setField("outcome", v === "all" ? undefined : v);
  };

  const setVerb = (v: string) => {
    setField("verb", filters.verb === v ? undefined : v);
  };

  const setRange = (presetMinutes: number, presetLabel: string) => {
    const to = new Date();
    const from = new Date(to.getTime() - presetMinutes * 60 * 1000);
    onChange({
      ...filters,
      from: from.toISOString(),
      to: to.toISOString(),
      offset: undefined,
    });
    setActivePreset(presetLabel);
  };

  // Track the most recently clicked preset directly. Don't try to
  // derive it from filters.from (which would need a Date.now() call
  // in render — banned by react-hooks/purity). When the user navigates
  // via URL or back/forward, no preset is highlighted; that's the
  // honest state ("custom range from URL").
  const [activePreset, setActivePreset] = useState<string | undefined>(
    undefined,
  );

  return (
    <div className="sticky top-[72px] z-10 flex flex-col gap-2 border-b border-border bg-bg/80 px-6 py-3 backdrop-blur-md">
      {/* row 1: outcome + verb + time */}
      <div className="flex flex-wrap items-center gap-2">
        <PillGroup label="outcome">
          {OUTCOMES.map((o) => (
            <Pill
              key={o.value}
              active={
                (o.value === "all" && !filters.outcome) ||
                o.value === filters.outcome
              }
              onClick={() => setOutcome(o.value)}
            >
              {o.label}
            </Pill>
          ))}
        </PillGroup>

        <PillGroup label="verb">
          <Pill active={!filters.verb} onClick={() => setField("verb", undefined)}>
            all
          </Pill>
          {VERBS.map((v) => (
            <Pill
              key={v}
              active={filters.verb === v}
              onClick={() => setVerb(v)}
            >
              {v}
            </Pill>
          ))}
        </PillGroup>

        <div className="ml-auto flex items-center gap-1.5 rounded-md border border-border bg-surface p-1">
          <span className="px-1.5 font-mono text-[10.5px] uppercase tracking-wide text-ink-faint">
            range
          </span>
          {TIME_PRESETS.map((p) => (
            <Pill
              key={p.label}
              active={activePreset === p.label}
              onClick={() => setRange(p.minutes, p.label)}
            >
              {p.label}
            </Pill>
          ))}
        </div>
      </div>

      {/* row 2: actor + more-filters disclosure */}
      <div className="flex flex-wrap items-center gap-2">
        {showActorFilter && (
          <input
            type="text"
            placeholder="filter by actor (sub or email)…"
            value={filters.actor ?? ""}
            onChange={(e) => setField("actor", e.target.value)}
            className="min-w-[260px] flex-1 rounded-md border border-border bg-surface px-3 py-1.5 text-[12.5px] text-ink placeholder:text-ink-faint focus:border-border-strong focus:outline-none"
          />
        )}

        <button
          type="button"
          onClick={() => setMoreOpen((v) => !v)}
          className={cn(
            "ml-auto rounded-md border border-border bg-surface px-2.5 py-1.5 font-mono text-[11px] text-ink-muted hover:border-border-strong hover:text-ink",
            moreOpen && "border-border-strong text-ink",
          )}
          aria-expanded={moreOpen}
        >
          more filters {moreOpen ? "▴" : "▾"}
        </button>
      </div>

      {moreOpen && (
        <div className="flex flex-wrap items-center gap-2 border-t border-border pt-2">
          <SmallInput
            label="namespace"
            value={filters.namespace ?? ""}
            onChange={(v) => setField("namespace", v)}
          />
          <SmallInput
            label="resource name"
            value={filters.name ?? ""}
            onChange={(v) => setField("name", v)}
          />
          <SmallInput
            label="request id"
            value={filters.requestId ?? ""}
            onChange={(v) => setField("requestId", v)}
          />
        </div>
      )}
    </div>
  );
}

function PillGroup({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center gap-1.5 rounded-md border border-border bg-surface p-1">
      <span className="px-1.5 font-mono text-[10.5px] uppercase tracking-wide text-ink-faint">
        {label}
      </span>
      {children}
    </div>
  );
}

function Pill({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "rounded px-2 py-0.5 font-mono text-[11.5px] transition-colors",
        active
          ? "bg-accent-soft text-accent"
          : "text-ink-muted hover:text-ink",
      )}
      aria-pressed={active}
    >
      {children}
    </button>
  );
}

function SmallInput({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <label className="flex items-center gap-2 rounded-md border border-border bg-surface px-2 py-1 text-[11.5px]">
      <span className="font-mono text-[10.5px] uppercase tracking-wide text-ink-faint">
        {label}
      </span>
      <input
        type="text"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="min-w-[120px] bg-transparent text-ink placeholder:text-ink-faint focus:outline-none"
      />
    </label>
  );
}
