// ConflictBanner — Phase 1's binary force-confirm bar. Renders inline
// above the action bar when an apply attempt returned 409 (SSA field-
// manager conflict). Two buttons: `apply with force` (retries with
// force=true) or `cancel` (returns to edit-dirty).
//
// Phase 2 replaces this with a per-field resolution view + takeover
// dialog (per the existing plan). For now we mirror the modal's
// existing UX, just inlined.

import { cn } from "../../../lib/cn";

interface ConflictBannerProps {
  message: string;
  onForce: () => void;
  onCancel: () => void;
  busy?: boolean;
}

export function ConflictBanner({ message, onForce, onCancel, busy }: ConflictBannerProps) {
  return (
    <div
      className={cn(
        "flex items-start gap-3 border-y border-yellow/50 bg-yellow-soft px-4 py-3 text-[12px]",
      )}
      role="alert"
    >
      <span aria-hidden className="mt-0.5 text-yellow">
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
          <path
            d="M7 1l6 11H1L7 1z"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinejoin="round"
          />
          <path
            d="M7 5v3"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinecap="round"
          />
          <circle cx="7" cy="10" r="0.7" fill="currentColor" />
        </svg>
      </span>
      <div className="min-w-0 flex-1">
        <div className="font-mono text-ink">
          <span className="font-medium">Field-manager conflict.</span>{" "}
          One or more fields are owned by another manager. Force apply will
          take ownership and may be reverted by the owning controller.
        </div>
        {message && (
          <div className="mt-1 font-mono text-[11px] text-ink-muted whitespace-pre-wrap break-words">
            {message}
          </div>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-1.5">
        <button
          type="button"
          onClick={onCancel}
          disabled={busy}
          className="rounded-sm border border-border-strong px-2.5 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink disabled:opacity-50"
        >
          cancel
        </button>
        <button
          type="button"
          onClick={onForce}
          disabled={busy}
          className="rounded-sm border border-yellow bg-yellow px-2.5 py-1 font-mono text-[11px] font-medium text-bg transition-colors hover:brightness-110 disabled:opacity-50"
        >
          {busy ? "applying…" : "apply with force"}
        </button>
      </div>
    </div>
  );
}
