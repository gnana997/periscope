// Modal — shared primitive for dialog overlays.
//
// Wraps the fixed-inset overlay + backdrop + Esc/click-outside-to-close
// + role="dialog"/aria-modal that DeleteResourceModal, TakeoverDialog,
// DriftDiffOverlay, ScaleModal, and EditLabelsModal each used to
// hand-roll. Keeps all five visually consistent and avoids drift when
// adding a sixth.
//
// Behaviour:
//   - Esc dismisses (unless the consumer disables via `dismissOnEsc=false`,
//     useful when a destructive action is in flight and you want to
//     prevent accidental cancel).
//   - Click on the backdrop dismisses (same disable knob via
//     `dismissOnBackdrop=false`).
//   - On unmount, focus returns to whatever element opened the modal.
//   - `size` controls the max-width: sm=420px, md=560px, lg=1100px.
//   - `z` controls the stacking layer (default 50; takeovers may want 60).

import { useEffect, useRef, type ReactNode } from "react";
import { cn } from "../../lib/cn";

interface ModalProps {
  open: boolean;
  onClose: () => void;
  /** Element id of the title node — wired to aria-labelledby. */
  labelledBy: string;
  size?: "sm" | "md" | "lg";
  z?: 50 | 60;
  dismissOnEsc?: boolean;
  dismissOnBackdrop?: boolean;
  /** Tailwind classes appended to the panel. Use for unusual fits. */
  panelClassName?: string;
  children: ReactNode;
}

const SIZE_CLASS: Record<NonNullable<ModalProps["size"]>, string> = {
  sm: "max-w-md",
  md: "max-w-[560px]",
  lg: "max-w-[1100px]",
};

export function Modal({
  open,
  onClose,
  labelledBy,
  size = "md",
  z = 50,
  dismissOnEsc = true,
  dismissOnBackdrop = true,
  panelClassName,
  children,
}: ModalProps) {
  const returnFocusRef = useRef<HTMLElement | null>(null);

  // Capture the element that had focus when the modal opened so we can
  // restore it on close. Without this, after Esc the focus drops to
  // <body> and keyboard users lose their place in the page.
  useEffect(() => {
    if (!open) return;
    returnFocusRef.current = (document.activeElement as HTMLElement | null) ?? null;
    return () => {
      returnFocusRef.current?.focus?.();
      returnFocusRef.current = null;
    };
  }, [open]);

  useEffect(() => {
    if (!open || !dismissOnEsc) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose, dismissOnEsc]);

  if (!open) return null;

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-labelledby={labelledBy}
      className={cn(
        "fixed inset-0 flex items-center justify-center bg-black/40 px-4",
        z === 60 ? "z-[60]" : "z-50",
      )}
      onClick={(e) => {
        if (dismissOnBackdrop && e.target === e.currentTarget) onClose();
      }}
    >
      <div
        className={cn(
          "w-full rounded-md border border-border-strong bg-surface shadow-2xl",
          SIZE_CLASS[size],
          panelClassName,
        )}
      >
        {children}
      </div>
    </div>
  );
}
