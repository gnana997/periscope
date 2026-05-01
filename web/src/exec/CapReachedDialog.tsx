import { useEffect } from "react";
import { cn } from "../lib/cn";
import { useExecSessions, SESSION_CAP } from "./ExecSessionsContext";
import { clusterStripeColor } from "./clusterColor";

/**
 * Inline dialog shown when the user tries to open a 6th shell. Lists the
 * active sessions with X buttons so the user can free a slot in one click.
 */

interface CapReachedDialogProps {
  open: boolean;
  onClose: () => void;
}

export function CapReachedDialog({ open, onClose }: CapReachedDialogProps) {
  const { sessions, closeSession, focusSession } = useExecSessions();

  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  const live = sessions.filter(
    (s) => s.status === "connecting" || s.status === "connected",
  );

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-ink/40 px-4 backdrop-blur-[2px]"
      onClick={onClose}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        className="w-full max-w-md rounded-md border border-border-strong bg-surface p-4 font-sans shadow-[0_24px_48px_-16px_rgba(0,0,0,0.4)]"
        role="dialog"
        aria-modal="true"
      >
        <div className="mb-1 flex items-center gap-2">
          <span aria-hidden className="block size-1.5 rounded-full bg-yellow" />
          <h3 className="font-display text-[18px] leading-none text-ink">
            session limit reached
          </h3>
        </div>
        <p className="mb-3 text-[12px] leading-relaxed text-ink-muted">
          You have <span className="font-mono">{live.length}</span> active
          shells (cap is {SESSION_CAP}). Close one to open another.
        </p>
        <ul className="divide-y divide-border border border-border bg-bg/40">
          {live.map((s) => {
            const stripe = clusterStripeColor(s.cluster);
            return (
              <li
                key={s.id}
                className="flex items-center gap-2 px-2 py-1.5 font-mono text-[11.5px]"
              >
                <span
                  aria-hidden
                  className="h-3.5 w-[2px] shrink-0 rounded-full"
                  style={{ background: stripe }}
                />
                <span className="text-ink-faint">{s.cluster}</span>
                <span className="text-ink-faint">·</span>
                <span className="min-w-0 flex-1 truncate text-ink-muted">
                  {s.namespace}/<span className="text-ink">{s.pod}</span>
                </span>
                <button
                  type="button"
                  onClick={() => focusSession(s.id)}
                  className="rounded border border-border px-1.5 py-px text-[10.5px] text-ink-muted hover:border-border-strong hover:text-ink"
                >
                  focus
                </button>
                <button
                  type="button"
                  onClick={() => closeSession(s.id)}
                  className="rounded border border-border px-1.5 py-px text-[10.5px] text-ink-muted hover:border-red/60 hover:bg-red-soft hover:text-red"
                >
                  close
                </button>
              </li>
            );
          })}
        </ul>
        <div className="mt-3 flex justify-end">
          <button
            type="button"
            onClick={onClose}
            className={cn(
              "rounded border border-border px-2.5 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:border-border-strong hover:text-ink",
            )}
          >
            dismiss
          </button>
        </div>
      </div>
    </div>
  );
}
