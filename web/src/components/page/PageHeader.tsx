import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { ThemeToggle } from "../shell/ThemeToggle";

interface ActionChip {
  label: string;
  count: number;
  tone: "red" | "yellow";
  active: boolean;
  onClick: () => void;
}

interface PageHeaderProps {
  title: string;
  subtitle?: string;
  /** Right-side actionable chips (e.g. failing/pending counts). */
  chips?: ActionChip[];
  /** Right-side trailing slot (e.g. namespace picker). Renders after chips. */
  trailing?: ReactNode;
}

export function PageHeader({
  title,
  subtitle,
  chips,
  trailing,
}: PageHeaderProps) {
  return (
    <div className="flex flex-wrap items-end gap-x-5 gap-y-2 border-b border-border bg-bg px-6 pb-4 pt-6">
      <h1
        className="font-display text-[40px] leading-[0.95] tracking-[-0.02em] text-ink"
        style={{ fontWeight: 400 }}
      >
        {title}
      </h1>
      {subtitle && (
        <div className="pb-1.5 text-[12.5px] text-ink-muted">{subtitle}</div>
      )}
      <div className="ml-auto flex flex-wrap items-center gap-2 pb-1">
        {chips?.map((chip) => <Chip key={chip.label} {...chip} />)}
        {trailing}
        <ThemeToggle />
      </div>
    </div>
  );
}

function Chip({ label, count, tone, active, onClick }: ActionChip) {
  if (count === 0) return null;
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1 font-mono text-[11.5px] font-medium transition-all",
        tone === "red"
          ? active
            ? "border-red bg-red text-white"
            : "border-red/50 bg-red-soft text-red hover:border-red"
          : active
            ? "border-yellow bg-yellow text-white"
            : "border-yellow/50 bg-yellow-soft text-yellow hover:border-yellow",
      )}
      aria-pressed={active}
    >
      <span className="block size-1.5 rounded-full bg-current" />
      <span className="tabular">{count}</span>
      <span>{label}</span>
    </button>
  );
}
