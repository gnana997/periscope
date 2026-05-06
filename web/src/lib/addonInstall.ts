// addonInstall.ts — pure helpers for InstallAddOnDialog (issue
// #119, PR-2). Extracted out so the (compat-filter, default-pick,
// schema-parse) logic is unit-testable without standing up a DOM.

import type { JSONSchema } from "./helmSchema";
import type { CatalogAddon, CatalogAddonVersion } from "./types";

// filterCompatibleVersions narrows an addon's compatible-versions
// list to entries that include the cluster's k8sVersion. When
// k8sVersion is empty/undefined, returns the full list verbatim
// (defensive: shouldn't happen in production since the catalog
// endpoint always supplies a cluster k8s version).
export function filterCompatibleVersions(
  addon: CatalogAddon,
  k8sVersion: string | undefined,
): CatalogAddonVersion[] {
  if (!k8sVersion) return addon.compatibleVersions;
  return addon.compatibleVersions.filter((v) =>
    v.kubernetesVersions.includes(k8sVersion),
  );
}

// pickDefaultVersion picks the version radio's initial selection.
// Priority: AWS-marked default → first compatible → empty string.
// AWS catalog responses arrive newest-first per AddonName, so the
// fallback to versions[0] also picks "latest compatible".
export function pickDefaultVersion(
  versions: readonly CatalogAddonVersion[],
): string {
  const def = versions.find((v) => v.default);
  if (def) return def.version;
  return versions[0]?.version ?? "";
}

// parseSchemaSafe returns the parsed JSON Schema object or undefined
// when the schema string is empty / malformed. Malformed schemas
// from AWS shouldn't happen in practice, but we degrade gracefully
// to YAML rather than blowing up the dialog — same defensive shape
// as parseSafe in HelmValuesEditor.
//
// Type asserted: JSON.parse on AWS's schema response either returns
// a valid schema object or it throws; we catch the throw, so the
// non-undefined branch is always shaped like JSONSchema.
export function parseSchemaSafe(raw: string | undefined): JSONSchema | undefined {
  if (!raw) return undefined;
  try {
    return JSON.parse(raw) as JSONSchema;
  } catch {
    return undefined;
  }
}

// buildInstallRequest assembles the wire-shape request body from
// the dialog's form state. Empty strings get omitted so the JSON
// body is minimal and the backend's whitelist on resolveConflicts
// matches an empty/missing field as "let AWS default" (NONE).
export function buildInstallRequest(args: {
  addonName: string;
  addonVersion: string;
  configurationValuesYaml: string;
  serviceAccountRoleArn: string;
  resolveConflicts: "" | "NONE" | "OVERWRITE" | "PRESERVE";
}): {
  addonName: string;
  addonVersion: string;
  configurationValues?: string;
  serviceAccountRoleArn?: string;
  resolveConflicts?: "" | "NONE" | "OVERWRITE" | "PRESERVE";
} {
  const out: ReturnType<typeof buildInstallRequest> = {
    addonName: args.addonName,
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
