// helmSchema.ts — JSON Schema introspection + validation for the
// Helm install dialog's values editor (#74).
//
// Two responsibilities, kept pure (no React imports) so the file
// is easy to vitest:
//
//   1. Walk a JSON Schema and emit FieldDescriptor nodes the form
//      renderer consumes. Recursive for objects + arrays.
//   2. Validate a values object against the schema via ajv, returning
//      a list of typed violations the form attaches to fields.
//
// JSON Schema is a sprawling spec. We support the subset Helm chart
// authors actually use:
//
//   ✓ string / number / integer / boolean / object / array
//   ✓ enum (renders as select)
//   ✓ default / description / title
//   ✓ required (from parent's required[])
//   ✓ pattern / format (passed to ajv for validation)
//   ✓ minimum / maximum / minLength / maxLength
//   ✓ array of primitives (string[], number[]) — tag editor
//   ✓ nested objects (recursive)
//
// Out of scope (chart will fall through to YAML mode if its schema
// uses these — kindly, not silently):
//
//   ✗ $ref / $defs (resolution complexity, rarely used in helm
//     charts since values are flat configuration trees, not models)
//   ✗ allOf / anyOf / oneOf
//   ✗ array of complex objects (operators reach for YAML mode for
//     these — the form ergonomics aren't worth the implementation
//     cost in v1.1; see deferred items in the issue)
//   ✗ patternProperties / additionalProperties shape

import Ajv, { type ErrorObject } from "ajv";

// JSON Schema fragment — narrow type that captures what we read.
// Loose typing on most fields because schema validators tolerate
// Helm-author quirks that strict types would reject.
export interface JSONSchema {
  $schema?: string;
  type?: string | string[];
  title?: string;
  description?: string;
  default?: unknown;
  enum?: unknown[];
  required?: string[];
  properties?: Record<string, JSONSchema>;
  items?: JSONSchema;
  pattern?: string;
  format?: string;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  // Anything else (additionalProperties, $ref, allOf, etc.) is
  // tolerated but ignored.
  [k: string]: unknown;
}

export type FieldType =
  | "string"
  | "number"
  | "integer"
  | "boolean"
  | "object"
  | "array-of-primitives"
  | "unsupported";

export interface FieldDescriptor {
  /** JSON-pointer-style path from root, e.g. ["resources", "limits", "cpu"]. */
  path: string[];
  /** Human label — falls back to the last path segment when title absent. */
  label: string;
  type: FieldType;
  description?: string;
  required: boolean;
  default?: unknown;
  enum?: unknown[];
  /** For type=array-of-primitives: the primitive type of the elements. */
  itemType?: "string" | "number" | "integer" | "boolean";
  /** For type=object: the nested field descriptors (recursive). */
  children?: FieldDescriptor[];
  /** Constraints surfaced for inline validation hints. */
  pattern?: string;
  format?: string;
  minimum?: number;
  maximum?: number;
  minLength?: number;
  maxLength?: number;
  /** For type=unsupported, why we couldn't render this field as a
   *  form. Surfaced as a "edit in YAML mode" hint. */
  unsupportedReason?: string;
}

/** Walk schema, emit a tree of field descriptors. */
export function buildFieldDescriptors(schema: JSONSchema): FieldDescriptor[] {
  if (!schema || typeof schema !== "object") return [];
  if (schema.type !== "object" && !schema.properties) {
    // Some schemas omit the top-level type. If there's a properties
    // bag, treat it as an object; otherwise return empty (the form
    // can't render a non-object root; the dialog falls through to
    // YAML mode).
    if (!schema.properties) return [];
  }
  return walkObject(schema, []);
}

/**
 * True when the schema contains at least one required field that the
 * form can't render (array of objects, $ref, allOf/anyOf/oneOf,
 * patternProperties — anything that ends up as `type: "unsupported"`).
 *
 * The editor uses this to pick the initial mode: if there's a
 * required-but-unrenderable field, defaulting to form mode would
 * prevent the operator from filling in a value the install will
 * reject. Default to YAML mode in that case so they can edit
 * everything in one place.
 */
export function hasRequiredUnsupportedField(schema: JSONSchema): boolean {
  return buildFieldDescriptors(schema).some(walkRequiredUnsupported);
}

function walkRequiredUnsupported(d: FieldDescriptor): boolean {
  if (d.type === "unsupported" && d.required) return true;
  if (d.type === "object" && d.children) {
    return d.children.some(walkRequiredUnsupported);
  }
  return false;
}

function walkObject(schema: JSONSchema, path: string[]): FieldDescriptor[] {
  const props = schema.properties ?? {};
  const required = new Set(schema.required ?? []);
  const out: FieldDescriptor[] = [];
  for (const key of Object.keys(props)) {
    const child = props[key];
    out.push(walkField(child, [...path, key], required.has(key)));
  }
  return out;
}

function walkField(schema: JSONSchema, path: string[], required: boolean): FieldDescriptor {
  const label = (schema.title as string) || path[path.length - 1] || "";
  const base: Pick<FieldDescriptor, "path" | "label" | "description" | "required" | "default"> = {
    path,
    label,
    description: schema.description,
    required,
    default: schema.default,
  };

  // Reject the "we don't render this" cases up front so the rest of
  // the function deals only with renderable shapes.
  if (
    schema.$ref !== undefined ||
    schema.allOf !== undefined ||
    schema.anyOf !== undefined ||
    schema.oneOf !== undefined ||
    schema.patternProperties !== undefined
  ) {
    return {
      ...base,
      type: "unsupported",
      unsupportedReason:
        "schema uses $ref / allOf / anyOf / oneOf / patternProperties — edit in YAML mode",
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
    case "object":
      return {
        ...base,
        type: "object",
        children: walkObject(schema, path),
      };
    case "array": {
      const items = schema.items;
      if (!items || typeof items !== "object") {
        return { ...base, type: "unsupported", unsupportedReason: "array without items schema" };
      }
      const itemType = normalizeType(items.type);
      if (itemType === "string" || itemType === "number" || itemType === "integer" || itemType === "boolean") {
        return { ...base, type: "array-of-primitives", itemType };
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

// ─── Validation ───────────────────────────────────────────────────

const ajv = new Ajv({
  // Helm authors don't always set "type" everywhere; allow.
  strict: false,
  // Schema can use "format: email" etc. without ajv-formats; we
  // tolerate by treating them as no-ops for validation.
  allErrors: true,
});

export interface ValidationIssue {
  /** JSON-pointer-style path to the offending value. */
  path: string[];
  /** Human-friendly violation message. */
  message: string;
  /** Original ajv keyword that flagged the issue (required / type /
   *  enum / pattern / etc.) — useful for inline UI affordances. */
  keyword: string;
}

/** Compile + validate. Returns [] when the value is valid. */
export function validateValues(
  schema: JSONSchema,
  values: unknown,
): ValidationIssue[] {
  if (!schema || typeof schema !== "object") return [];
  let validate;
  try {
    validate = ajv.compile(schema);
  } catch {
    // Ajv refuses some schemas (rare in practice for chart schemas).
    // Treat as "no validation" rather than crashing the form.
    return [];
  }
  validate(values);
  const errs = (validate.errors ?? []) as ErrorObject[];
  return errs.map(errorToIssue);
}

function errorToIssue(err: ErrorObject): ValidationIssue {
  const instancePath = err.instancePath ?? "";
  // ajv's instancePath is "/foo/bar"; split into path segments.
  const path = instancePath
    .split("/")
    .filter(Boolean)
    // Ajv encodes ~ as ~0 and / as ~1 per RFC 6901.
    .map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
  return {
    path,
    message: humanize(err),
    keyword: err.keyword,
  };
}

function humanize(err: ErrorObject): string {
  switch (err.keyword) {
    case "required":
      return `missing required field "${(err.params as { missingProperty?: string }).missingProperty ?? ""}"`;
    case "type":
      return `must be ${(err.params as { type?: string }).type ?? "the right type"}`;
    case "enum":
      return `must be one of: ${(err.params as { allowedValues?: unknown[] }).allowedValues?.join(", ") ?? ""}`;
    case "pattern":
      return `does not match pattern ${(err.params as { pattern?: string }).pattern ?? ""}`;
    case "minimum":
      return `must be ≥ ${(err.params as { limit?: number }).limit ?? ""}`;
    case "maximum":
      return `must be ≤ ${(err.params as { limit?: number }).limit ?? ""}`;
    case "minLength":
      return `must have ≥ ${(err.params as { limit?: number }).limit ?? ""} characters`;
    case "maxLength":
      return `must have ≤ ${(err.params as { limit?: number }).limit ?? ""} characters`;
  }
  return err.message ?? "schema violation";
}
