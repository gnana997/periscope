// Pure helpers for the EKS Upgrade Insights surface (issue #103).
//
// The page-level component delegates to these so tests can pin the
// behavior without spinning up a full DOM renderer — same pattern
// as buildCanIDecision in useCanI.

import type {
  UpgradeInsightStatus,
  UpgradeInsightSummary,
} from "./types";

// ERROR first, PASSING last — operators open this page when planning
// an upgrade and want the blockers up top. UNKNOWN is bucketed
// between WARNING and PASSING so it doesn't dilute the actionable
// rows but also isn't filed alongside "all clear".
const STATUS_ORDER: Record<UpgradeInsightStatus, number> = {
  ERROR: 0,
  WARNING: 1,
  UNKNOWN: 2,
  PASSING: 3,
};

/**
 * Returns a copy of `rows` sorted ERROR → WARNING → UNKNOWN → PASSING,
 * with ties broken by display name (or id when name is empty). Pure
 * — does not mutate the input.
 */
export function sortInsightsByStatus(
  rows: UpgradeInsightSummary[],
): UpgradeInsightSummary[] {
  const copy = rows.slice();
  copy.sort((a, b) => {
    const da = STATUS_ORDER[a.status] ?? 99;
    const db = STATUS_ORDER[b.status] ?? 99;
    if (da !== db) return da - db;
    return (a.name || a.id).localeCompare(b.name || b.id);
  });
  return copy;
}
