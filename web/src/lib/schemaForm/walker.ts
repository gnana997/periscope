// schemaForm/walker.ts — JSON Schema → FieldDescriptor[] walker.
//
// Originally lived in helmSchema.ts; lifted here and given a
// WalkOptions arg so the same walker drives Helm's chart values
// (no options) AND K8s OpenAPI v3 (resolveRef + allowKvMap +
// allowArrayOfObjects). Default options reproduce Helm v1.1
// behavior so the existing 18 helmSchema vitest cases pass
// unchanged.

import { mergeAllOf } from "./allOfMerger";
import type { DiscriminatorBranch, FieldDescriptor, JSONSchema } from "./types";

export interface WalkOptions {
  /** Resolve a `$ref` string (e.g. "#/components/schemas/...") to a
   *  schema fragment. When absent, refs render as unsupported. */
  resolveRef?: (ref: string) => JSONSchema | undefined;
  /** Emit `kv-map` descriptors for objects whose `additionalProperties`
   *  is a primitive schema (and whose `properties` is empty/missing).
   *  Used by ConfigMap.data, Secret.data, Service.selector, and every
   *  metadata.labels / metadata.annotations under K8s. */
  allowKvMap?: boolean;
  /** Emit `array-of-objects` descriptors instead of the unsupported
   *  fallback. The widget renders a table of fieldsets per row. */
  allowArrayOfObjects?: boolean;
  /** Emit `discriminator` descriptors for `oneOf` shapes. The
   *  renderer shows a branch picker that lets the operator choose
   *  between N sub-schemas. K8s + EKS add-ons + cert-manager-style
   *  CRDs benefit; Helm chart authors typically don't expect a
   *  discriminator UI, so the Helm path leaves this off. */
  allowOneOfDiscriminator?: boolean;
  /** Dotted-path strings (e.g. `metadata.name`) the walker should
   *  flag with `editable: "create-only"`. Surfaced by the renderer
   *  as read-only inputs in edit mode. */
  createOnlyPaths?: string[];
  /** Hint table for schemas that encode polymorphism via sibling
   *  properties instead of JSON Schema `oneOf`. K8s does this for
   *  Probe (httpGet/tcpSocket/exec/grpc), Volume (~30 volume
   *  types), EnvVarSource (fieldRef/configMapKeyRef/secretKeyRef/
   *  resourceFieldRef), LifecycleHandler, and others — the
   *  apiserver enforces "exactly one" but the schema doesn't
   *  declare it.
   *
   *  Map key is the schema's `$ref` string (matched against the
   *  property's primary ref before deref — covers both `{$ref: X}`
   *  and `{allOf: [{$ref: X}]}` envelopes). When matched, the
   *  walker emits a Shape B-style discriminator over `branches[]`
   *  with the remaining properties as `sharedChildren`. */
  discriminatorHints?: Map<string, DiscriminatorHint>;
}

/** Sibling-property hint for K8s-style polymorphism. */
export interface DiscriminatorHint {
  /** Property keys that are mutually exclusive — exactly one of
   *  them should be set on the value at a time. The walker emits
   *  a discriminator branch per key. */
  branches: string[];
  /** Optional human label per branch (defaults to the key). */
  labels?: Record<string, string>;
}

/** Walk schema, emit a tree of field descriptors. */
export function buildFieldDescriptors(
  schema: JSONSchema,
  options: WalkOptions = {},
): FieldDescriptor[] {
  if (!schema || typeof schema !== "object") return [];
  const root = derefIfNeeded(schema, options, new Set());
  if (!root) return [];
  if (root.type !== "object" && !root.properties) {
    // Some schemas omit the top-level type. If there's a properties
    // bag, treat it as an object; otherwise return empty (the form
    // can't render a non-object root).
    if (!root.properties) return [];
  }
  return walkObject(root, [], options, new Set());
}

/**
 * True when the schema contains at least one required field that the
 * form can't render (array of objects, $ref, allOf/anyOf/oneOf,
 * patternProperties — anything that ends up as `type: "unsupported"`).
 *
 * Editors use this to pick an initial mode: if there's a required-
 * but-unrenderable field, defaulting to form mode would prevent the
 * operator from filling in a value the install / apply will reject.
 * Default to YAML mode in that case so they can edit everything in
 * one place.
 */
export function hasRequiredUnsupportedField(
  schema: JSONSchema,
  options: WalkOptions = {},
): boolean {
  return buildFieldDescriptors(schema, options).some(walkRequiredUnsupported);
}

function walkRequiredUnsupported(d: FieldDescriptor): boolean {
  if (d.type === "unsupported" && d.required) return true;
  if (d.type === "object" && d.children) {
    return d.children.some(walkRequiredUnsupported);
  }
  return false;
}

function walkObject(
  schema: JSONSchema,
  path: string[],
  options: WalkOptions,
  seen: Set<string>,
): FieldDescriptor[] {
  const props = schema.properties ?? {};
  const required = new Set(schema.required ?? []);
  const out: FieldDescriptor[] = [];
  for (const key of Object.keys(props)) {
    const child = props[key];
    out.push(walkField(child, [...path, key], required.has(key), options, seen));
  }
  return out;
}

function walkField(
  raw: JSONSchema,
  path: string[],
  required: boolean,
  options: WalkOptions,
  parentSeen: Set<string>,
): FieldDescriptor {
  // Each field walks under its own copy of `seen`. Cycle detection
  // is per-branch — siblings sharing a $ref must not mutate each
  // other's resolution state, or `Outer { a: Inner, b: Inner }`
  // would emit `b` as recursive even though it isn't.
  const seen = new Set(parentSeen);

  // Hint match is keyed off the PRE-deref shape so we can recognise
  // K8s envelopes like `{allOf:[{$ref: Probe}]}` before they collapse
  // into a flat object that's structurally indistinguishable from
  // any other K8s sub-resource. Look up the hint up front; act on it
  // after we've deref'd into the resolved schema (we still need the
  // underlying properties to build the branch sub-forms).
  const primaryRef = extractPrimaryRef(raw);
  const hint =
    primaryRef !== undefined ? options.discriminatorHints?.get(primaryRef) : undefined;

  // Resolve a $ref before reading the rest of the fields so the
  // walker can see the underlying type/properties.
  const schema = derefIfNeeded(raw, options, seen);
  if (!schema) {
    return unsupportedField(raw, path, required, "recursive $ref — edit in YAML mode");
  }

  const label = (schema.title as string) || path[path.length - 1] || "";
  const createOnly = options.createOnlyPaths?.includes(path.join(".")) ?? false;
  const base: Pick<
    FieldDescriptor,
    "path" | "label" | "description" | "required" | "default" | "editable"
  > = {
    path,
    label,
    description: schema.description,
    required,
    default: schema.default,
    ...(createOnly ? { editable: "create-only" as const } : {}),
  };

  // Hinted discriminator: the host schema told us this type is
  // really a sibling-encoded oneOf. Build a Shape B-style picker
  // over `hint.branches`; remaining properties become sharedChildren
  // (rendered alongside the picker, preserved across branch
  // switches). This is THE bridge that makes K8s Probe / Volume /
  // EnvVarSource / LifecycleHandler render as proper discriminators.
  if (hint && schema.properties) {
    const built = buildHintedDiscriminator(schema, hint, options, seen);
    if (built) {
      return { ...base, type: "discriminator", ...built };
    }
    // Fall through to standard logic when hint can't be applied
    // (e.g. none of the branch keys present in the resolved schema).
  }

  // oneOf detection — emit a discriminator descriptor instead of
  // unsupported. Two structural shapes:
  //
  //   Case A: property-level oneOf (whole-value picker)
  //     { oneOf: [Schema1, Schema2, ...] }
  //   Case B: object-level oneOf with required-key branches
  //     { type: object, properties: {...}, oneOf: [
  //         {required: [foo]}, {required: [bar]}, ...
  //       ] }
  //
  // Case B is the cert-manager Issuer style — common in K8s CRDs.
  // The `discriminatorKey` lets the renderer detect the active
  // branch via key-presence (fast path; no ajv-validate needed).
  if (
    options.allowOneOfDiscriminator &&
    Array.isArray(schema.oneOf) &&
    schema.oneOf.length > 0
  ) {
    const branches = buildDiscriminatorBranches(schema, options, seen);
    if (branches && branches.length > 0) {
      return { ...base, type: "discriminator", branches };
    }
    // Fall through to unsupported when branch building fails (mixed
    // shapes, unresolvable refs, etc.) so the operator gets the
    // YAML-mode hint rather than a broken picker.
  }

  // Reject the remaining "we don't render this" cases. allOf is in
  // this list because the merger leaves allOf in place when it
  // can't merge cleanly (type conflict across entries) — in that
  // case we surface as unsupported rather than render an empty
  // object that hides the failure.
  if (
    schema.anyOf !== undefined ||
    schema.oneOf !== undefined ||
    schema.allOf !== undefined ||
    schema.patternProperties !== undefined
  ) {
    return {
      ...base,
      type: "unsupported",
      unsupportedReason:
        "schema uses anyOf / oneOf / allOf / patternProperties — edit in YAML mode",
    };
  }
  // A $ref that didn't resolve at the entry above means the resolver
  // returned undefined or wasn't supplied. Surface kindly.
  if (schema.$ref !== undefined) {
    return {
      ...base,
      type: "unsupported",
      unsupportedReason: "schema uses $ref — edit in YAML mode",
    };
  }

  const type = normalizeType(schema.type);

  switch (type) {
    case "string":
      return {
        ...base,
        type: "string",
        enum: schema.enum,
        pattern: schema.pattern,
        format: schema.format,
        minLength: schema.minLength,
        maxLength: schema.maxLength,
      };
    case "number":
    case "integer":
      return {
        ...base,
        type,
        enum: schema.enum,
        minimum: schema.minimum,
        maximum: schema.maximum,
      };
    case "boolean":
      return { ...base, type: "boolean", enum: schema.enum };
    case "object": {
      // K8s schemas frequently model maps as `type: object` with
      // `additionalProperties: { type: string }` and no `properties`.
      // When the caller opts in, render as a kv-map widget.
      if (
        options.allowKvMap &&
        (!schema.properties || Object.keys(schema.properties).length === 0) &&
        isPrimitiveAdditionalProperties(schema.additionalProperties)
      ) {
        const ap = schema.additionalProperties as JSONSchema;
        const apType = normalizeType(ap.type);
        const kvValueType =
          apType === "string" || apType === "number" || apType === "integer" || apType === "boolean"
            ? apType
            : "string";
        return { ...base, type: "kv-map", kvValueType };
      }
      return {
        ...base,
        type: "object",
        children: walkObject(schema, path, options, seen),
      };
    }
    case "array": {
      const items = schema.items;
      if (!items || typeof items !== "object") {
        return { ...base, type: "unsupported", unsupportedReason: "array without items schema" };
      }
      const resolvedItems = derefIfNeeded(items, options, seen) ?? items;
      const itemType = normalizeType(resolvedItems.type);
      if (itemType === "string" || itemType === "number" || itemType === "integer" || itemType === "boolean") {
        return { ...base, type: "array-of-primitives", itemType };
      }
      if (options.allowArrayOfObjects && itemType === "object") {
        // Children paths are RELATIVE to the row item, not absolute
        // from the form root. The array-of-objects widget composes
        // the absolute path at render time.
        return {
          ...base,
          type: "array-of-objects",
          children: walkObject(resolvedItems, [], options, seen),
        };
      }
      return {
        ...base,
        type: "unsupported",
        unsupportedReason: "array of objects — edit in YAML mode",
      };
    }
    default:
      return {
        ...base,
        type: "unsupported",
        unsupportedReason: schema.type ? `unsupported type ${String(schema.type)}` : "type not specified",
      };
  }
}

function unsupportedField(
  raw: JSONSchema,
  path: string[],
  required: boolean,
  reason: string,
): FieldDescriptor {
  return {
    path,
    label: path[path.length - 1] || "",
    description: typeof raw.description === "string" ? raw.description : undefined,
    required,
    type: "unsupported",
    unsupportedReason: reason,
  };
}

// Resolve a $ref via options.resolveRef. Cycle-safe: if the same
// ref is already on the resolution stack, returns undefined so the
// caller surfaces it as unsupported and breaks the loop. K8s schemas
// like JSONSchemaProps recurse into themselves; without this guard
// the walker stack-overflows.
function derefIfNeeded(
  schema: JSONSchema,
  options: WalkOptions,
  seen: Set<string>,
): JSONSchema | undefined {
  if (!schema || typeof schema !== "object") return schema;
  // Resolve $ref first if present.
  let current: JSONSchema | undefined = schema;
  if (current.$ref !== undefined) {
    if (!options.resolveRef) return undefined;
    const ref = current.$ref;
    if (seen.has(ref)) return undefined;
    const resolved = options.resolveRef(ref);
    if (!resolved) return undefined;
    seen.add(ref);
    current = derefIfNeeded(resolved, options, seen);
    if (!current) return undefined;
  }
  // Then merge allOf (if any). The merger flattens layer by layer
  // — nested allOf inside allOf entries get flattened recursively
  // via the merger's own resolveEntry call. Result is a single
  // unified schema the rest of the walker can read flat.
  if (Array.isArray(current.allOf) && current.allOf.length > 0) {
    current = mergeAllOf(current, { resolveRef: options.resolveRef, seen });
  }
  // Note: we deliberately do NOT remove refs from `seen` after
  // walking. Sibling fields in the same sub-tree never legitimately
  // resolve to the same recursive type without going through a
  // different parent first; allowing re-visits would let huge K8s
  // schemas blow up exponentially for no rendering benefit.
  return current;
}

function isPrimitiveAdditionalProperties(ap: unknown): boolean {
  if (!ap || typeof ap !== "object") return false;
  const t = normalizeType((ap as JSONSchema).type);
  return t === "string" || t === "number" || t === "integer" || t === "boolean";
}

function normalizeType(t: unknown): string {
  if (typeof t === "string") return t;
  if (Array.isArray(t)) {
    // JSON Schema allows ["string", "null"] for nullable. Pick the
    // first non-null type. If only null, fall through to unsupported.
    for (const candidate of t) {
      if (typeof candidate === "string" && candidate !== "null") return candidate;
    }
  }
  return "";
}

// ── Discriminator branch building ────────────────────────────────
//
// `oneOf` shows up in two structurally different shapes; this
// helper detects which and builds the branches array the
// renderer's DiscriminatorInput consumes.
//
// Shape A — property-level oneOf (whole-value picker):
//   { oneOf: [SchemaA, SchemaB, SchemaC] }
// Each entry is a complete sub-schema; the operator picks one and
// the value of the field IS one of those shapes.
//
// Shape B — object-level oneOf with required-key branches:
//   { type: object, properties: {a:{...}, b:{...}, c:{...}},
//     oneOf: [{required: [a]}, {required: [b]}, {required: [c]}] }
// The object has BASE properties + a oneOf saying "exactly one of
// these property names must be set." Each branch is the property
// schema corresponding to its single required key.

function buildDiscriminatorBranches(
  schema: JSONSchema,
  options: WalkOptions,
  parentSeen: Set<string>,
): DiscriminatorBranch[] | undefined {
  if (!Array.isArray(schema.oneOf) || schema.oneOf.length === 0) return undefined;

  // Shape B: every branch is just `{required: [SINGLE_KEY]}` and
  // the parent has properties whose names match each branch's key.
  // Cert-manager Issuer / many CRDs use this style. Detect first
  // because Shape A's "branch is a full schema" check is more
  // permissive and would also match Shape B branches structurally
  // (each branch IS a schema, just a tiny one).
  if (schema.properties && schema.oneOf.every(isSingleRequiredKeyBranch)) {
    const branches: DiscriminatorBranch[] = [];
    for (const branch of schema.oneOf) {
      const key = (branch.required as string[])[0];
      const subSchema = schema.properties[key];
      if (!subSchema) return undefined; // schema author error — bail
      // Walk the sub-schema with path prefixed by the discriminator
      // key so descendant setAtPath calls write to value[key][...].
      const seen = new Set(parentSeen);
      const descriptors = walkBranchSchema(subSchema, [key], options, seen);
      branches.push({
        label: branchLabelFor(subSchema, key),
        description: subSchema.description,
        schema: subSchema,
        discriminatorKey: key,
        descriptors,
      });
    }
    return branches;
  }

  // Shape A: each entry is a full sub-schema (not just a required
  // marker). Resolve any $refs and pre-walk the entry so the
  // renderer can mount the chosen branch without touching the walker.
  const branches: DiscriminatorBranch[] = [];
  for (let i = 0; i < schema.oneOf.length; i++) {
    const entry = schema.oneOf[i];
    const seen = new Set(parentSeen);
    const resolved = derefIfNeeded(entry, options, seen);
    if (!resolved) {
      // Unresolvable ref or recursive — bail rather than render a
      // partial picker that drops branches silently.
      return undefined;
    }
    // Walk with empty base path — Shape A's value IS the branch
    // value (no extra wrapping key), so descendant paths are
    // relative to the discriminator's value directly.
    const descriptors = walkBranchSchema(resolved, [], options, seen);
    branches.push({
      label: branchLabelFor(resolved, undefined, i),
      description: resolved.description,
      schema: resolved,
      descriptors,
    });
  }
  return branches;
}

// Walk a branch schema starting at `basePath`. Object schemas
// produce a property-list of descriptors (each rooted at
// basePath + key). Primitive schemas produce a single descriptor
// at basePath itself — covers cases like Service.spec.ports[].
// targetPort which is `oneOf: [{type:string},{type:integer}]` and
// each branch is a primitive.
function walkBranchSchema(
  schema: JSONSchema,
  basePath: string[],
  options: WalkOptions,
  seen: Set<string>,
): FieldDescriptor[] {
  if (!schema || typeof schema !== "object") return [];
  if (schema.type === "object" || schema.properties) {
    const props = schema.properties ?? {};
    const required = new Set(schema.required ?? []);
    const out: FieldDescriptor[] = [];
    for (const key of Object.keys(props)) {
      out.push(walkField(props[key], [...basePath, key], required.has(key), options, seen));
    }
    return out;
  }
  // Primitive / array / unsupported branch — emit a single descriptor.
  return [walkField(schema, basePath, false, options, seen)];
}

// ── Hinted discriminator (K8s sibling-encoded polymorphism) ───────
//
// K8s schemas like Probe, Volume, EnvVarSource encode "exactly one
// of these properties is set" as plain sibling properties — no JSON
// Schema oneOf. The walker can't infer that structurally, so the
// caller passes a hint table keyed by `$ref` declaring which
// properties are mutually-exclusive branches. When we see one,
// we synthesise a Shape B discriminator (per-branch sub-form,
// `discriminatorKey` set on each) and treat the remaining schema
// properties as `sharedChildren` (always rendered, preserved across
// branch switches — Probe's threshold knobs, Volume's `name`, etc.).

/** Pull out the `$ref` string from a property schema, looking
 *  through a single-element `allOf` envelope. Returns undefined
 *  when no primary ref is present. */
function extractPrimaryRef(schema: JSONSchema): string | undefined {
  if (!schema || typeof schema !== "object") return undefined;
  if (typeof schema.$ref === "string") return schema.$ref;
  if (Array.isArray(schema.allOf) && schema.allOf.length === 1) {
    const entry = schema.allOf[0] as JSONSchema | undefined;
    if (entry && typeof entry === "object" && typeof entry.$ref === "string") {
      return entry.$ref;
    }
  }
  return undefined;
}

function buildHintedDiscriminator(
  schema: JSONSchema,
  hint: DiscriminatorHint,
  options: WalkOptions,
  parentSeen: Set<string>,
): { branches: DiscriminatorBranch[]; sharedChildren?: FieldDescriptor[] } | undefined {
  const props = schema.properties ?? {};
  const branchKeys = hint.branches.filter((k) => k in props);
  if (branchKeys.length === 0) return undefined;

  const branches: DiscriminatorBranch[] = [];
  for (const key of branchKeys) {
    const sub = props[key];
    if (!sub) continue;
    const seen = new Set(parentSeen);
    const descriptors = walkBranchSchema(sub, [key], options, seen);
    branches.push({
      label: hint.labels?.[key] ?? branchLabelFor(sub, key),
      description: typeof sub.description === "string" ? sub.description : undefined,
      schema: sub,
      discriminatorKey: key,
      descriptors,
    });
  }
  if (branches.length === 0) return undefined;

  // Everything that wasn't called out as a branch becomes a shared
  // child. Walk each as a normal field rooted relative to the
  // discriminator value (same convention as Shape B descriptors —
  // their setAtPath calls write directly to value[key] without an
  // extra wrapping segment).
  const branchSet = new Set(branchKeys);
  const sharedChildren: FieldDescriptor[] = [];
  const requiredSet = new Set(schema.required ?? []);
  for (const key of Object.keys(props)) {
    if (branchSet.has(key)) continue;
    const childSeen = new Set(parentSeen);
    sharedChildren.push(
      walkField(props[key], [key], requiredSet.has(key), options, childSeen),
    );
  }

  return {
    branches,
    sharedChildren: sharedChildren.length > 0 ? sharedChildren : undefined,
  };
}

function isSingleRequiredKeyBranch(branch: unknown): branch is JSONSchema {
  if (!branch || typeof branch !== "object") return false;
  const b = branch as JSONSchema;
  // The branch is "just a required marker" if it ONLY has `required`
  // (not its own properties / type / etc.) AND that required array
  // has exactly one entry.
  if (!Array.isArray(b.required) || b.required.length !== 1) return false;
  const interestingKeys = Object.keys(b).filter(
    (k) => k !== "required" && k !== "description" && k !== "title",
  );
  return interestingKeys.length === 0;
}

function branchLabelFor(
  sub: JSONSchema,
  key?: string,
  fallbackIdx?: number,
): string {
  // Title beats heuristics. CRD authors who set titles get readable
  // pickers; everyone else falls through.
  if (typeof sub.title === "string" && sub.title) return sub.title;
  // Required-key style: the key IS the label (e.g. "selfSigned" /
  // "ca" / "vault" for cert-manager Issuer.spec).
  if (key) return key;
  // Single-required key as a self-discriminator hint inside Shape A.
  if (Array.isArray(sub.required) && sub.required.length === 1) {
    return sub.required[0];
  }
  // Single-property object — use the property name.
  if (sub.properties) {
    const keys = Object.keys(sub.properties);
    if (keys.length === 1) return keys[0];
    if (keys.length > 0 && keys.length <= 3) return keys.join(" + ");
  }
  // Last resort. Operators editing CRDs with poorly-titled schemas
  // see "option N" — imperfect but better than yaml-only.
  return `option ${(fallbackIdx ?? 0) + 1}`;
}
