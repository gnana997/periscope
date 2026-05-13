// Colour-coded chip for the sensitive-perms catalog. Reused by the
// AWS Access tab (per-permission flag) and the reverse-lookup form
// (chip palette + autocomplete + result row).
//
// Category → colour mapping mirrors what marketing screenshots
// committed to; keep them stable across releases.

import { cn } from "../../lib/cn";
import type { SensitiveCategory } from "../../lib/identity";

export interface SensitivePermChipProps {
  category: SensitiveCategory;
  label?: string;
  onClick?: () => void;
  className?: string;
}

const categoryStyles: Record<SensitiveCategory, string> = {
  "privilege-escalation": "border-red/30 bg-red/10 text-red",
  data: "border-amber-500/30 bg-amber-500/10 text-amber-500",
  "cross-account": "border-purple-500/30 bg-purple-500/10 text-purple-500",
  destructive: "border-red/30 bg-red/10 text-red",
  cluster: "border-blue-500/30 bg-blue-500/10 text-blue-500",
  wildcard: "border-red/30 bg-red/10 text-red",
};

const categoryShortLabel: Record<SensitiveCategory, string> = {
  "privilege-escalation": "priv-esc",
  data: "data",
  "cross-account": "cross-acct",
  destructive: "destructive",
  cluster: "cluster",
  wildcard: "wildcard",
};

export function SensitivePermChip({
  category,
  label,
  onClick,
  className,
}: SensitivePermChipProps) {
  const display = label ?? categoryShortLabel[category];
  const styles = categoryStyles[category];
  const interactive = !!onClick;
  return (
    <span
      role={interactive ? "button" : undefined}
      tabIndex={interactive ? 0 : undefined}
      onClick={onClick}
      onKeyDown={
        interactive
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick?.();
              }
            }
          : undefined
      }
      className={cn(
        "inline-flex items-center gap-1 rounded-sm border px-1.5 py-0.5 font-mono text-[11px]",
        styles,
        interactive && "cursor-pointer hover:opacity-80",
        className,
      )}
    >
      {display}
    </span>
  );
}
