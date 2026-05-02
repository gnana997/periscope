// yamlPatch — minimal SSA patch generation.
//
// The SPA editor produces a "fully-specified intent" body for server-
// side apply that contains *only* the fields the user changed plus the
// resource's identity stub (apiVersion / kind / metadata.name / namespace).
// Sending only changed fields means periscope-spa claims ownership of
// only those fields — eliminating spurious 409 conflicts on fields like
// HPA-managed replicas or GitOps-managed image tags that the user never
// touched.
//
// Public surface:
//   computeOps(before, after)       — diff two YAML documents into Op[]
//   buildMinimalSSA(ops, identity)  — assemble Op[] back into the smallest
//                                     well-formed SSA YAML payload
//   parseOrThrow(yaml)              — parse + reject multi-doc input
//
// All functions are pure. The state machine in YamlEditor (PR4) stores
// the pristine snapshot and the current buffer; this module is the
// stateless transformation between them.

import {
  parseAllDocuments,

  stringify,
  type Document,
  type Pair,
  type ParsedNode,
  isMap,
  isSeq,
  isScalar,
} from "yaml";

/* ============================================================
   Types
   ============================================================ */

/**
 * A path segment is either a map key (string) or an array merge-key
 * locator (object with one entry, e.g. `{ name: "nginx" }`). Positional
 * array indices (`{ idx: 0 }`) are emitted only as a fallback for
 * arrays without a known SSA merge key.
 */
export type MergeKey = { [key: string]: string };
export type IndexKey = { idx: number };
export type PathSegment = string | MergeKey | IndexKey;

export type Op =
  | { op: "replace"; path: PathSegment[]; value: unknown }
  | { op: "add";     path: PathSegment[]; value: unknown }
  | { op: "remove";  path: PathSegment[] };

export interface Identity {
  apiVersion: string;
  kind: string;
  name: string;
  namespace?: string;
}

export class MultiDocumentError extends Error {
  constructor(count: number) {
    super(
      `multi-document YAML not supported (found ${count} documents). ` +
      `The inline editor edits one resource at a time.`,
    );
    this.name = "MultiDocumentError";
  }
}

export class YamlParseError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "YamlParseError";
  }
}

/* ============================================================
   Disallowed paths — never sent in an SSA payload.
   Mirrors internal/k8s/apply.go:91 (disallowedMetadataPaths).
   ============================================================ */
const DISALLOWED_AT_METADATA = new Set([
  "uid",
  "creationTimestamp",
  "generation",
  "resourceVersion",
  "managedFields",
  "deletionTimestamp",
  "deletionGracePeriodSeconds",
  "selfLink",
]);

/**
 * Server-managed fields under metadata that we strip *before* diffing.
 * These appear in the YAML the apiserver returns; the user's edited
 * buffer also has them (it's loaded from the same source). Stripping
 * keeps them out of `before` and `after` entirely so they never
 * generate phantom Ops.
 *
 * `status` is also stripped — it's a server projection, never input.
 */
function stripServerManaged(obj: unknown): unknown {
  if (!isPlainObject(obj)) return obj;
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(obj)) {
    if (k === "status") continue;
    if (k === "metadata" && isPlainObject(v)) {
      const meta: Record<string, unknown> = {};
      for (const [mk, mv] of Object.entries(v)) {
        if (DISALLOWED_AT_METADATA.has(mk)) continue;
        meta[mk] = mv;
      }
      out[k] = meta;
      continue;
    }
    out[k] = v;
  }
  return out;
}

/* ============================================================
   SSA merge-key registry.
   ============================================================
   Built-in K8s arrays use field-keyed merging. Path is matched by
   prefix walk: any path ending in one of these keys uses the
   corresponding merge key. CRDs that don't appear here fall back
   to whole-array replace, which is safe (the apiserver treats
   unknown arrays as atomic lists).
*/
const MERGE_KEYS: Record<string, string> = {
  // pod spec
  "spec.containers": "name",
  "spec.initContainers": "name",
  "spec.ephemeralContainers": "name",
  "spec.volumes": "name",
  "spec.tolerations": "key",
  "spec.imagePullSecrets": "name",
  "spec.hostAliases": "ip",
  // container-level (deeper paths)
  "containers.env": "name",
  "containers.envFrom": "configMapRef.name", // approximate; envFrom items mix configMap/secret
  "containers.volumeMounts": "mountPath",
  "containers.volumeDevices": "devicePath",
  // PodTemplateSpec wraps another spec so paths under .template.spec.containers
  // are matched the same way as top-level spec.containers.
};

/**
 * mergeKeyForArrayAt returns the merge-key field name for the array
 * located at `path`, or null when the array has no known merge key.
 *
 * Path-suffix matching: we look up the last two segments
 * (e.g. `containers.ports` for spec.template.spec.containers[name=nginx].ports)
 * which lets one entry handle nested usage without enumerating every
 * possible parent prefix.
 */
function mergeKeyForArrayAt(path: PathSegment[]): string | null {
  // Reduce the path to plain string keys, dropping merge-key + index
  // segments. Then check the last two and the full normalized prefix.
  const stringPath = path.filter((s): s is string => typeof s === "string");
  // Try exact full-path match first (most specific)
  const full = stringPath.join(".");
  if (full in MERGE_KEYS) return MERGE_KEYS[full];
  // Then try last two segments (handles spec.containers, containers.env, etc.)
  if (stringPath.length >= 2) {
    const tail = stringPath.slice(-2).join(".");
    if (tail in MERGE_KEYS) return MERGE_KEYS[tail];
  }
  return null;
}

/* ============================================================
   parseOrThrow — single-document gate
   ============================================================ */
export function parseOrThrow(yaml: string): { doc: Document.Parsed<ParsedNode>; obj: unknown } {
  const docs = parseAllDocuments(yaml);
  // Filter empty documents. eemeli/yaml represents the empty trailing doc
  // produced by `foo: 1\n---\n` as a Document whose toJS() is null, so
  // testing the JS projection is the most reliable empty-check.
  const nonEmpty = docs.filter((d) => {
    const js = d.toJS({ mapAsMap: false });
    return js !== null && js !== undefined;
  });
  if (nonEmpty.length > 1) {
    throw new MultiDocumentError(nonEmpty.length);
  }
  if (nonEmpty.length === 0) {
    throw new YamlParseError("yaml: empty document");
  }
  const doc = nonEmpty[0] as Document.Parsed<ParsedNode>;
  if (doc.errors.length > 0) {
    throw new YamlParseError(doc.errors[0].message);
  }
  const obj = doc.toJS({ mapAsMap: false });
  return { doc, obj };
}

/* ============================================================
   computeOps — diff two YAML strings into Op[]
   ============================================================ */
export function computeOps(before: string, after: string): Op[] {
  const beforeJS = stripServerManaged(parseOrThrow(before).obj);
  const afterJS = stripServerManaged(parseOrThrow(after).obj);
  const ops: Op[] = [];
  diffNode(beforeJS, afterJS, [], ops);
  return ops;
}

function diffNode(before: unknown, after: unknown, path: PathSegment[], ops: Op[]): void {
  // Normalise undefined → null for the "value missing" comparison
  // (YAML doesn't distinguish them; JSON does).
  if (deepEqual(before, after)) return;

  if (before === undefined) {
    ops.push({ op: "add", path: [...path], value: after });
    return;
  }
  if (after === undefined) {
    ops.push({ op: "remove", path: [...path] });
    return;
  }

  if (isPlainObject(before) && isPlainObject(after)) {
    diffMap(before, after, path, ops);
    return;
  }
  if (Array.isArray(before) && Array.isArray(after)) {
    diffArray(before, after, path, ops);
    return;
  }
  // Type changed (e.g. scalar → map). Replace at this path.
  ops.push({ op: "replace", path: [...path], value: after });
}

function diffMap(
  before: Record<string, unknown>,
  after: Record<string, unknown>,
  path: PathSegment[],
  ops: Op[],
): void {
  const allKeys = new Set([...Object.keys(before), ...Object.keys(after)]);
  for (const k of allKeys) {
    const bv = before[k];
    const av = after[k];
    if (deepEqual(bv, av)) continue;
    const childPath = [...path, k];
    if (bv === undefined) {
      ops.push({ op: "add", path: childPath, value: av });
    } else if (av === undefined) {
      ops.push({ op: "remove", path: childPath });
    } else if (isPlainObject(bv) && isPlainObject(av)) {
      diffMap(bv, av, childPath, ops);
    } else if (Array.isArray(bv) && Array.isArray(av)) {
      diffArray(bv, av, childPath, ops);
    } else {
      ops.push({ op: "replace", path: childPath, value: av });
    }
  }
}

function diffArray(
  before: unknown[],
  after: unknown[],
  path: PathSegment[],
  ops: Op[],
): void {
  const mergeKey = mergeKeyForArrayAt(path);
  if (!mergeKey) {
    // No known merge key — emit a whole-array replace.
    ops.push({ op: "replace", path: [...path], value: after });
    return;
  }
  // Index by merge key.
  const beforeByKey = new Map<string, Record<string, unknown>>();
  const afterByKey = new Map<string, Record<string, unknown>>();
  for (const item of before) {
    if (isPlainObject(item) && typeof item[mergeKey] === "string") {
      beforeByKey.set(String(item[mergeKey]), item);
    }
  }
  for (const item of after) {
    if (isPlainObject(item) && typeof item[mergeKey] === "string") {
      afterByKey.set(String(item[mergeKey]), item);
    }
  }
  // If either side has items missing the merge key, fall back to atomic replace.
  if (beforeByKey.size !== before.length || afterByKey.size !== after.length) {
    ops.push({ op: "replace", path: [...path], value: after });
    return;
  }
  // Diff per item.
  const allKeys = new Set([...beforeByKey.keys(), ...afterByKey.keys()]);
  for (const k of allKeys) {
    const bItem = beforeByKey.get(k);
    const aItem = afterByKey.get(k);
    const childPath: PathSegment[] = [...path, { [mergeKey]: k }];
    if (bItem === undefined) {
      ops.push({ op: "add", path: childPath, value: aItem });
    } else if (aItem === undefined) {
      ops.push({ op: "remove", path: childPath });
    } else {
      diffMap(bItem, aItem, childPath, ops);
    }
  }
}

/* ============================================================
   buildMinimalSSA — assemble Ops back into a YAML payload
   ============================================================
   The output always contains apiVersion + kind + metadata.{name, namespace?}.
   The rest of the tree is built up lazily from the ops' paths so only
   the fields the user actually changed appear.
*/
export function buildMinimalSSA(ops: Op[], identity: Identity): string {
  const root: Record<string, unknown> = {
    apiVersion: identity.apiVersion,
    kind: identity.kind,
    metadata: {
      name: identity.name,
      ...(identity.namespace ? { namespace: identity.namespace } : {}),
    },
  };

  for (const op of ops) {
    applyOpToTree(root, op);
  }

  return stringify(root, {
    lineWidth: 0,
    defaultStringType: "PLAIN",
    nullStr: "~",
  });
}

function applyOpToTree(root: Record<string, unknown>, op: Op): void {
  if (op.path.length === 0) {
    // No-op: trying to set the root itself isn't expressible without
    // dropping identity. Skip.
    return;
  }
  // Walk to the parent of the leaf, creating intermediates as needed.
  // The next segment determines whether to create the current step
  // as an object (string next) or an array (merge-key/index next).
  let node: unknown = root;
  for (let i = 0; i < op.path.length - 1; i++) {
    const seg = op.path[i];
    const nextSeg = op.path[i + 1];
    node = stepInto(node, seg, nextSeg);
  }
  const last = op.path[op.path.length - 1];
  if (op.op === "remove") {
    setLeaf(node, last, REMOVE_SENTINEL);
    return;
  }
  setLeaf(node, last, op.value);
}

const REMOVE_SENTINEL = Symbol("yamlPatch.remove");

function stepInto(node: unknown, seg: PathSegment, nextSeg: PathSegment | undefined): unknown {
  if (typeof seg === "string") {
    if (!isPlainObject(node)) {
      throw new Error(`stepInto: expected map at "${seg}" but got ${typeof node}`);
    }
    if (!(seg in node)) {
      // Look ahead: if the next segment locates an array element
      // (merge-key or positional), this slot must be an array.
      const isArrayChild = nextSeg !== undefined && typeof nextSeg !== "string";
      node[seg] = isArrayChild ? [] : {};
    }
    return node[seg];
  }
  // Merge-key segment: parent must be an array. Find or create the
  // item whose merge-key field matches this segment's value.
  if (!Array.isArray(node)) {
    throw new Error(
      `stepInto: expected array for merge-key segment, got ${typeof node}`,
    );
  }
  const [keyName, keyValue] = entryOf(seg);
  let item = (node as Record<string, unknown>[]).find(
    (x) => isPlainObject(x) && String(x[keyName]) === keyValue,
  );
  if (!item) {
    item = { [keyName]: keyValue };
    (node as unknown[]).push(item);
  }
  return item;
}

function setLeaf(node: unknown, seg: PathSegment, value: unknown | typeof REMOVE_SENTINEL): void {
  if (typeof seg === "string") {
    if (!isPlainObject(node)) {
      throw new Error(`setLeaf: expected map for "${seg}" but got ${typeof node}`);
    }
    if (value === REMOVE_SENTINEL) {
      // SSA: setting a managed field to null tells the apiserver to drop
      // it. Marker form for downstream stringify.
      node[seg] = null;
    } else {
      node[seg] = value;
    }
    return;
  }
  // Merge-key leaf is unusual but possible (e.g. removing a whole
  // array item). Ensure the parent has the keyed item, then drop it.
  if (!Array.isArray(node)) {
    throw new Error(`setLeaf: expected array for merge-key seg, got ${typeof node}`);
  }
  const [keyName, keyValue] = entryOf(seg);
  const idx = (node as Record<string, unknown>[]).findIndex(
    (x) => isPlainObject(x) && x[keyName] === keyValue,
  );
  if (value === REMOVE_SENTINEL) {
    // SSA atomic-list removal isn't expressible by absence — but for
    // map-keyed lists, omitting the item from the apply means we don't
    // claim ownership; combined with prior ownership, it's removed. We
    // model this as "don't include the item." If the array doesn't
    // already have it, no-op.
    if (idx >= 0) {
      (node as unknown[]).splice(idx, 1);
    }
    return;
  }
  if (idx >= 0) {
    (node as unknown[])[idx] = { ...(node as Record<string, unknown>[])[idx], ...(value as object) };
  } else {
    (node as unknown[]).push({ [keyName]: keyValue, ...(value as object) });
  }
}

function entryOf(seg: MergeKey | IndexKey): [string, string] {
  if ("idx" in seg) return ["__idx", String(seg.idx)];
  const [k, v] = Object.entries(seg)[0];
  return [k, v];
}

/* ============================================================
   Helpers
   ============================================================ */
function isPlainObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === "object" && !Array.isArray(v);
}

function deepEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (a == null || b == null) return a === b;
  if (typeof a !== typeof b) return false;
  if (Array.isArray(a)) {
    if (!Array.isArray(b) || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (!deepEqual(a[i], b[i])) return false;
    }
    return true;
  }
  if (typeof a === "object") {
    const ak = Object.keys(a as object);
    const bk = Object.keys(b as object);
    if (ak.length !== bk.length) return false;
    for (const k of ak) {
      if (!deepEqual(
        (a as Record<string, unknown>)[k],
        (b as Record<string, unknown>)[k],
      )) return false;
    }
    return true;
  }
  return false;
}

// Re-export type guards for downstream consumers (PR4 editor uses them
// for paint hints in the patch preview drawer).
export { isMap, isSeq, isScalar };
export type { Pair };
