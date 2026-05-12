// PackageGroupBlock — renders one server-grouped package as a
// collapsible block. Header shows "package · X CVEs · upgrade
// current → suggestedFix" + a compact severity chip. Body is the
// sorted CVE list rendered via FindingRow.
//
// The grouping + sorting is computed server-side (internal/cve/
// findings_group.go) so this component just renders what it gets;
// the same shape feeds the future MCP / AI-agent tool layer.

import { useState } from "react";
import type { CvePackageGroup } from "../../lib/types";
import { cn } from "../../lib/cn";
import { SeverityChip } from "./SeverityChip";
import { FindingRow } from "./FindingRow";

interface Props {
  group: CvePackageGroup;
  /** Total findings in the group BEFORE any client-side filter (the
   *  header always reflects the group's full size; the body lists
   *  only the filtered subset). */
  totalCount: number;
  /** Default-expanded for the first group on each container (the
   *  one with the highest priority). Lets the operator see the most
   *  urgent CVEs without an extra click. */
  defaultOpen?: boolean;
}

export function PackageGroupBlock({ group, totalCount, defaultOpen }: Props) {
  const [open, setOpen] = useState(!!defaultOpen);
  const counts = {
    critical: group.counts.critical,
    high: group.counts.high,
    medium: group.counts.medium,
    low: group.counts.low,
    informational: group.counts.informational,
  };
  const shownCount = group.findings.length;
  const upgradeText =
    group.suggestedFix && group.currentVersion
      ? `${group.currentVersion} → ${group.suggestedFix}`
      : group.suggestedFix
      ? `→ ${group.suggestedFix}`
      : "no fix published";

  return (
    <div className="mb-2 overflow-hidden rounded-sm border border-border bg-surface-2/30">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className={cn(
          "flex w-full items-center gap-3 px-3 py-1.5 text-left font-mono text-[12px]",
          "hover:bg-surface-2/60 transition-colors",
        )}
      >
        <span className="text-ink-faint">{open ? "▼" : "▶"}</span>
        <span className="flex-1 truncate text-ink">{group.packageName}</span>
        <SeverityChip mode="compact" counts={counts} state="has-findings" />
        <span className="text-[11px] text-ink-faint">
          {totalCount} CVE{totalCount === 1 ? "" : "s"}
        </span>
      </button>
      <div className="border-t border-border px-3 py-1 font-mono text-[11px] text-ink-muted">
        <span>upgrade {upgradeText}</span>
        {group.exploitCount > 0 && (
          <span className="ml-3 text-red">
            {group.exploitCount} exploit{group.exploitCount === 1 ? "" : "s"}
          </span>
        )}
        {group.fixableCount < totalCount && (
          <span className="ml-3 text-ink-faint">
            {totalCount - group.fixableCount} not yet fixable
          </span>
        )}
      </div>
      {open && (
        <div className="border-t border-border bg-bg/40">
          {shownCount === 0 ? (
            <div className="px-3 py-2 text-[11px] text-ink-faint">
              no findings match the active filters
            </div>
          ) : (
            group.findings.map((f, i) => (
              <FindingRow key={f.arn ?? `${f.cve}-${i}`} finding={f} />
            ))
          )}
        </div>
      )}
    </div>
  );
}
