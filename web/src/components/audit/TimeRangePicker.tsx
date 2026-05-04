import { useMemo } from "react";
import { cn } from "../../lib/cn";
import type { AuditQueryParams } from "../../lib/types";

// TIME_PRESETS — the same list previously embedded in
// AuditFilterStrip. Lifted here so the picker owns its own option
// table and can be embedded anywhere (today: PageHeader trailing
// slot on AuditPage; future: any page that needs time-range
// scoping).
const TIME_PRESETS: Array<{ label: string; minutes: number }> = [
  { label: "15m", minutes: 15 },
  { label: "1h", minutes: 60 },
  { label: "6h", minutes: 60 * 6 },
  { label: "24h", minutes: 60 * 24 },
  { label: "7d", minutes: 60 * 24 * 7 },
];

interface TimeRangePickerProps {
  filters: AuditQueryParams;
  onChange: (next: AuditQueryParams) => void;
}

/**
 * TimeRangePicker — five-preset range picker (15m / 1h / 6h / 24h
 * / 7d). Renders inline; designed to live in PageHeader's `trailing`
 * slot so the active range is always visible regardless of how the
 * filter strip wraps below it.
 *
 * Active preset is derived from filters.from/to so the highlight is
 * correct on page load (when the parent seeds the default range
 * via setSearchParams) AND when the user clicks. No Date.now() in
 * render — `to` is a captured timestamp, not a sliding window, so
 * the diff is stable across re-renders.
 */
export function TimeRangePicker({ filters, onChange }: TimeRangePickerProps) {
  const activePreset = useMemo(() => {
    if (!filters.from || !filters.to) return undefined;
    const diffMin = Math.round(
      (new Date(filters.to).getTime() - new Date(filters.from).getTime()) /
        60_000,
    );
    return TIME_PRESETS.find((p) => p.minutes === diffMin)?.label;
  }, [filters.from, filters.to]);

  const setRange = (presetMinutes: number) => {
    const to = new Date();
    const from = new Date(to.getTime() - presetMinutes * 60 * 1000);
    onChange({
      ...filters,
      from: from.toISOString(),
      to: to.toISOString(),
      offset: undefined,
    });
  };

  return (
    <div className="flex items-center gap-1.5 rounded-md border border-border bg-surface p-1">
      <span className="px-1.5 font-mono text-[10.5px] uppercase tracking-wide text-ink-faint">
        range
      </span>
      {TIME_PRESETS.map((p) => (
        <button
          key={p.label}
          type="button"
          onClick={() => setRange(p.minutes)}
          aria-pressed={activePreset === p.label}
          className={cn(
            "rounded px-2 py-0.5 font-mono text-[11.5px] transition-colors",
            activePreset === p.label
              ? "bg-accent-soft text-accent"
              : "text-ink-muted hover:text-ink",
          )}
        >
          {p.label}
        </button>
      ))}
    </div>
  );
}
