import { describe, expect, it } from "vitest";
import {
  buildFieldDescriptors,
  validateValues,
  type JSONSchema,
} from "./helmSchema";

describe("buildFieldDescriptors", () => {
  it("emits a descriptor per top-level property", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        replicaCount: { type: "integer", default: 1 },
        image: {
          type: "object",
          properties: {
            tag: { type: "string", default: "latest" },
            pullPolicy: { type: "string", enum: ["Always", "IfNotPresent"] },
          },
        },
      },
      required: ["replicaCount"],
    };
    const out = buildFieldDescriptors(schema);
    expect(out).toHaveLength(2);
    expect(out[0]).toMatchObject({
      path: ["replicaCount"],
      type: "integer",
      required: true,
      default: 1,
    });
    expect(out[1].type).toBe("object");
    expect(out[1].children).toHaveLength(2);
    expect(out[1].children?.[1]).toMatchObject({
      path: ["image", "pullPolicy"],
      type: "string",
      enum: ["Always", "IfNotPresent"],
    });
  });

  it("recursively walks nested objects", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        a: {
          type: "object",
          properties: {
            b: {
              type: "object",
              properties: {
                c: { type: "string" },
              },
            },
          },
        },
      },
    };
    const out = buildFieldDescriptors(schema);
    const c = out[0].children?.[0].children?.[0];
    expect(c?.path).toEqual(["a", "b", "c"]);
    expect(c?.type).toBe("string");
  });

  it("renders array of primitives as array-of-primitives", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        keywords: {
          type: "array",
          items: { type: "string" },
          default: [],
        },
      },
    };
    const out = buildFieldDescriptors(schema);
    expect(out[0].type).toBe("array-of-primitives");
    expect(out[0].itemType).toBe("string");
  });

  it("flags array of objects as unsupported", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        replicas: {
          type: "array",
          items: { type: "object", properties: { name: { type: "string" } } },
        },
      },
    };
    const out = buildFieldDescriptors(schema);
    expect(out[0].type).toBe("unsupported");
    expect(out[0].unsupportedReason).toMatch(/array of objects/i);
  });

  it("flags $ref / anyOf / oneOf as unsupported in the helm path", () => {
    // Helm path uses default WalkOptions (no resolveRef, no
    // allowOneOfDiscriminator). With those off, $ref / anyOf /
    // oneOf still render as unsupported. allOf is now flattened
    // by the merger inside derefIfNeeded — verified separately
    // below.
    const schema: JSONSchema = {
      type: "object",
      properties: {
        a: { $ref: "#/definitions/Thing" },
        c: { anyOf: [{ type: "string" }, { type: "number" }] },
        d: { oneOf: [{ type: "string" }] },
      },
    };
    const out = buildFieldDescriptors(schema);
    for (const f of out) {
      expect(f.type).toBe("unsupported");
    }
  });

  it("merges allOf into a flat shape (helm path benefits too — #132)", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        merged: { allOf: [{ type: "string", description: "from-allOf" }] },
      },
    };
    const out = buildFieldDescriptors(schema);
    const merged = out.find((d) => d.path.join(".") === "merged");
    expect(merged?.type).toBe("string");
    expect(merged?.description).toBe("from-allOf");
  });

  it("allOf with conflicting types still surfaces as unsupported", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        bad: { type: "object", allOf: [{ type: "string" }] },
      },
    };
    const out = buildFieldDescriptors(schema);
    expect(out[0].type).toBe("unsupported");
  });

  it("normalizes ['string', 'null'] nullable types", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        nickname: { type: ["string", "null"] },
      },
    };
    const out = buildFieldDescriptors(schema);
    expect(out[0].type).toBe("string");
  });

  it("propagates required flag from parent", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        a: { type: "string" },
        b: { type: "string" },
      },
      required: ["b"],
    };
    const out = buildFieldDescriptors(schema);
    expect(out[0].required).toBe(false);
    expect(out[1].required).toBe(true);
  });

  it("uses title for label, falls back to last path segment", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        replicaCount: { type: "integer", title: "Replica Count" },
        version: { type: "string" },
      },
    };
    const out = buildFieldDescriptors(schema);
    expect(out[0].label).toBe("Replica Count");
    expect(out[1].label).toBe("version");
  });

  it("returns [] for non-object root schemas", () => {
    const schema: JSONSchema = { type: "string" };
    expect(buildFieldDescriptors(schema)).toEqual([]);
  });

  it("tolerates schema without explicit top-level type", () => {
    const schema: JSONSchema = {
      properties: { a: { type: "string" } },
    };
    const out = buildFieldDescriptors(schema);
    expect(out).toHaveLength(1);
    expect(out[0].path).toEqual(["a"]);
  });
});

describe("validateValues", () => {
  const schema: JSONSchema = {
    type: "object",
    properties: {
      replicaCount: { type: "integer", minimum: 1, maximum: 10 },
      env: { type: "string", enum: ["dev", "staging", "prod"] },
      name: { type: "string", minLength: 3 },
    },
    required: ["replicaCount", "name"],
  };

  it("returns [] for valid values", () => {
    const issues = validateValues(schema, {
      replicaCount: 3,
      env: "prod",
      name: "web",
    });
    expect(issues).toEqual([]);
  });

  it("flags missing required field", () => {
    const issues = validateValues(schema, { replicaCount: 1 });
    expect(issues.some((i) => i.keyword === "required" && i.message.includes("name"))).toBe(true);
  });

  it("flags wrong type", () => {
    const issues = validateValues(schema, {
      replicaCount: "three",
      name: "web",
    });
    expect(issues.some((i) => i.keyword === "type")).toBe(true);
  });

  it("flags out-of-enum", () => {
    const issues = validateValues(schema, {
      replicaCount: 1,
      env: "production", // not in enum
      name: "web",
    });
    expect(issues.some((i) => i.keyword === "enum")).toBe(true);
  });

  it("flags out-of-range integer", () => {
    const issues = validateValues(schema, {
      replicaCount: 99,
      name: "web",
    });
    expect(issues.some((i) => i.keyword === "maximum")).toBe(true);
  });

  it("flags too-short string", () => {
    const issues = validateValues(schema, {
      replicaCount: 1,
      name: "ab",
    });
    expect(issues.some((i) => i.keyword === "minLength")).toBe(true);
  });

  it("recovers gracefully from a malformed schema", () => {
    const broken: JSONSchema = {
      type: "object",
      properties: {
        a: { pattern: "[" }, // unclosed regex character class — ajv rejects compile
      },
    };
    // Should not throw, should return empty issue list (no validation
    // ran rather than a crash).
    expect(() => validateValues(broken, {})).not.toThrow();
  });

  it("returns issue path as JSON-pointer split", () => {
    const issues = validateValues(schema, {
      replicaCount: 0, // below minimum
      name: "web",
    });
    const minIssue = issues.find((i) => i.keyword === "minimum");
    expect(minIssue?.path).toEqual(["replicaCount"]);
  });
});
