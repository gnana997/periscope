// addonCatalog.ts — pure helpers for the EKS add-on catalog page
// (issue #119, PR-1).
//
// Extracted out of EKSAddOnsCatalogPage so the (owner-classification,
// compat-range, installed-state-merge) logic can be exercised by
// unit tests without standing up a DOM. Same pattern as upgradeInsights
// + nodegroups + helmSchema modules.

import type {
  AddonsListResponse,
  CatalogAddon,
  CatalogInstalled,
} from "./types";

// AWS-owned check matches the backend's isAWSOwned in
// eks_addon_catalog_handler.go: empty / "aws" / "amazon-web-services"
// are AWS-authored, anything else is third-party. Keeping the
// constants list in lockstep with the backend prevents the filter
// chip from disagreeing with the server-side sort order.
export function isAWSOwned(owner?: string): boolean {
  return !owner || owner === "aws" || owner === "amazon-web-services";
}

// compatRangeOf returns "1.27 – 1.30" for the (min, max) of a list
// of K8s minor strings. Returns null for empty input. Tolerates
// trailing noise after a "+" (e.g. "1.29.1+eksbuild.1") so the same
// helper works on AWS-style version strings without a separate
// parser. When min == max, returns the single version.
export function compatRangeOf(versions: string[]): string | null {
  if (versions.length === 0) return null;
  const parsed = versions
    .map(parseK8sMinor)
    .filter((v): v is { raw: string; major: number; minor: number } => v !== null);
  if (parsed.length === 0) return null;
  parsed.sort((a, b) => a.major - b.major || a.minor - b.minor);
  const lo = parsed[0].raw;
  const hi = parsed[parsed.length - 1].raw;
  return lo === hi ? lo : `${lo} – ${hi}`;
}

function parseK8sMinor(
  v: string,
): { raw: string; major: number; minor: number } | null {
  const dot = v.indexOf(".");
  if (dot <= 0) return null;
  const major = Number(v.slice(0, dot));
  const minorStr = v.slice(dot + 1);
  let end = minorStr.length;
  for (let i = 0; i < minorStr.length; i++) {
    const ch = minorStr.charCodeAt(i);
    if (ch < 48 || ch > 57) {
      end = i;
      break;
    }
  }
  if (end === 0) return null;
  const minor = Number(minorStr.slice(0, end));
  if (Number.isNaN(major) || Number.isNaN(minor)) return null;
  return { raw: v, major, minor };
}

// mergeInstalled fills in `installed` on catalog rows from the
// already-fetched useAddons() data. Best-effort fallback for the
// case where the backend's per-cluster addons cache was cold at
// catalog-request time (so server-side merge couldn't run).
//
// Server-side merge wins: a row that already has `installed` is
// passed through unchanged. The fallback only fills nulls.
export function mergeInstalled(
  catalog: readonly CatalogAddon[],
  addons: AddonsListResponse | undefined,
): CatalogAddon[] {
  if (!addons || addons.addons.length === 0) {
    return catalog.slice();
  }
  const byName = new Map<string, CatalogInstalled>();
  for (const a of addons.addons) {
    byName.set(a.name, { version: a.version ?? "", status: a.status });
  }
  return catalog.map((row) => {
    if (row.installed) return row;
    const fallback = byName.get(row.name);
    return fallback ? { ...row, installed: fallback } : row;
  });
}

// pickLatestForK8s returns the first compatible-versions entry whose
// k8s list contains the cluster's k8sVersion. Catalog entries arrive
// in AWS's default ordering (newest first per AddonName), so first-
// match is the latest compatible version. When k8sVersion is empty,
// returns the first entry verbatim.
export function pickLatestForK8s(
  versions: CatalogAddon["compatibleVersions"],
  k8sVersion: string | undefined,
): CatalogAddon["compatibleVersions"][number] | undefined {
  if (!k8sVersion) return versions[0];
  return versions.find((v) => v.kubernetesVersions.includes(k8sVersion));
}
