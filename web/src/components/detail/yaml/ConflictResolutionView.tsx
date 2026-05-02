// ConflictResolutionView — replaces the old binary "apply with force"
// banner when the apiserver returns 409. Shows a per-field card for
// each conflicting path: the manager that owns it, what category that
// manager belongs to (GitOps / Controller / Helm / Human), the user's
// proposed value vs the live owned value, and the operational
// consequence of force-applying ("Flux will revert this in ~5 min").
//
// User picks per field: revert mine (drop the field from the patch)
// or keep mine (seize ownership with force=true). Apply gates on all
// conflicts being resolved.
//
// This is the safety-critical surface of Phase 2 — before this PR an
// operator clicking "force apply" had no idea whether they were
// taking a single field from old kubectl-edit or starting a 5-minute
// reconcile-war with Flux. Now they see exactly which manager,
// what'll happen, and decide per-field.

import { useMemo } from "react";
import { cn } from "../../../lib/cn";
import {
  classifyManager,
  managerColorClass,
} from "../../../lib/managers";

export interface FieldConflict {
  path: string;
  manager: string;
  // Values from the editor + apiserver. We have the user's proposed
  // value (from their YAML buffer) but not always the apiserver's
  // current value — Status response only includes the conflicting
  // path, not the value. We surface mine; theirs is informational
  // only when we have it (filled at parse time when possible).
  mine?: string;
  theirs?: string;
}

export type Resolution = "keep" | "revert";

interface ConflictResolutionViewProps {
  conflicts: FieldConflict[];
  resolutions: Map<string, Resolution>;
  onResolve(path: string, choice: Resolution | null): void;
  onJumpTo(path: string): void;
  onBackToEdit(): void;
  onApply(): void;
  busy: boolean;
}

export function ConflictResolutionView({
  conflicts,
  resolutions,
  onResolve,
  onJumpTo,
  onBackToEdit,
  onApply,
  busy,
}: ConflictResolutionViewProps) {
  const counts = useMemo(() => {
    let keep = 0;
    let revert = 0;
    for (const r of resolutions.values()) {
      if (r === "keep") keep++;
      else if (r === "revert") revert++;
    }
    const unresolved = conflicts.length - keep - revert;
    return { keep, revert, unresolved };
  }, [conflicts.length, resolutions]);

  const allResolved = counts.unresolved === 0;
  const anyTakeover = counts.keep > 0;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-auto bg-bg">
      {/* Header */}
      <header className="shrink-0 border-b border-border bg-surface px-5 py-4 border-l-[3px] border-l-red">
        <div className="font-display text-[22px] leading-tight text-ink">
          Apply blocked · <span className="text-red">{conflicts.length}</span>{" "}
          field{conflicts.length === 1 ? "" : "s"} owned by other managers
        </div>
        <div className="mt-1.5 max-w-[720px] text-[12.5px] leading-relaxed text-ink-muted">
          Server-side apply requires every field you set to be either unowned,
          owned by you, or seized with <code className="font-mono">force</code>.
          Choose per field — revert to use the live value, or keep yours and
          take ownership.
        </div>
      </header>

      {/* Per-field cards */}
      <div className="flex flex-col gap-3 px-5 py-4">
        {conflicts.map((c) => (
          <FieldCard
            key={c.path}
            conflict={c}
            resolution={resolutions.get(c.path) ?? null}
            onResolve={(r) => onResolve(c.path, r)}
            onJumpTo={() => onJumpTo(c.path)}
          />
        ))}
      </div>

      {/* Summary footer */}
      <footer className="sticky bottom-0 mt-auto flex shrink-0 items-center gap-4 border-t border-border bg-surface px-5 py-3 font-mono text-[11.5px]">
        <SummarySeg label="keep mine" value={counts.keep} tone={counts.keep > 0 ? "accent" : "faint"} />
        <SummarySeg label="revert mine" value={counts.revert} tone={counts.revert > 0 ? "green" : "faint"} />
        <SummarySeg
          label="unresolved"
          value={counts.unresolved}
          tone={counts.unresolved > 0 ? "red" : "green"}
        />
        <div className="ml-auto flex items-center gap-1.5">
          <button
            type="button"
            onClick={onBackToEdit}
            disabled={busy}
            className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink disabled:opacity-50"
          >
            back to editor
          </button>
          <button
            type="button"
            onClick={onApply}
            disabled={!allResolved || busy}
            className={cn(
              "rounded-sm border px-3 py-1 font-mono text-[11px] font-medium transition-colors",
              allResolved && !busy
                ? anyTakeover
                  ? "border-red bg-red text-bg hover:brightness-110"
                  : "border-accent bg-accent text-bg hover:brightness-110"
                : "cursor-not-allowed border-border-strong text-ink-faint opacity-60",
            )}
          >
            {busy
              ? "applying…"
              : !allResolved
                ? `resolve ${counts.unresolved} more`
                : anyTakeover
                  ? `apply with takeover ⚠`
                  : `apply (revert-only)`}
          </button>
        </div>
      </footer>
    </div>
  );
}

function FieldCard({
  conflict,
  resolution,
  onResolve,
  onJumpTo,
}: {
  conflict: FieldConflict;
  resolution: Resolution | null;
  onResolve: (r: Resolution | null) => void;
  onJumpTo: () => void;
}) {
  const mgr = classifyManager(conflict.manager);
  const palette = managerColorClass(mgr.category);

  return (
    <article
      className={cn(
        "rounded-md border bg-surface p-4 transition-colors",
        resolution === "keep" && "border-red/60 bg-red-soft/40",
        resolution === "revert" && "border-green/40 opacity-70",
        resolution === null && "border-border",
      )}
    >
      {/* Top: path + jump link */}
      <div className="flex items-start gap-3">
        <PathLabel path={conflict.path} />
        <button
          type="button"
          onClick={onJumpTo}
          className="ml-auto shrink-0 rounded-sm px-2 py-0.5 font-mono text-[10px] text-ink-faint transition-colors hover:bg-accent-soft hover:text-accent"
          title="Jump to this line in editor"
        >
          ▸ jump to line
        </button>
      </div>

      {/* Owner row */}
      <div className="mt-2 flex items-center gap-2">
        <span className="font-mono text-[10px] uppercase tracking-[0.06em] text-ink-faint">
          owned by
        </span>
        <span className="font-mono text-[12px] text-ink">{mgr.display}</span>
        <span
          className={cn(
            "rounded-sm border px-1.5 py-0.5 font-mono text-[9.5px] uppercase tracking-[0.07em]",
            palette.text,
            palette.bg,
            palette.border,
          )}
        >
          {mgr.category} · {mgr.source}
        </span>
      </div>

      {/* Consequence */}
      <p className="mt-2 max-w-[640px] text-[11.5px] leading-relaxed text-ink-muted">
        <span className="text-red">Likely outcome:</span> {mgr.consequence}
        {mgr.prefer && <em className="ml-1 not-italic text-ink-faint">{mgr.prefer}</em>}
      </p>

      {/* Values (only render if we know them) */}
      {(conflict.mine !== undefined || conflict.theirs !== undefined) && (
        <div className="mt-3 grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 rounded-sm border border-border bg-bg px-3 py-2 font-mono text-[12px]">
          {conflict.mine !== undefined && (
            <>
              <div className="self-start text-[10px] uppercase tracking-[0.05em] text-ink-faint">
                your value
              </div>
              <div className="break-all text-accent">{String(conflict.mine)}</div>
            </>
          )}
          {conflict.theirs !== undefined && (
            <>
              <div className="self-start text-[10px] uppercase tracking-[0.05em] text-ink-faint">
                their value
              </div>
              <div className="break-all text-ink-muted">{String(conflict.theirs)}</div>
            </>
          )}
        </div>
      )}

      {/* Action row */}
      <div className="mt-3 flex flex-wrap items-center gap-2">
        {resolution === null ? (
          <>
            <button
              type="button"
              onClick={() => onResolve("revert")}
              className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-green hover:text-green"
              title="Drop this field from the patch — apiserver keeps the manager's value"
            >
              revert mine
            </button>
            <button
              type="button"
              onClick={() => onResolve("keep")}
              className="rounded-sm border border-red/40 bg-red-soft px-2.5 py-1 font-mono text-[11px] text-red transition-colors hover:bg-red/20"
              title="Keep your value and take ownership of this field"
            >
              keep mine ⚠
            </button>
          </>
        ) : (
          <>
            <span
              className={cn(
                "font-mono text-[10.5px] uppercase tracking-[0.05em]",
                resolution === "keep" ? "text-red" : "text-green",
              )}
            >
              {resolution === "keep" ? "★ will take ownership" : "↺ will revert to live value"}
            </span>
            <button
              type="button"
              onClick={() => onResolve(null)}
              className="ml-auto rounded-sm border border-border px-2 py-1 font-mono text-[10.5px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
            >
              undo
            </button>
          </>
        )}
      </div>
    </article>
  );
}

/** Render a dotted path with merge keys highlighted distinctly. */
function PathLabel({ path }: { path: string }) {
  // Split into segments preserving brackets
  const segments: { text: string; isLeaf: boolean; isArr: boolean }[] = [];
  const rePart = /([^.[\]]+)|\[([^\]]+)\]/g;
  const matches = [...path.matchAll(rePart)];
  matches.forEach((m, i) => {
    const isLeaf = i === matches.length - 1;
    const isArr = Boolean(m[2]);
    segments.push({ text: isArr ? m[2] : m[1], isLeaf, isArr });
  });

  return (
    <div className="min-w-0 flex-1 break-all font-mono text-[13px]">
      {segments.map((s, i) => (
        <span key={i}>
          {i > 0 && !s.isArr && <span className="text-ink-faint">.</span>}
          {s.isArr ? (
            <span className="text-ink-faint">[</span>
          ) : null}
          <span
            className={cn(
              s.isArr
                ? "text-violet italic"
                : s.isLeaf
                  ? "text-accent"
                  : "text-ink-muted",
            )}
          >
            {s.text}
          </span>
          {s.isArr ? <span className="text-ink-faint">]</span> : null}
        </span>
      ))}
    </div>
  );
}

function SummarySeg({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone: "accent" | "green" | "red" | "faint";
}) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="text-[10px] uppercase tracking-[0.05em] text-ink-faint">{label}</span>
      <span
        className={cn(
          "tabular text-ink",
          tone === "accent" && "text-accent",
          tone === "green" && "text-green",
          tone === "red" && "text-red",
          tone === "faint" && "text-ink-faint",
        )}
      >
        {value}
      </span>
    </span>
  );
}
