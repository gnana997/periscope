// addonInstall.ts — pure helpers for InstallAddOnDialog (issue
// #119, PR-2). Extracted out so the (compat-filter, default-pick,
// schema-parse) logic is unit-testable without standing up a DOM.

import {
  buildFieldDescriptors,
  type FieldDescriptor,
  type FieldType,
  type JSONSchema,
} from "./helmSchema";
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

// compareAddonVersions compares two EKS-addon version strings of
// the form "v<x>.<y>.<z>(-<suffix>.<n>)?" — e.g.
//   v1.12.2-eksbuild.2  > v1.12.2-eksbuild.1
//   v1.12.2-eksbuild.2  > v1.12.1-eksbuild.4
//   v1.7.1-eksbuild.2   < v1.10.0-eksbuild.1   (numeric-aware,
//                                              not lexicographic)
// Returns >0 when a > b, <0 when a < b, 0 when equal or
// unparseable. Defensive fallback to 0 on bad shapes so callers
// can decide ("treat as equal" → no upgrade affordance).
export function compareAddonVersions(a: string, b: string): number {
  const ta = parseAddonVersion(a);
  const tb = parseAddonVersion(b);
  for (let i = 0; i < Math.max(ta.length, tb.length); i++) {
    const da = ta[i] ?? 0;
    const db = tb[i] ?? 0;
    if (da !== db) return da - db;
  }
  return 0;
}

function parseAddonVersion(v: string): number[] {
  const stripped = v.replace(/^v/, "");
  const [main, suffix] = stripped.split("-");
  const mainParts = (main ?? "").split(".").map((p) => Number(p));
  if (mainParts.some((n) => Number.isNaN(n))) return [];
  if (!suffix) return mainParts;
  // Suffix shapes vary ("eksbuild.2", "eksbuild.4"); we only care
  // about the trailing integer for ordering. Anything unparseable
  // becomes 0, which keeps the comparator total.
  const m = /\.(\d+)$/.exec(suffix);
  return [...mainParts, m ? Number(m[1]) : 0];
}

// generateAddonValuesYamlStub turns an addon's JSON Schema into a
// fully-commented YAML reference the install dialog seeds into the
// editor when the operator opens it for an addon they haven't
// configured yet. Without this, the YAML editor is an empty box —
// the operator has no way to discover what fields exist short of
// reading AWS docs in another tab. (For addons whose schema has
// $ref / allOf / arrays-of-objects, YAML mode is the auto-default
// per HelmValuesEditor's mode picker, so the discoverability
// problem matters most precisely where it bites hardest.)
//
// Format: every line starts `# ` at its YAML-correct indent. To
// override a leaf, the operator removes `# ` from the leaf line
// AND each of its parent (object) lines so the structure validates.
// Field descriptions are emitted as separate `# <text>` lines above
// each entry. Empty config still means "AWS uses all defaults" —
// commented lines are no-ops at the YAML parser.
export function generateAddonValuesYamlStub(schema: JSONSchema): string {
  const descriptors = buildFieldDescriptors(schema);
  if (descriptors.length === 0) return "";
  const lines: string[] = [
    "# All fields below are commented for reference. Uncomment and",
    "# edit only what you want to override; to uncomment a nested",
    "# key, also uncomment its parent key(s) so the YAML structure",
    "# validates. Empty config = AWS uses all defaults.",
    "",
  ];
  for (const d of descriptors) {
    emitStubLines(d, 0, lines);
  }
  // trim a trailing blank line if present so the editor doesn't
  // open with two blank rows at the bottom
  while (lines.length > 0 && lines[lines.length - 1] === "") lines.pop();
  return lines.join("\n") + "\n";
}

function emitStubLines(d: FieldDescriptor, depth: number, out: string[]) {
  const indent = "  ".repeat(depth);
  const key = d.path[d.path.length - 1] ?? "";
  const requiredHint = d.required ? "  # required" : "";

  if (d.description) {
    // Description split across lines if very long — keeps editor
    // line lengths reasonable.
    for (const line of wrapDescription(d.description, 70)) {
      out.push(`${indent}# ${line}`);
    }
  }

  if (d.type === "object" && d.children && d.children.length > 0) {
    out.push(`${indent}# ${key}:${requiredHint}`);
    for (const c of d.children) {
      emitStubLines(c, depth + 1, out);
    }
    if (depth === 0) out.push("");
    return;
  }

  if (d.type === "unsupported") {
    out.push(
      `${indent}# ${key}:  # ${d.unsupportedReason ?? "edit in YAML"}${requiredHint}`,
    );
    if (depth === 0) out.push("");
    return;
  }

  const defaultStr = formatDefaultLiteral(d.default, d.type);
  const enumHint =
    d.enum && d.enum.length > 0
      ? `  # one of: ${d.enum.map((v) => formatDefaultLiteral(v, d.type)).join(", ")}`
      : "";
  out.push(`${indent}# ${key}: ${defaultStr}${enumHint}${requiredHint}`);
  if (depth === 0) out.push("");
}

function formatDefaultLiteral(v: unknown, type: FieldType): string {
  if (v === undefined) {
    switch (type) {
      case "string":
        return '""';
      case "number":
      case "integer":
        return "0";
      case "boolean":
        return "false";
      case "array-of-primitives":
        return "[]";
      default:
        return "";
    }
  }
  if (v === null) return "null";
  if (typeof v === "string") return JSON.stringify(v);
  if (typeof v === "boolean" || typeof v === "number") return String(v);
  if (Array.isArray(v)) {
    return `[${v.map((x) => JSON.stringify(x)).join(", ")}]`;
  }
  return JSON.stringify(v);
}

function wrapDescription(text: string, max: number): string[] {
  const t = text.replace(/\s+/g, " ").trim();
  if (t.length <= max) return [t];
  const words = t.split(" ");
  const lines: string[] = [];
  let cur = "";
  for (const w of words) {
    if (cur.length === 0) {
      cur = w;
    } else if (cur.length + 1 + w.length > max) {
      lines.push(cur);
      cur = w;
    } else {
      cur = `${cur} ${w}`;
    }
  }
  if (cur) lines.push(cur);
  return lines;
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
