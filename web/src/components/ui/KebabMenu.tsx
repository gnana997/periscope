// KebabMenu — small accessible dropdown anchored to a vertical-dots
// trigger. Used by row-level "more actions" affordances where a
// primary action button isn't appropriate (e.g. installed-addon
// rows that get Upgrade / Delete as secondary actions).
//
// Why not @radix-ui/react-popover: this is a row-level affordance
// repeated dozens of times per page; the radix Popover ships its
// own portal + trapped focus + animation runtime, which adds weight
// for a use case that needs neither. A plain click-outside / Esc /
// arrow-key handler is sufficient and matches the inline
// UserMenu.tsx pattern in this codebase. Keeps bundle weight off
// the catalog page hot path.
//
// Usage:
//   <KebabMenu
//     label="Actions"
//     items={[
//       { label: "Upgrade…", onSelect: () => setUpgrade(addon) },
//       { label: "Delete…", onSelect: () => setDelete(addon), variant: "danger" },
//     ]}
//   />

import { useEffect, useRef, useState } from "react";
import { cn } from "../../lib/cn";

export interface KebabMenuItem {
  label: string;
  onSelect: () => void;
  /** Visual emphasis. "danger" tints the item red; "default" is plain. */
  variant?: "default" | "danger";
  /** When true, the item renders disabled and onSelect is suppressed. */
  disabled?: boolean;
}

interface KebabMenuProps {
  /** Aria label for the trigger button (the dots). */
  label: string;
  items: KebabMenuItem[];
  /** Tailwind classes appended to the trigger. Use for sizing fits. */
  triggerClassName?: string;
}

export function KebabMenu({
  label,
  items,
  triggerClassName,
}: KebabMenuProps) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onDoc(e: MouseEvent) {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    }
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDoc);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div ref={ref} className="relative inline-block">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        onClick={(e) => {
          // Stop click bubbling so a row-click handler (e.g. expand
          // detail) doesn't fire when the operator opens the menu.
          e.stopPropagation();
          setOpen((v) => !v);
        }}
        className={cn(
          "rounded-sm border border-transparent px-1.5 py-0.5 font-mono text-[14px] leading-none text-ink-faint hover:border-border hover:bg-surface-2 hover:text-ink",
          open && "border-border bg-surface-2 text-ink",
          triggerClassName,
        )}
      >
        ⋮
      </button>
      {open && (
        <div
          role="menu"
          // Right-aligned because kebabs typically sit at row end.
          className="absolute right-0 top-full z-30 mt-1 min-w-[140px] rounded-md border border-border-strong bg-surface py-1 shadow-lg"
        >
          {items.map((item, i) => (
            <button
              key={i}
              type="button"
              role="menuitem"
              disabled={item.disabled}
              onClick={(e) => {
                e.stopPropagation();
                if (item.disabled) return;
                setOpen(false);
                item.onSelect();
              }}
              className={cn(
                "block w-full px-3 py-1.5 text-left font-mono text-[12px] transition-colors",
                item.variant === "danger"
                  ? "text-red hover:bg-red-soft"
                  : "text-ink hover:bg-surface-2",
                item.disabled && "cursor-not-allowed opacity-50 hover:bg-transparent",
              )}
            >
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
