// FindingFilterChips — the filter-chip strip at the top of every
// SecurityTab variant. Click a severity chip to filter; click
// `exploits` to toggle exploit-only; click `fixable` to toggle
// fixable-only. The active state is presentation-only — filtering
// runs in `applyFilters` against backend-provided package groups.

import type { CveSeverityCounts } from "../../lib/types";
import type { FindingFilters } from "../../lib/findingFilters";
import { cn } from "../../lib/cn";

interface Props {
  /** Totals across all containers / findings BEFORE filtering. The
   *  per-chip count shows the total in that severity bucket, not the
   *  filtered subset. */
  totals: CveSeverityCounts;
  /** Number of findings with exploitAvailable truthy (any severity). */
  exploitCount: number;
  /** Number of findings with a published fix. */
  fixableCount: number;
  /** Total findings (sum of all severities). Used to render the
   *  "X / Y shown" hint when filters are active. */
  totalFindings: number;
  /** Visible-after-filter count (computed by caller). */
  visibleFindings: number;
  filters: FindingFilters;
  onChange: (next: FindingFilters) => void;
}

type SeverityKey = "critical" | "high" | "medium" | "low";

const SEVERITY_TONE: Record<SeverityKey, string> = {
  critical: "text-red border-red/40 bg-red/5",
  high: "text-amber-300 border-amber-400/40 bg-amber-400/5",
  medium: "text-yellow border-yellow/40 bg-yellow/5",
  low: "text-ink-muted border-ink-faint/40 bg-surface-2",
};

export function FindingFilterChips({
  totals,
  exploitCount,
  fixableCount,
  totalFindings,
  visibleFindings,
  filters,
  onChange,
}: Props) {
  const toggleSeverity = (sev: SeverityKey) => {
    onChange({
      ...filters,
      severity: filters.severity === sev ? undefined : sev,
    });
  };
  const toggleExploit = () =>
    onChange({ ...filters, exploitOnly: !filters.exploitOnly });
  const toggleFixable = () =>
    onChange({ ...filters, fixableOnly: !filters.fixableOnly });

  const hasFilters =
    !!filters.severity || !!filters.exploitOnly || !!filters.fixableOnly;

  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-border px-5 py-2 font-mono text-[11.5px]">
      <SeverityChipButton
        label={`${totals.critical} critical`}
        active={filters.severity === "critical"}
        disabled={totals.critical === 0}
        tone={SEVERITY_TONE.critical}
        onClick={() => toggleSeverity("critical")}
      />
      <SeverityChipButton
        label={`${totals.high} high`}
        active={filters.severity === "high"}
        disabled={totals.high === 0}
        tone={SEVERITY_TONE.high}
        onClick={() => toggleSeverity("high")}
      />
      <SeverityChipButton
        label={`${totals.medium} medium`}
        active={filters.severity === "medium"}
        disabled={totals.medium === 0}
        tone={SEVERITY_TONE.medium}
        onClick={() => toggleSeverity("medium")}
      />
      <SeverityChipButton
        label={`${totals.low} low`}
        active={filters.severity === "low"}
        disabled={totals.low === 0}
        tone={SEVERITY_TONE.low}
        onClick={() => toggleSeverity("low")}
      />
      <span className="mx-1 text-ink-faint">·</span>
      <FlagChip
        label={`exploits ${exploitCount}`}
        active={!!filters.exploitOnly}
        disabled={exploitCount === 0}
        onClick={toggleExploit}
      />
      <FlagChip
        label="fixable only"
        active={!!filters.fixableOnly}
        disabled={fixableCount === 0}
        onClick={toggleFixable}
      />
      {hasFilters && (
        <span className="ml-auto text-ink-faint">
          {visibleFindings} / {totalFindings} shown
        </span>
      )}
    </div>
  );
}

function SeverityChipButton({
  label,
  active,
  disabled,
  tone,
  onClick,
}: {
  label: string;
  active: boolean;
  disabled: boolean;
  tone: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "rounded-sm border px-2 py-0.5 transition-colors",
        disabled && "cursor-default opacity-30",
        !disabled && tone,
        active && "ring-1 ring-accent",
      )}
    >
      {label}
    </button>
  );
}

function FlagChip({
  label,
  active,
  disabled,
  onClick,
}: {
  label: string;
  active: boolean;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className={cn(
        "rounded-sm border border-border px-2 py-0.5 text-ink-muted transition-colors",
        disabled && "cursor-default opacity-30",
        !disabled && "hover:bg-surface-2",
        active && "border-accent text-ink ring-1 ring-accent",
      )}
    >
      {label}
    </button>
  );
}
