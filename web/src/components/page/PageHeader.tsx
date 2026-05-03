import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { ThemeToggle } from "../shell/ThemeToggle";
import { StreamHealthBadge } from "./StreamHealthBadge";
import type { StreamStatus } from "../../hooks/useResourceStream";

interface ActionChip {
  label: string;
  count: number;
  tone: "red" | "yellow" | "green";
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
  /**
   * Live-update badge state. Pass useResource's streamStatus on list
   * pages whose kind has a watch SSE feed; undefined for everything
   * else (the badge renders nothing).
   */
  streamStatus?: StreamStatus;
}

export function PageHeader({
  title,
  subtitle,
  chips,
  trailing,
  streamStatus,
}: PageHeaderProps) {
  return (
    // Sticky glass-effect chrome: sits at the top of the page's scroll
    // container, stays visible as content scrolls beneath. Translucent
    // base + backdrop blur so the page underneath shows through subtly
    // without hurting legibility. z-20 keeps it above table rows but
    // below modal/drawer overlays (z-40+).
    <div className="sticky top-0 z-20 flex flex-wrap items-end gap-x-5 gap-y-2 border-b border-border bg-bg/80 px-6 pb-4 pt-6 backdrop-blur-md">
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
        <StreamHealthBadge status={streamStatus} />
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
          : tone === "green"
            ? active
              ? "border-green bg-green text-white"
              : "border-green/50 bg-green-soft text-green hover:border-green"
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
