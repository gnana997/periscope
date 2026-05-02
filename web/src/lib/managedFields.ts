// managedFields — utilities for parsing K8s SSA managedFields metadata.
//
// K8s tracks per-field ownership via metadata.managedFields. Each entry
// has a `fieldsV1` tree with a peculiar key encoding:
//
//   "f:<name>"             — a field named <name>
//   "k:<json>"             — a list item, keyed by the merge key encoded
//                             as JSON (e.g. {"name":"nginx"})
//   "i:<index>"            — a list item, keyed by integer index
//   "v:<value>"            — a set member (atomic value)
//
// Example fieldsV1 from a kustomize-controller entry on a Deployment:
//
//   "f:spec":
//     "f:replicas": {}
//     "f:template":
//       "f:spec":
//         "f:containers":
//           "k:{\"name\":\"nginx\"}":
//             "f:image": {}
//
// We flatten this to dotted paths matching the YAML editor's path
// computation:
//
//   spec.replicas
//   spec.template.spec.containers[name=nginx].image
//
// Used by:
//   - The owner-glyph margin (path → manager map for color-coding lines)
//   - The 409 conflict resolver (parses Status.details.causes[].field
//     into the same dotted format for cross-referencing)

import type { ManagedFieldsEntry } from "./api";

export interface FieldOwner {
  path: string;
  manager: string;
  // The "operation" field on a managedFields entry. Apply means SSA;
  // Update means an old client-side write. Apply is what we contend
  // with on conflict; Update fields are usually safe to seize.
  operation: "Apply" | "Update";
}

/**
 * walkFieldsV1 turns a fieldsV1 sub-tree into a list of dotted paths.
 * Recursive; appends segments as it descends. Tolerant of malformed
 * input (missing prefixes, non-object values) — returns whatever it
 * could parse.
 */
function walkFieldsV1(
  node: unknown,
  prefix: string,
  out: string[],
): void {
  if (!node || typeof node !== "object") return;
  for (const [key, value] of Object.entries(node as Record<string, unknown>)) {
    let segment: string | null = null;
    if (key.startsWith("f:")) {
      segment = key.slice(2);
    } else if (key.startsWith("k:")) {
      try {
        const keyObj = JSON.parse(key.slice(2)) as Record<string, unknown>;
        const entries = Object.entries(keyObj);
        if (entries.length > 0) {
          const [k, v] = entries[0];
          segment = `[${k}=${String(v)}]`;
        }
      } catch {
        // malformed k: key, skip
      }
    } else if (key.startsWith("i:")) {
      segment = `[${key.slice(2)}]`;
    } else if (key === ".") {
      // ".": {} means "this whole subtree is owned" — emit the prefix as a leaf
      if (prefix) out.push(prefix);
      continue;
    } else {
      // Unknown prefix; skip
      continue;
    }
    if (!segment) continue;
    // For merge-key segments (start with [), don't introduce a dot
    const childPath = segment.startsWith("[")
      ? `${prefix}${segment}`
      : prefix
        ? `${prefix}.${segment}`
        : segment;
    if (typeof value === "object" && value !== null && Object.keys(value as object).length > 0) {
      walkFieldsV1(value, childPath, out);
    } else {
      // leaf — empty {} means "this field is owned"
      out.push(childPath);
    }
  }
}

/**
 * parseManagedFields produces a flat list of (path, manager, operation)
 * tuples from a managedFields array. Skips entries with no fieldsV1.
 */
export function parseManagedFields(
  entries: ManagedFieldsEntry[] | null | undefined,
): FieldOwner[] {
  if (!entries) return [];
  const out: FieldOwner[] = [];
  for (const entry of entries) {
    if (!entry.fieldsV1 || entry.fieldsType !== "FieldsV1") continue;
    const paths: string[] = [];
    walkFieldsV1(entry.fieldsV1, "", paths);
    for (const path of paths) {
      out.push({
        path,
        manager: entry.manager,
        operation: entry.operation,
      });
    }
  }
  return out;
}

/**
 * pathToManager builds a Map<dotted-path, manager> from parsed owners.
 * Used for O(1) lookup when applying glyph decorations to editor lines.
 *
 * If multiple managers own the same path (rare but possible — usually
 * a result of a SSA migration), we keep the LAST one written. In
 * practice K8s coalesces these so it's a non-issue.
 */
export function pathToManager(owners: FieldOwner[]): Map<string, string> {
  const map = new Map<string, string>();
  for (const owner of owners) {
    map.set(owner.path, owner.manager);
  }
  return map;
}

/**
 * normalizeStatusFieldPath converts a K8s Status conflict cause's
 * field reference (e.g. ".spec.replicas" or ".spec.containers[name=\"x\"].image")
 * into the dotted form the editor uses (no leading dot, no quoted
 * keys). The apiserver returns slightly different formats across
 * versions, so we normalise rather than relying on exact equality.
 */
export function normalizeStatusFieldPath(field: string): string {
  let p = field.trim();
  if (p.startsWith(".")) p = p.slice(1);
  // Quote-strip in [name="x"] / [name='x'] → [name=x]
  p = p.replace(/\[(\w+)=["']([^"']+)["']\]/g, "[$1=$2]");
  return p;
}
