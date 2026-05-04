// IconAction — the icon-button primitive for resource detail action
// rows. Replaces the text-labeled <Tooltip><button>...</button>
// </Tooltip> pattern that ResourceActions used pre-2026-05-04.
//
// Why this exists:
//   - Action rows in DetailPane were getting clipped at narrow pane
//     widths (close X became unreachable). Two-row header solved
//     reachability; this primitive solves visual density so the row
//     fits the typical 6+ buttons without horizontal scroll.
//   - Tooltip + button + sized icon + a11y wiring (aria-label,
//     aria-disabled, aria-pressed) is the same boilerplate at every
//     callsite. Centralising it here means one place to change the
//     button geometry across the whole app.
//
// Tones:
//   default   — neutral text-ink-muted, hover lifts to text-ink
//   danger    — red tint for destructive actions (delete, drain, etc.)
//   active    — yellow tint when the resource is in the toggled state
//                (cordon-when-unschedulable, suspend-when-suspended)
//
// Disabled state mirrors the pre-existing pattern: the button is HTML
// disabled (no clicks, no focus) and the tooltip body switches to the
// disabledTooltip when present.

import type { ReactNode } from "react";
import { cn } from "../lib/cn";
import { Tooltip } from "./Tooltip";

export type IconActionTone = "default" | "danger" | "active";

interface IconActionProps {
  /** Tooltip body shown on hover and the button's accessible name. */
  label: string;
  icon: ReactNode;
  onClick: () => void;
  disabled?: boolean;
  /**
   * Optional override tooltip when disabled. Useful for surfacing the
   * RBAC reason ("your tier (`viewer`) cannot delete pods") instead
   * of a vanilla "Delete" hint when the action isn't reachable.
   */
  disabledTooltip?: string | null;
  tone?: IconActionTone;
  /**
   * Pressed/toggle state. Set true on the active half of a toggle pair
   * (e.g. resource is currently cordoned, currently suspended) so the
   * button gets the "active" visual indicator regardless of tone.
   */
  pressed?: boolean;
}

const TONE_BASE: Record<IconActionTone, string> = {
  default:
    "text-ink-muted hover:bg-surface-2 hover:text-ink",
  danger:
    "text-red hover:bg-red-soft",
  active:
    "border border-yellow/40 bg-yellow/10 text-yellow hover:brightness-110",
};

const PRESSED_RING = "ring-1 ring-inset ring-accent/40";

export function IconAction({
  label,
  icon,
  onClick,
  disabled = false,
  disabledTooltip,
  tone = "default",
  pressed = false,
}: IconActionProps) {
  const tooltipContent = disabled ? (disabledTooltip ?? label) : label;

  const button = (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      aria-disabled={disabled}
      aria-pressed={pressed || undefined}
      aria-label={label}
      className={cn(
        "inline-flex size-8 shrink-0 items-center justify-center rounded-md transition-colors",
        TONE_BASE[tone],
        pressed && PRESSED_RING,
        "disabled:cursor-not-allowed disabled:opacity-50 disabled:hover:bg-transparent",
      )}
    >
      {icon}
    </button>
  );

  // Disabled <button> swallows pointer events on some browsers, which
  // means the Tooltip never opens. Wrap in a span so the tooltip's
  // hover detection works regardless.
  if (disabled) {
    return (
      <Tooltip content={tooltipContent}>
        <span className="inline-flex">{button}</span>
      </Tooltip>
    );
  }

  return <Tooltip content={tooltipContent}>{button}</Tooltip>;
}
