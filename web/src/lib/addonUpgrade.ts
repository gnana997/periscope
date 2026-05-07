// addonUpgrade.ts — pure helpers for UpgradeAddOnDialog (issue
// #119, PR-3). Same pattern as addonInstall — testable without DOM.

import type { AddonUpgradeRequest, CatalogAddonVersion } from "./types";

// filterUpgradeTargets drops the currently-installed version from
// the compatible-versions list. Upgrading "to the same version" is
// a noop and AWS rejects it with InvalidParameterException; the
// dialog's radio shouldn't even offer it. Empty/undefined installed
// is a defensive no-op (returns the input list verbatim).
export function filterUpgradeTargets(
  versions: readonly CatalogAddonVersion[],
  installedVersion: string | undefined,
): CatalogAddonVersion[] {
  if (!installedVersion) return versions.slice();
  return versions.filter((v) => v.version !== installedVersion);
}

// pickUpgradeDefault picks the radio's initial selection.
// Priority: AWS-marked default → first entry → empty string.
// AWS catalog responses are newest-first per AddonName, so the
// fallback to versions[0] picks "latest compatible", which is
// usually the operator's intent.
export function pickUpgradeDefault(
  versions: readonly CatalogAddonVersion[],
): string {
  const def = versions.find((v) => v.default);
  if (def) return def.version;
  return versions[0]?.version ?? "";
}

// buildUpgradeRequest assembles the wire-shape request body from
// the dialog's form state. Same omit-empty-fields shape as
// buildInstallRequest (in addonInstall.ts) but without the
// addonName field — that's a URL param on PUT.
export function buildUpgradeRequest(args: {
  addonVersion: string;
  configurationValuesYaml: string;
  serviceAccountRoleArn: string;
  resolveConflicts: "" | "NONE" | "OVERWRITE" | "PRESERVE";
}): AddonUpgradeRequest {
  const out: AddonUpgradeRequest = {
    addonVersion: args.addonVersion,
  };
  if (args.configurationValuesYaml.trim()) {
    out.configurationValues = args.configurationValuesYaml;
  }
  if (args.serviceAccountRoleArn.trim()) {
    out.serviceAccountRoleArn = args.serviceAccountRoleArn;
  }
  if (args.resolveConflicts !== "") {
    out.resolveConflicts = args.resolveConflicts;
  }
  return out;
}
