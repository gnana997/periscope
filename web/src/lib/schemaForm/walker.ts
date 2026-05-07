// schemaForm/walker.ts — JSON Schema → FieldDescriptor[] walker.
//
// Originally lived in helmSchema.ts; lifted here and given a
// WalkOptions arg so the same walker drives Helm's chart values
// (no options) AND K8s OpenAPI v3 (resolveRef + allowKvMap +
// allowArrayOfObjects). Default options reproduce Helm v1.1
// behavior so the existing 18 helmSchema vitest cases pass
// unchanged.

import type { FieldDescriptor, JSONSchema } from "./types";

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
  /** Dotted-path strings (e.g. `metadata.name`) the walker should
   *  flag with `editable: "create-only"`. Surfaced by the renderer
   *  as read-only inputs in edit mode. */
  createOnlyPaths?: string[];
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

  // Reject the "we don't render this" cases up front.
  if (
    schema.allOf !== undefined ||
    schema.anyOf !== undefined ||
    schema.oneOf !== undefined ||
    schema.patternProperties !== undefined
  ) {
    return {
      ...base,
      type: "unsupported",
      unsupportedReason:
        "schema uses allOf / anyOf / oneOf / patternProperties — edit in YAML mode",
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
  if (!schema || typeof schema !== "object" || schema.$ref === undefined) return schema;
  if (!options.resolveRef) return undefined;
  const ref = schema.$ref;
  if (seen.has(ref)) return undefined;
  const resolved = options.resolveRef(ref);
  if (!resolved) return undefined;
  // Add to seen for the duration of this branch's walk.
  seen.add(ref);
  // If the resolved schema is itself a ref (chained), recurse.
  const final = derefIfNeeded(resolved, options, seen);
  // Note: we deliberately do NOT remove the ref from `seen` after
  // walking. Sibling fields in the same sub-tree never legitimately
  // resolve to the same recursive type without going through a
  // different parent first; allowing re-visits would let huge K8s
  // schemas blow up exponentially for no rendering benefit.
  return final;
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
