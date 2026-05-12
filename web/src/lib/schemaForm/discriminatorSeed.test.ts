import { describe, expect, it } from "vitest";
import {
  emptyForDescriptor,
  seedBranchValue,
} from "./discriminatorSeed";
import type { DiscriminatorBranch, FieldDescriptor, JSONSchema } from "./types";

// Test factory — builds the FieldDescriptor shape the walker produces
// for a top-level field under a branch root.
function desc(
  key: string,
  type: FieldDescriptor["type"],
  extras: Partial<FieldDescriptor> = {},
): FieldDescriptor {
  return {
    path: [key],
    label: key,
    type,
    required: false,
    ...extras,
  };
}

function branch(
  opts: {
    label?: string;
    required?: string[];
    descriptors?: FieldDescriptor[];
    discriminatorKey?: string;
    properties?: Record<string, unknown>;
  } = {},
): DiscriminatorBranch {
  const schema: JSONSchema = {
    type: "object",
    properties: opts.properties ?? {},
    required: opts.required,
  } as JSONSchema;
  return {
    label: opts.label ?? "branch",
    schema,
    descriptors: opts.descriptors ?? [],
    discriminatorKey: opts.discriminatorKey,
  };
}

describe("seedBranchValue", () => {
  // Core fix for #180: Shape A's active-branch detector matches when
  // every key in `required` is present in the value object. The seed
  // must populate those keys so BranchSubForm renders on click.
  it("Shape A: seeds every top-level required key with a type-appropriate empty", () => {
    const b = branch({
      required: ["host", "port"],
      descriptors: [
        desc("host", "string"),
        desc("port", "integer"),
      ],
    });
    expect(seedBranchValue(b)).toEqual({ host: "", port: 0 });
  });

  // Shape B marker branches (e.g. cert-manager Issuer.selfSigned)
  // should produce `{ selfSigned: {} }` — that IS legitimate K8s YAML
  // for "use self-signed, no further config". The discriminatorKey's
  // presence alone makes Shape B's active detector match.
  it("Shape B marker: wraps under discriminatorKey, inner is empty when no required keys", () => {
    const b = branch({
      label: "selfSigned",
      discriminatorKey: "selfSigned",
      required: [],
    });
    expect(seedBranchValue(b)).toEqual({ selfSigned: {} });
  });

  // Shape B with a configurable sub-form: seeds the discriminatorKey
  // AND populates its inner required keys so the picker click lands
  // the operator in a sub-form with the right fields visible.
  it("Shape B with required inner fields: seeds both layers", () => {
    const b = branch({
      label: "acme",
      discriminatorKey: "acme",
      required: ["email", "server"],
      descriptors: [
        desc("email", "string"),
        desc("server", "string"),
      ],
    });
    expect(seedBranchValue(b)).toEqual({
      acme: { email: "", server: "" },
    });
  });

  // Edge: Shape A with no required keys at all. The active detector
  // intentionally requires req.length > 0, so this branch can't be
  // matched by detection alone — but tracking explicit picks in the
  // component is a bigger refactor. For now seed `{}` (the operator
  // sees the picker buttons; switching to YAML mode is the escape).
  it("Shape A with empty required array returns {}", () => {
    const b = branch({ required: [] });
    expect(seedBranchValue(b)).toEqual({});
  });

  // Descriptor schema-default takes precedence — "default: 80" on a
  // port field is more useful than a literal 0.
  it("prefers descriptor.default over the type-generic empty", () => {
    const b = branch({
      required: ["port"],
      descriptors: [desc("port", "integer", { default: 80 })],
    });
    expect(seedBranchValue(b)).toEqual({ port: 80 });
  });

  // Enums where every value is valid: seeding with the first enum
  // value (deterministic) lets the operator immediately see a valid
  // selection rather than the placeholder. Honors default first.
  it("prefers enum[0] when no default is set", () => {
    const b = branch({
      required: ["scheme"],
      descriptors: [
        desc("scheme", "string", { enum: ["HTTP", "HTTPS"] }),
      ],
    });
    expect(seedBranchValue(b)).toEqual({ scheme: "HTTP" });
  });

  it("default beats enum when both are present", () => {
    const b = branch({
      required: ["scheme"],
      descriptors: [
        desc("scheme", "string", { default: "HTTPS", enum: ["HTTP", "HTTPS"] }),
      ],
    });
    expect(seedBranchValue(b)).toEqual({ scheme: "HTTPS" });
  });

  // Only top-level descriptors should drive seeding. Branch descriptors
  // for nested children (paths longer than 1) describe sub-fields
  // INSIDE an object-typed required key — they shouldn't surface to
  // the branch-root seed.
  it("ignores non-top-level descriptors (path.length !== 1)", () => {
    const b = branch({
      required: ["config"],
      descriptors: [
        desc("config", "object"),
        // path-length 2: child of `config`, should not be hoisted.
        {
          path: ["config", "nested"],
          label: "nested",
          type: "string",
          required: true,
        },
      ],
    });
    expect(seedBranchValue(b)).toEqual({ config: {} });
  });

  // Required key listed in schema but missing from descriptors —
  // common for fields the walker filtered as unsupported. Fall back
  // to null so the key is still present (satisfies the active
  // detector) and the operator can edit in YAML.
  it("falls back to null for required keys without a descriptor", () => {
    const b = branch({
      required: ["raw", "name"],
      descriptors: [desc("name", "string")],
    });
    expect(seedBranchValue(b)).toEqual({ raw: null, name: "" });
  });

  // Defensive: malformed schema.required (not an array of strings)
  // shouldn't throw.
  it("tolerates non-string entries in schema.required", () => {
    const b = branch({
      required: ["good", 42 as unknown as string, null as unknown as string],
      descriptors: [desc("good", "string")],
    });
    expect(seedBranchValue(b)).toEqual({ good: "" });
  });
});

describe("emptyForDescriptor", () => {
  it("returns descriptor.default when set, regardless of type", () => {
    expect(emptyForDescriptor({
      path: ["x"], label: "x", type: "integer", required: false, default: 7,
    })).toBe(7);
    expect(emptyForDescriptor({
      path: ["x"], label: "x", type: "boolean", required: false, default: true,
    })).toBe(true);
  });

  it("returns type-appropriate empties when neither default nor enum is set", () => {
    expect(emptyForDescriptor({ path: ["s"], label: "s", type: "string",  required: false })).toBe("");
    expect(emptyForDescriptor({ path: ["n"], label: "n", type: "number",  required: false })).toBe(0);
    expect(emptyForDescriptor({ path: ["i"], label: "i", type: "integer", required: false })).toBe(0);
    expect(emptyForDescriptor({ path: ["b"], label: "b", type: "boolean", required: false })).toBe(false);
    expect(emptyForDescriptor({ path: ["a"], label: "a", type: "array-of-primitives", required: false })).toEqual([]);
    expect(emptyForDescriptor({ path: ["a"], label: "a", type: "array-of-objects",    required: false })).toEqual([]);
    expect(emptyForDescriptor({ path: ["m"], label: "m", type: "kv-map",   required: false })).toEqual({});
    expect(emptyForDescriptor({ path: ["o"], label: "o", type: "object",   required: false })).toEqual({});
  });

  it("unsupported descriptors get null so the seeded key is still present", () => {
    expect(emptyForDescriptor({
      path: ["u"], label: "u", type: "unsupported", required: false,
      unsupportedReason: "x",
    })).toBeNull();
  });
});
