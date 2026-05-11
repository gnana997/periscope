// findingFilters — client-side render filters for the Security tab.
// Backend already pre-groups + pre-sorts findings; these helpers
// just hide rows from view based on the chip-row state. Keeps the
// network response unchanged and avoids a roundtrip on every chip
// toggle.

import type { CveFinding, CvePackageGroup } from "./types";

export interface FindingFilters {
  /** When set, only findings with this severity are kept.
   *  Lowercase names matching `severity` field after toUpperCase. */
  severity?: "critical" | "high" | "medium" | "low" | "informational";
  exploitOnly?: boolean;
  fixableOnly?: boolean;
}

export const NO_FILTERS: FindingFilters = {};

/** isExploit — Inspector's exploitAvailable field is a string
 *  ("YES" / "NO" / empty). Match the backend's `exploitTruthy`
 *  heuristic: any non-empty, non-"NO" value counts. */
export function isExploit(f: CveFinding): boolean {
  const v = (f.exploitAvailable ?? "").trim().toUpperCase();
  return v !== "" && v !== "NO" && v !== "FALSE";
}

export function isFixable(f: CveFinding): boolean {
  return !!(f.fixedVersion && f.fixedVersion.length > 0);
}

/** filterFinding decides whether a single finding passes the filter
 *  set. Used by filterPackageGroup. */
export function filterFinding(f: CveFinding, filters: FindingFilters): boolean {
  if (filters.severity) {
    const want = filters.severity.toUpperCase();
    const got = (f.severity ?? "").toUpperCase();
    const normalized = got === "INFO" ? "INFORMATIONAL" : got;
    if (normalized !== want) return false;
  }
  if (filters.exploitOnly && !isExploit(f)) return false;
  if (filters.fixableOnly && !isFixable(f)) return false;
  return true;
}

/** filterPackageGroup returns a shallow copy of the group with
 *  `findings` filtered. Counts / exploitCount / fixableCount on the
 *  returned group are NOT recomputed — the header still shows the
 *  group's full totals (which is what the operator wants: "this
 *  package has 87 CVEs, of which 4 match your filter"). */
export function filterPackageGroup(
  group: CvePackageGroup,
  filters: FindingFilters,
): CvePackageGroup {
  if (!filters.severity && !filters.exploitOnly && !filters.fixableOnly) {
    return group;
  }
  return {
    ...group,
    findings: group.findings.filter((f) => filterFinding(f, filters)),
  };
}

/** anyFilterActive returns true when at least one filter is set.
 *  Used to render the chip-row's "X / Y shown" hint only when it
 *  carries information. */
export function anyFilterActive(filters: FindingFilters): boolean {
  return !!(filters.severity || filters.exploitOnly || filters.fixableOnly);
}
