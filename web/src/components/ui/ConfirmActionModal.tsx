// ConfirmActionModal — single-button confirmation dialog for ops
// actions (rollout restart, cordon node, suspend cronjob, trigger
// cronjob now). Shared so the four Phase-5 buttons render
// consistently rather than each re-inventing the layout.
//
// Variants:
//   - "warn" (default)   yellow accent — operationally safe but
//                        worth a beat (suspend, cordon).
//   - "danger"           red accent — destructive or disruptive
//                        (rollout restart cycles all pods).
//
// The DELETE flow keeps its own type-the-name modal because it
// needs the friction of literal-name confirmation; this primitive
// is for the single-click confirms.

import { type ReactNode } from "react";
import { Modal } from "./Modal";
import { cn } from "../../lib/cn";

interface ConfirmActionModalProps {
  open: boolean;
  title: string;
  /** Body content — usually one or two short paragraphs. */
  body: ReactNode;
  /** Label on the confirm button. Default "confirm". */
  confirmLabel?: string;
  variant?: "warn" | "danger";
  pending?: boolean;
  error?: string | null;
  onCancel: () => void;
  onConfirm: () => void;
}

const TITLE_ID = "confirm-action-title";

export function ConfirmActionModal({
  open,
  title,
  body,
  confirmLabel = "confirm",
  variant = "warn",
  pending = false,
  error = null,
  onCancel,
  onConfirm,
}: ConfirmActionModalProps) {
  return (
    <Modal
      open={open}
      onClose={pending ? () => {} : onCancel}
      labelledBy={TITLE_ID}
      size="sm"
      dismissOnEsc={!pending}
      dismissOnBackdrop={!pending}
    >
      <div className="px-5 py-4">
        <h2
          id={TITLE_ID}
          className="font-display text-[18px] leading-tight text-ink"
        >
          {title}
        </h2>
        <div className="mt-2 font-mono text-[12px] leading-relaxed text-ink-muted">
          {body}
        </div>
        {error && (
          <div className="mt-3 rounded-md border border-red/40 bg-red-soft px-3 py-2 font-mono text-[11.5px] text-red break-all">
            {error}
          </div>
        )}
      </div>
      <div className="flex items-center justify-end gap-2 border-t border-border bg-surface-2/30 px-5 py-3">
        <button
          type="button"
          onClick={onCancel}
          disabled={pending}
          className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-[11.5px] text-ink-muted transition-colors hover:border-ink-muted hover:text-ink disabled:cursor-not-allowed disabled:opacity-50"
        >
          cancel
        </button>
        <button
          type="button"
          onClick={onConfirm}
          disabled={pending}
          className={cn(
            "rounded-sm border px-3 py-1.5 font-mono text-[11.5px] font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50",
            variant === "danger"
              ? "border-red bg-red text-bg hover:brightness-110"
              : "border-yellow-700 bg-yellow text-bg hover:brightness-110",
          )}
        >
          {pending ? "working…" : confirmLabel}
        </button>
      </div>
    </Modal>
  );
}
