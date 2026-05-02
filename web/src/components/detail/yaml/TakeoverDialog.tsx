// TakeoverDialog — confirmation dialog shown before applying with
// force=true. Required when the user has chosen "keep mine" on at
// least one conflicting field, because seizing ownership from another
// manager is a deliberate, hard-to-reverse action (Flux/HPA/etc. may
// fight back).
//
// Design from the v2 mock: list the fields being seized + their
// current owners, summarize the operational consequence per category,
// and gate the confirm button behind typing the literal word "force"
// — the same pattern GitHub uses for repo deletion. Removes the
// "I'll just click through" failure mode.
//
// If all keep-mine fields are owned by HUMAN-category managers (old
// kubectl-edit, etc.), we skip the typing gate — that's a benign
// case and the friction isn't earning anything.

import { useEffect, useRef, useState } from "react";
import { cn } from "../../../lib/cn";
import { classifyManager } from "../../../lib/managers";

interface TakeoverDialogProps {
  fields: Array<{ path: string; manager: string }>;
  onCancel(): void;
  onConfirm(): void;
}

export function TakeoverDialog({ fields, onCancel, onConfirm }: TakeoverDialogProps) {
  const [typed, setTyped] = useState("");
  const inputRef = useRef<HTMLInputElement | null>(null);

  // Auto-focus the input on open. setTimeout dodges the React 19
  // double-mount quirk where the ref isn't set yet on first commit.
  useEffect(() => {
    const t = setTimeout(() => inputRef.current?.focus(), 50);
    return () => clearTimeout(t);
  }, []);

  // Esc to cancel. Doesn't catch when typing — typing flows through.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        onCancel();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  // Decide whether to require the typing gate. Skip if every field
  // being seized is owned by a HUMAN-category manager (kubectl-edit,
  // client-side-apply, etc.) — those are routine.
  const requireGate = fields.some((f) => classifyManager(f.manager).category !== "HUMAN");

  // Compose the consequence summary. Dedupe per category since
  // listing the same warning N times reads as noise.
  const seenConsequences = new Set<string>();
  const consequenceLines: string[] = [];
  for (const f of fields) {
    const c = classifyManager(f.manager).consequence;
    if (!seenConsequences.has(c)) {
      seenConsequences.add(c);
      consequenceLines.push(c);
    }
  }

  const ok = !requireGate || typed.trim().toLowerCase() === "force";

  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/40 px-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="takeover-title"
      onClick={(e) => {
        if (e.target === e.currentTarget) onCancel();
      }}
    >
      <div className="w-full max-w-[560px] rounded-md border border-border-strong bg-surface px-6 py-5 shadow-2xl">
        <div className="font-mono text-[10px] uppercase tracking-[0.10em] text-red">
          take ownership · deliberate action
        </div>
        <h2
          id="takeover-title"
          className="mt-1.5 font-display text-[24px] leading-tight text-ink"
        >
          Take ownership of {fields.length} field{fields.length === 1 ? "" : "s"}?
        </h2>
        <p className="mt-3 text-[12.5px] leading-relaxed text-ink">
          You are about to seize ownership of fields currently managed by another
          controller. Periscope will become the field manager for these paths
          until they are explicitly handed back.
        </p>

        {/* Fields list */}
        <div className="mt-3 rounded-sm border border-border bg-bg px-3 py-2">
          {fields.map((f) => {
            const mgr = classifyManager(f.manager);
            return (
              <div
                key={f.path}
                className="flex items-baseline justify-between gap-3 border-b border-border py-1 last:border-b-0"
              >
                <span className="break-all font-mono text-[11.5px] text-ink">
                  {f.path}
                </span>
                <span className="shrink-0 font-mono text-[10.5px] text-ink-muted">
                  from <span className="text-violet">{mgr.display}</span>
                </span>
              </div>
            );
          })}
        </div>

        {/* Consequence block */}
        <div className="mt-3 rounded-sm border-l-[3px] border-red bg-red-soft px-3 py-2">
          <div className="font-mono text-[10.5px] font-medium text-red">
            Likely outcome
          </div>
          {consequenceLines.map((line, i) => (
            <p
              key={i}
              className="mt-1 text-[12px] leading-relaxed text-ink first:mt-0"
            >
              {line}
            </p>
          ))}
        </div>

        {/* Typing gate */}
        {requireGate && (
          <div className="mt-4">
            <label className="mb-1.5 block font-mono text-[11px] text-ink-muted">
              Type{" "}
              <code className="rounded-sm bg-accent-soft px-1.5 py-0.5 font-mono text-accent">
                force
              </code>{" "}
              to confirm:
            </label>
            <input
              ref={inputRef}
              type="text"
              autoComplete="off"
              spellCheck={false}
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              placeholder="force"
              className={cn(
                "w-full rounded-sm border bg-bg px-3 py-2 font-mono text-[13px] text-ink outline-none transition-colors",
                ok
                  ? "border-green focus:shadow-[0_0_0_3px_var(--color-green-soft)]"
                  : "border-border-strong focus:border-accent focus:shadow-[0_0_0_3px_var(--color-accent-soft)]",
              )}
            />
          </div>
        )}

        {/* Actions */}
        <div className="mt-5 flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-[11.5px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink"
          >
            cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={!ok}
            className={cn(
              "rounded-sm border px-3 py-1.5 font-mono text-[11.5px] font-medium transition-colors",
              ok
                ? "border-red bg-red text-bg hover:brightness-110"
                : "cursor-not-allowed border-border-strong text-ink-faint opacity-60",
            )}
          >
            take ownership ⚠
          </button>
        </div>
      </div>
    </div>
  );
}
