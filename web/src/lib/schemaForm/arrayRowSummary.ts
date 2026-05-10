// arrayRowSummary.ts — picks a one-line summary for an
// array-of-objects / array-of-discriminators row when collapsed.
// Pulled out of SchemaForm.tsx so the heuristics are unit-testable
// without rendering React.
//
// Strategy: pick a primary "identifier" field (name / key / host /
// secretName / topologyKey) — falling back to the first numeric or
// non-empty string. Then a secondary field (image / value /
// mountPath / port / etc.) for context. Result is rendered as
// `name: api · image: ghcr.io/api`.
//
// For array-of-discriminators rows: the active branch label takes
// the primary slot, and the row's `name` (if any) becomes secondary.
// Volumes ("ConfigMap · name: app-config") and EnvFromSource
// ("ConfigMap · name: app-config") both benefit.

import type { FieldDescriptor } from "./types";

export const IDENTIFIER_KEYS = [
  "name",
  "key",
  "host",
  "secretName",
  "topologyKey",
];

export const SECONDARY_KEYS = [
  "image",
  "value",
  "mountPath",
  "operator",
  "port",
  "containerPort",
  "path",
];

export function rowSummary(row: unknown): string {
  if (!row || typeof row !== "object" || Array.isArray(row)) return "";
  const obj = row as Record<string, unknown>;
  let primaryKey: string | undefined;
  let primaryValue: string | undefined;
  for (const k of IDENTIFIER_KEYS) {
    const v = obj[k];
    if (typeof v === "string" && v) {
      primaryKey = k;
      primaryValue = v;
      break;
    }
    if (typeof v === "number") {
      primaryKey = k;
      primaryValue = String(v);
      break;
    }
  }
  let secondaryKey: string | undefined;
  let secondaryValue: string | undefined;
  for (const k of SECONDARY_KEYS) {
    if (k === primaryKey) continue;
    const v = obj[k];
    if (v === undefined || v === null || v === "") continue;
    if (typeof v === "object") continue;
    secondaryKey = k;
    secondaryValue = String(v);
    break;
  }
  const parts: string[] = [];
  if (primaryKey && primaryValue) parts.push(`${primaryKey}: ${truncate(primaryValue, 40)}`);
  if (secondaryKey && secondaryValue)
    parts.push(`${secondaryKey}: ${truncate(secondaryValue, 40)}`);
  return parts.join(" · ");
}

export function discriminatorRowSummary(
  row: unknown,
  branches: NonNullable<FieldDescriptor["branches"]>,
): string {
  if (!row || typeof row !== "object" || Array.isArray(row)) return "";
  const obj = row as Record<string, unknown>;
  const active = branches.find((b) => b.discriminatorKey && b.discriminatorKey in obj);
  if (!active) return rowSummary(row);
  const parts: string[] = [active.label];
  for (const k of IDENTIFIER_KEYS) {
    const v = obj[k];
    if (typeof v === "string" && v) {
      parts.push(`${k}: ${truncate(v, 40)}`);
      break;
    }
  }
  return parts.join(" · ");
}

export function truncate(s: string, n: number): string {
  return s.length > n ? `${s.slice(0, n - 1)}…` : s;
}

/** Default-open set for a row count: open the only row, collapse all
 *  if multiple. Used to seed the open-set on first mount. */
export function initialOpenSet(rowCount: number): Set<number> {
  if (rowCount === 1) return new Set([0]);
  return new Set();
}

/** Shift open-indices down after removing row at `removedIdx` so
 *  open state stays attached to the same logical row. */
export function shiftOpenOnRemove(prev: Set<number>, removedIdx: number): Set<number> {
  const next = new Set<number>();
  for (const i of prev) {
    if (i === removedIdx) continue;
    next.add(i > removedIdx ? i - 1 : i);
  }
  return next;
}
