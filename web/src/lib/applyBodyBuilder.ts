// applyBodyBuilder — single source of truth for SSA Apply request bodies.
//
// Periscope's SPA used to construct apply bodies inconsistently:
//   - YAML mode → minimal SSA via computeOps + buildMinimalSSA
//   - Form mode → full draft buffer
//   - Mutation hooks (scale/labels/suspend/restart/cordon) → ad-hoc minimal
//
// The full-buffer path made periscope-spa claim ownership of every field
// in the body. A subsequent apply that omitted any field would release
// that ownership; with no other manager owning it, the field was dropped.
// In production this manifested as ALB controller annotations vanishing
// after an Ingress port edit (see issue #181).
//
// The fix: a retained-ownership minimal patch. Each apply contains:
//   1. The user's edited paths (computeOps(baseline, draft))
//   2. Every path periscope-spa already owns, valued at current cluster
//      state (walked from managedFields[manager=periscope-spa].fieldsV1)
//
// This matches SSA's "fully-specified intent" doctrine — we keep
// asserting what we've claimed, plus our new edit — and eliminates
// the migration cliff a naïve minimal-patch switch would create.
//
// For first-ever applies (no prior ownership yet), this degenerates to
// today's minimal-patch behavior.
//
// Note on path representation: we work in PathSegment[] form everywhere
// internally because the dotted-string form (`metadata.annotations.alb
// .ingress.kubernetes.io/scheme`) is ambiguous when a map key contains
// dots (annotation/label keys frequently do). Stringification happens
// only at the boundary for UI / exclusion-callback consumers.

import {
  buildMinimalSSA,
  parseOrThrow,
  computeOps,
  type Identity,
  type Op,
  type PathSegment,
  type MergeKey,
  type IndexKey,
} from "./yamlPatch";
import type { ManagedFieldsEntry } from "./api";

const DEFAULT_SELF_MANAGER = "periscope-spa";

/**
 * Thrown when the caller cannot construct a retained-ownership body
 * because managedFields hasn't loaded yet. Callers should gate the
 * Apply CTA on metaQuery.isPending; this error is the defensive net
 * for races (apply clicked before the 15s meta poll lands).
 */
export class ManagedFieldsUnavailableError extends Error {
  constructor() {
    super("managedFields not yet loaded — try again in a moment.");
    this.name = "ManagedFieldsUnavailableError";
  }
}

export interface BuildBodyInput {
  /** YAML the user mounted against (anchor for `computeOps`). */
  baseline: string;
  /** Current editor buffer (what the user wants to apply). */
  draft: string;
  /**
   * Latest server YAML — source of values for prior-owned paths the
   * user hasn't touched. In YamlEditor this can reuse `pristineLocked`
   * (kept fresh by the pristine-swap effect on clean buffers).
   */
  current: string;
  identity: Identity;
  /** From `useResourceMeta`'s ResourceMeta.managedFields. */
  managedFields: ManagedFieldsEntry[] | null | undefined;
  /** Defaults to "periscope-spa". Overridable for tests. */
  selfManager?: string;
  /**
   * Optional filter: prior-owned paths the user has explicitly chosen
   * to release (via the 409 ConflictResolutionView "revert" action).
   * Receives the dotted-string form of each candidate retained path.
   * Returning true drops the path from the body so periscope-spa
   * releases ownership of it on this apply.
   */
  excludePriorOwned?: (path: string) => boolean;
}

export interface BuildBodyResult {
  /** Stringified SSA body ready to POST. */
  yaml: string;
  /**
   * User-edit ops only (not the synthetic retain-ownership ops).
   * Exposed so callers like `parseConflictCauses` can match
   * Status.details.causes[].field against the paths the user actually
   * touched.
   */
  ops: Op[];
  /**
   * Dotted-form paths the body re-asserts ownership of. Useful for
   * UI affordances ("+ N retained fields you already owned" in the
   * PatchPreviewDrawer).
   */
  priorOwnedPaths: string[];
  /**
   * True when managedFields had no prior periscope-spa Apply entry.
   * The body in this case is identical to the legacy minimal SSA.
   */
  firstApply: boolean;
}

export interface BuildBodyFromOpsInput {
  ops: Op[];
  current: string;
  identity: Identity;
  managedFields: ManagedFieldsEntry[] | null | undefined;
  selfManager?: string;
  /**
   * See `BuildBodyInput.excludePriorOwned`. Used by `runApplyResolved`
   * in YamlEditor to drop paths the operator chose to revert in the
   * field-manager conflict view.
   */
  excludePriorOwned?: (path: string) => boolean;
}

export interface BuildBodyFromOpsResult {
  yaml: string;
  priorOwnedPaths: string[];
  firstApply: boolean;
}

/**
 * Editor-driven entry: diffs `baseline` vs `draft` for user edits,
 * walks `managedFields` for prior periscope-spa ownership, composes
 * the SSA body.
 */
export function buildRetainedOwnershipBody(
  input: BuildBodyInput,
): BuildBodyResult {
  const {
    baseline,
    draft,
    current,
    identity,
    managedFields,
    selfManager = DEFAULT_SELF_MANAGER,
    excludePriorOwned,
  } = input;

  assertManagedFieldsPresent(managedFields);

  const ops = computeOps(baseline, draft);
  const { yaml, priorOwnedPaths, firstApply } = composeBody({
    ops,
    current,
    identity,
    managedFields: managedFields as ManagedFieldsEntry[],
    selfManager,
    excludePriorOwned,
  });

  return { yaml, ops, priorOwnedPaths, firstApply };
}

/**
 * Mutation-hook entry: caller has pre-computed ops (scale/labels/etc.)
 * and we only need to layer in retained ownership.
 */
export function buildRetainedOwnershipBodyFromOps(
  input: BuildBodyFromOpsInput,
): BuildBodyFromOpsResult {
  const {
    ops,
    current,
    identity,
    managedFields,
    selfManager = DEFAULT_SELF_MANAGER,
    excludePriorOwned,
  } = input;

  assertManagedFieldsPresent(managedFields);

  return composeBody({
    ops,
    current,
    identity,
    managedFields: managedFields as ManagedFieldsEntry[],
    selfManager,
    excludePriorOwned,
  });
}

/**
 * Walks managedFields for `selfManager` Apply entries and returns the
 * dotted-form paths of every claimed field, deduped.
 *
 * Note: dotted form is for display only. The builder works in segment
 * form internally because dotted strings are ambiguous when map keys
 * contain dots (annotation keys regularly do).
 */
export function selectSelfOwnedPaths(
  entries: ManagedFieldsEntry[],
  selfManager: string,
): string[] {
  const segs = selectSelfOwnedSegments(entries, selfManager);
  const seen = new Set<string>();
  for (const path of segs) seen.add(stringifyPath(path));
  return [...seen];
}

// ---------------- internals ----------------

interface ComposeBodyInput {
  ops: Op[];
  current: string;
  identity: Identity;
  managedFields: ManagedFieldsEntry[];
  selfManager: string;
  excludePriorOwned?: (path: string) => boolean;
}

function composeBody(input: ComposeBodyInput): BuildBodyResult {
  const { ops, current, identity, managedFields, selfManager, excludePriorOwned } = input;

  const selfOwnedSegments = selectSelfOwnedSegments(managedFields, selfManager);
  const firstApply = selfOwnedSegments.length === 0;

  if (firstApply) {
    // Degenerate path: behaves exactly like the legacy minimal SSA.
    return {
      yaml: buildMinimalSSA(ops, identity),
      ops,
      priorOwnedPaths: [],
      firstApply: true,
    };
  }

  // Apply the user-side path-release filter (used by runApplyResolved
  // when the operator chose to revert a field in the conflict view).
  const retainSegments = excludePriorOwned
    ? selfOwnedSegments.filter((segs) => !excludePriorOwned(stringifyPath(segs)))
    : selfOwnedSegments;

  // Extract current values for the retained paths. Paths we can't
  // resolve (missing in current, IndexKey segments we don't materialize)
  // are silently dropped — the worst case is we release ownership of
  // one path on this apply, which is no worse than today's behavior.
  const retainedOps = collectRetainedOps(retainSegments, current);
  const retainedPathStrings = retainedOps.map((rop) => stringifyPath(rop.path));

  // Compose: retained ops first, user ops second. Edit-wins follows
  // from order — applyOpToTree's setLeaf overwrites the same slot.
  const combinedOps: Op[] = [...retainedOps, ...ops];
  const yaml = buildMinimalSSA(combinedOps, identity);

  return {
    yaml,
    ops,
    priorOwnedPaths: retainedPathStrings,
    firstApply: false,
  };
}

/**
 * Walks the managedFields entries for `selfManager` (Apply op only)
 * and emits each owned path as a PathSegment[]. Unions across
 * multiple entries (apiVersion drift produces multiple entries for
 * the same manager; the apiserver coalesces ownership on the next
 * Apply regardless of which entry recorded which subset).
 */
function selectSelfOwnedSegments(
  entries: ManagedFieldsEntry[],
  selfManager: string,
): PathSegment[][] {
  const out: PathSegment[][] = [];
  const seenKeys = new Set<string>();
  for (const entry of entries) {
    if (entry.manager !== selfManager) continue;
    if (entry.operation !== "Apply") continue;
    if (!entry.fieldsV1 || entry.fieldsType !== "FieldsV1") continue;
    const collected: PathSegment[][] = [];
    walkFieldsV1ToSegments(entry.fieldsV1, [], collected);
    for (const segs of collected) {
      // Dedup across entries via stringified key — duplicates across
      // apiVersion entries are common and harmless.
      const k = stringifyPath(segs);
      if (seenKeys.has(k)) continue;
      seenKeys.add(k);
      out.push(segs);
    }
  }
  return out;
}

/**
 * Segment-aware mirror of managedFields.walkFieldsV1. Emits PathSegment[]
 * lists (no string concatenation) so map keys containing dots survive
 * intact.
 *
 *   "f:<name>"  → string segment <name>
 *   "k:<json>"  → MergeKey { [k]: v }   (one-entry JSON object)
 *   "i:<n>"     → IndexKey { idx: n }
 *   "."         → "this whole subtree is owned" — emit the current prefix
 *
 * Tolerant of malformed input: bad k: payloads / unknown prefixes are
 * skipped silently.
 */
function walkFieldsV1ToSegments(
  node: unknown,
  prefix: PathSegment[],
  out: PathSegment[][],
): void {
  if (!node || typeof node !== "object") return;
  for (const [key, value] of Object.entries(node as Record<string, unknown>)) {
    let segment: PathSegment | null = null;
    if (key === ".") {
      if (prefix.length > 0) out.push([...prefix]);
      continue;
    } else if (key.startsWith("f:")) {
      segment = key.slice(2);
    } else if (key.startsWith("k:")) {
      try {
        const keyObj = JSON.parse(key.slice(2)) as Record<string, unknown>;
        const entries = Object.entries(keyObj);
        if (entries.length > 0) {
          const [k, v] = entries[0];
          segment = { [k]: String(v) } as MergeKey;
        }
      } catch {
        // malformed k: payload, skip this branch entirely
      }
    } else if (key.startsWith("i:")) {
      const n = Number(key.slice(2));
      if (Number.isInteger(n)) segment = { idx: n } as IndexKey;
    } else {
      // Unknown prefix (e.g. "v:" set-of-atomic-values) — skip
      continue;
    }
    if (segment === null) continue;
    const childPrefix = [...prefix, segment];
    if (typeof value === "object" && value !== null && Object.keys(value as object).length > 0) {
      walkFieldsV1ToSegments(value, childPrefix, out);
    } else {
      // leaf — empty {} means "this exact field is owned"
      out.push(childPrefix);
    }
  }
}

function collectRetainedOps(
  paths: PathSegment[][],
  currentYaml: string,
): Op[] {
  let currentObj: unknown;
  try {
    currentObj = parseOrThrow(currentYaml).obj;
  } catch {
    // If we can't parse current state, fall through with no retained
    // ops — the caller's user-edit ops still apply. This is intentional:
    // the editor's apply already has an error path for unparseable
    // YAML; we shouldn't crash the body builder on top of that.
    return [];
  }

  const ops: Op[] = [];
  for (const segs of paths) {
    // IndexKey segments come from fieldsV1 "i:" markers on atomic
    // lists (set semantics). applyOpToTree.stepInto doesn't materialize
    // these positionally, and re-asserting a single index across an
    // atomic list is semantically dicey anyway — drop with a warning.
    if (segs.some(isIndexKey)) {
      console.warn(
        `[applyBodyBuilder] dropping retained path with IndexKey segment (atomic list): ${stringifyPath(segs)}`,
      );
      continue;
    }
    const value = walkValue(currentObj, segs);
    if (value === undefined) {
      // The field was removed from the resource between the last
      // apply and now. Don't re-assert it — let ownership lapse.
      continue;
    }
    ops.push({ op: "replace", path: segs, value });
  }
  return ops;
}

function walkValue(root: unknown, segs: PathSegment[]): unknown {
  let node: unknown = root;
  for (const seg of segs) {
    if (node === undefined || node === null) return undefined;
    if (typeof seg === "string") {
      if (typeof node !== "object" || Array.isArray(node)) return undefined;
      node = (node as Record<string, unknown>)[seg];
    } else if (isIndexKey(seg)) {
      if (!Array.isArray(node)) return undefined;
      node = node[seg.idx];
    } else {
      // MergeKey: find the array item whose key === value.
      if (!Array.isArray(node)) return undefined;
      const [k, v] = Object.entries(seg)[0];
      const found = (node as Record<string, unknown>[]).find(
        (item) =>
          item !== null &&
          typeof item === "object" &&
          String(item[k]) === v,
      );
      if (!found) return undefined;
      node = found;
    }
  }
  return node;
}

function stringifyPath(segs: PathSegment[]): string {
  // Mirror of YamlEditor.opPathToString — kept local to avoid a
  // cross-module dependency for one tiny helper.
  const parts: string[] = [];
  for (const seg of segs) {
    if (typeof seg === "string") {
      parts.push(seg);
    } else if (isIndexKey(seg)) {
      parts.push(`[${seg.idx}]`);
    } else {
      const [k, v] = Object.entries(seg)[0];
      parts.push(`[${k}=${v}]`);
    }
  }
  return parts.join(".").replace(/\.\[/g, "[");
}

function isIndexKey(seg: PathSegment): seg is IndexKey {
  return typeof seg !== "string" && "idx" in (seg as object);
}

function assertManagedFieldsPresent(
  mf: ManagedFieldsEntry[] | null | undefined,
): asserts mf is ManagedFieldsEntry[] {
  if (mf === null || mf === undefined) {
    throw new ManagedFieldsUnavailableError();
  }
}
