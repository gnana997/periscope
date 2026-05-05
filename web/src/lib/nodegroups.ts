// Pure helpers for the EKS managed node group surface (issue #103).
//
// The drift formatter is the most reused piece — both the page
// table and the card pill display the same string. Centralizing
// here keeps PR-3's drift-text tweaks a single-file change.

import type { NodegroupSummary } from "./types";

export type DriftLabel =
  | { kind: "custom" } // CUSTOM AMI — drift not tracked by AWS
  | { kind: "uncomputed" } // PR-2 / drift not yet computed
  | { kind: "current" } // up to date with the latest published AMI
  | { kind: "behind"; days: number; latest?: string };

/**
 * Classifies a nodegroup's drift state for UI rendering. Pure —
 * does not mutate or fetch anything.
 *
 * Order matters: CUSTOM AMI takes precedence over driftComputed
 * because even when the backend computes drift across the fleet,
 * we still want to render "not tracked" for custom-AMI rows
 * rather than a misleading "current" / "behind" badge.
 */
export function classifyDrift(ng: NodegroupSummary): DriftLabel {
  if (ng.customAmi) return { kind: "custom" };
  if (!ng.driftComputed) return { kind: "uncomputed" };
  if (!ng.isBehind) return { kind: "current" };
  return {
    kind: "behind",
    days: ng.daysBehind ?? 0,
    latest: ng.latestReleaseVersion,
  };
}
