// drift — detect changes to a K8s resource between when the editor
// mounted (pristine meta) and the most recent server poll (fresh
// meta). Drives Phase 3's "the resource changed under you" flow.
//
// The naive signal — "resourceVersion changed" — is far too noisy:
// kubelet updates `status` on a busy Pod every few seconds without
// touching anything a human cares about. We filter to *real spec
// writes* by inspecting `managedFields`:
//
//   1. Skip if resourceVersion is unchanged (no writes at all).
//   2. Find the newest managedFields entry by `time`. If it isn't
//      newer than the youngest pristine entry, no manager has
//      written since mount — the rv bumped via an unmanaged status
//      writer; ignore.
//   3. Skip if the writer is us (`periscope-spa`) — that's our own
//      apply, briefly visible before react-query invalidates.
//   4. Walk the entry's `fieldsV1` to dotted paths. Empty walk →
//      `null` (writer touched only fields we don't visualize).
//
// What survives all four filters is a real, attributable, field-
// touching write by another actor. The UI surfaces it via banner.

import type { ManagedFieldsEntry, ResourceMeta } from "./api";
import { classifyManager, type ManagerCategory } from "./managers";
import { parseManagedFields } from "./managedFields";

/** The manager name we send as field-manager on every SSA apply. */
export const PERISCOPE_MANAGER = "periscope-spa";

export interface DriftInfo {
  manager: string;
  category: ManagerCategory;
  /** Dotted YAML paths the writer touched (from fieldsV1 walk). */
  paths: string[];
  /** ISO timestamp from the managedFields entry. */
  at: string;
  /** True when fresh.generation differs from pristine.generation. */
  generationChanged: boolean;
}

/**
 * describeDrift returns a DriftInfo when the cluster's view has been
 * meaningfully modified by a non-Periscope writer since `pristine`,
 * or null when nothing actionable has changed.
 */
export function describeDrift(
  pristine: ResourceMeta,
  fresh: ResourceMeta,
): DriftInfo | null {
  // 1. No write at all.
  if (fresh.resourceVersion === pristine.resourceVersion) return null;

  // 2. Newest fresh entry must be newer than youngest pristine entry.
  const newest = newestEntry(fresh.managedFields);
  if (!newest || !newest.time) return null;
  const pristineYoungest = youngestEntryTime(pristine.managedFields);
  if (pristineYoungest !== null && newest.time <= pristineYoungest) {
    return null;
  }

  // 3. Filter our own writes.
  if (newest.manager === PERISCOPE_MANAGER) return null;

  // 4. Field walk — must be non-empty.
  const owners = parseManagedFields([newest]);
  if (owners.length === 0) return null;

  return {
    manager: newest.manager,
    category: classifyManager(newest.manager).category,
    paths: owners.map((o) => o.path),
    at: newest.time,
    generationChanged: fresh.generation !== pristine.generation,
  };
}

/** Compare ISO timestamps. ISO 8601 strings sort lexicographically. */
function newestEntry(
  entries: ManagedFieldsEntry[] | undefined,
): ManagedFieldsEntry | null {
  if (!entries || entries.length === 0) return null;
  let best: ManagedFieldsEntry | null = null;
  for (const e of entries) {
    if (!e.time) continue;
    if (!best || (best.time && e.time > best.time)) best = e;
  }
  return best;
}

/** Largest `time` across pristine entries — the bar a fresh write must clear. */
function youngestEntryTime(
  entries: ManagedFieldsEntry[] | undefined,
): string | null {
  if (!entries || entries.length === 0) return null;
  let max: string | null = null;
  for (const e of entries) {
    if (!e.time) continue;
    if (!max || e.time > max) max = e.time;
  }
  return max;
}
