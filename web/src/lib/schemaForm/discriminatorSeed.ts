// discriminatorSeed — initial value for a oneOf branch when the
// operator picks it from the form's branch picker.
//
// Why this is non-trivial (and why #180 hurt before this existed):
// DiscriminatorInput's active-branch detector for Shape A (no
// `discriminatorKey` on branches) requires every key in the branch
// schema's `required` array to be present in the value object — it
// uses key-presence as a heuristic since the SPA doesn't do an ajv
// hop. If the seed is `{}` the detector finds no match → no sub-form
// renders → the operator sees the picker buttons still there with a
// stray `{}` already in their YAML buffer.
//
// Fix: seed each top-level required key with a type-appropriate empty
// (or the schema's `default` / first `enum` value when those are
// more informative). For Shape B branches we wrap the seeded inner
// under the `discriminatorKey` — `selfSigned: {}` style — which is
// also legitimate K8s shape for marker branches that take no config.

import type {
  DiscriminatorBranch,
  FieldDescriptor,
  JSONSchema,
} from "./types";

/**
 * Build the initial value for the operator's branch pick.
 *
 * Shape A → returns `{ <required-key>: <empty>, ... }` so the
 * active-branch detector matches the chosen branch and the
 * `BranchSubForm` renders immediately.
 *
 * Shape B → returns `{ [discriminatorKey]: <Shape A object> }`
 * (the discriminator key alone is enough to make the active detector
 * match — required-key seeding under it just gives the operator
 * something visible to edit inside the sub-form).
 *
 * When the branch has no required keys at all, an empty object is
 * returned. Shape A in that case will still leave `active = -1`
 * (the detector's `req.length > 0` guard); this is a known niche we
 * don't try to handle here because tracking explicit-pick state in
 * the component is a bigger refactor.
 */
export function seedBranchValue(branch: DiscriminatorBranch): unknown {
  // Primitive-branched discriminator (Service.targetPort string-or-
  // integer, Probe.timeoutSeconds, etc.). The branch's "value" is a
  // scalar, not an object — so a type-appropriate scalar empty is
  // the right seed. Object-shape required-key seeding doesn't apply
  // here; the active-branch detector matches by `typeof value`.
  const primitive = primitiveEmptyFor(branch.schema.type);
  if (primitive !== undefined) {
    return primitive;
  }

  const inner = seedRequiredKeys(branch);
  if (branch.discriminatorKey) {
    return { [branch.discriminatorKey]: inner };
  }
  return inner;
}

/**
 * Empty value for a primitive JSON-Schema type. Returns `undefined`
 * for non-primitive types (caller falls through to object handling).
 */
function primitiveEmptyFor(type: unknown): unknown {
  if (type === "string") return "";
  if (type === "integer" || type === "number") return 0;
  if (type === "boolean") return false;
  return undefined;
}

function seedRequiredKeys(
  branch: DiscriminatorBranch,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  const required = readRequired(branch.schema);
  if (required.length === 0) return out;

  // Index top-level descriptors by their key name. `branch.descriptors`
  // paths are relative to the branch's value root, so a descriptor
  // with `path: ["foo"]` is the descriptor for the top-level field
  // `foo`. Descriptors with deeper paths are children of object-typed
  // fields and don't drive seeding here.
  const topLevel = new Map<string, FieldDescriptor>();
  for (const d of branch.descriptors) {
    if (d.path.length === 1) topLevel.set(d.path[0], d);
  }

  for (const key of required) {
    const desc = topLevel.get(key);
    out[key] = desc ? emptyForDescriptor(desc) : null;
  }
  return out;
}

/**
 * Type-appropriate empty for a single field. Honors `default` and the
 * first `enum` value when set — feels less arbitrary to the operator
 * than a blanket `""` / `0`. Exported for testing.
 */
export function emptyForDescriptor(desc: FieldDescriptor): unknown {
  if (desc.default !== undefined) return desc.default;
  if (desc.enum && desc.enum.length > 0) return desc.enum[0];
  switch (desc.type) {
    case "string":
      return "";
    case "number":
    case "integer":
      return 0;
    case "boolean":
      return false;
    case "array-of-primitives":
    case "array-of-objects":
      return [];
    case "kv-map":
      return {};
    case "object":
      // Don't recurse into nested required keys. The user will see
      // the nested object's required fields in red and fill them in;
      // an empty object is enough to satisfy this branch's own
      // active-detector at the top level.
      return {};
    case "discriminator":
      // Nested discriminator at a required field: leave empty so the
      // operator picks a sub-branch explicitly.
      return {};
    case "unsupported":
      return null;
    default:
      return null;
  }
}

function readRequired(schema: JSONSchema): string[] {
  const raw = (schema as { required?: unknown }).required;
  if (!Array.isArray(raw)) return [];
  return raw.filter((x): x is string => typeof x === "string");
}
