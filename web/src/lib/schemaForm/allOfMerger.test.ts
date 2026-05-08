// Tests for the allOf merger — pure schema-merge function called
// from the walker's derefIfNeeded so K8s metadata (which uses
// `allOf: [{$ref: ObjectMeta}]`) and kubebuilder-generated CRDs
// flatten into a single rendered shape instead of falling back
// to the yaml-only badge.

import { describe, expect, it } from "vitest";
import { mergeAllOf } from "./allOfMerger";
import type { JSONSchema } from "./types";

describe("mergeAllOf", () => {
  it("returns input unchanged when no allOf present", () => {
    const s: JSONSchema = { type: "object", properties: { a: { type: "string" } } };
    expect(mergeAllOf(s, { seen: new Set() })).toBe(s);
  });

  it("returns input unchanged when allOf is empty array", () => {
    const s: JSONSchema = { type: "object", allOf: [] };
    expect(mergeAllOf(s, { seen: new Set() })).toBe(s);
  });

  it("merges properties from a single allOf entry", () => {
    const s: JSONSchema = {
      type: "object",
      allOf: [{ properties: { a: { type: "string" } } }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.properties).toEqual({ a: { type: "string" } });
    expect(merged.allOf).toBeUndefined();
  });

  it("unions properties across multiple entries (later wins on collision)", () => {
    const s: JSONSchema = {
      allOf: [
        { properties: { a: { type: "string" }, shared: { type: "string" } } },
        { properties: { b: { type: "integer" }, shared: { type: "integer" } } },
      ],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.properties).toEqual({
      a: { type: "string" },
      b: { type: "integer" },
      shared: { type: "integer" }, // later wins
    });
  });

  it("unions required arrays across entries (deduped)", () => {
    const s: JSONSchema = {
      required: ["x"],
      allOf: [{ required: ["a", "b"] }, { required: ["b", "c"] }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.required?.sort()).toEqual(["a", "b", "c", "x"]);
  });

  it("preserves the parent's own properties not contributed by allOf", () => {
    const s: JSONSchema = {
      type: "object",
      properties: { ownField: { type: "boolean" } },
      allOf: [{ properties: { inheritedField: { type: "string" } } }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.properties?.ownField).toEqual({ type: "boolean" });
    expect(merged.properties?.inheritedField).toEqual({ type: "string" });
  });

  it("description: last non-empty wins", () => {
    const s: JSONSchema = {
      description: "parent",
      allOf: [{ description: "first" }, { description: "second" }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.description).toBe("second");
  });

  it("default: last non-undefined wins", () => {
    const s: JSONSchema = {
      allOf: [{ default: 1 }, { default: 2 }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.default).toBe(2);
  });

  it("type: agreement preserved", () => {
    const s: JSONSchema = {
      type: "object",
      allOf: [{ type: "object", properties: { a: { type: "string" } } }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.type).toBe("object");
  });

  it("type: conflict aborts merge (returns original)", () => {
    const s: JSONSchema = {
      type: "object",
      allOf: [{ type: "string" }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    // Conflict — abort, return original schema. Walker surfaces as
    // unsupported via the remaining-rejections path.
    expect(merged).toBe(s);
  });

  it("enum: intersection across entries", () => {
    const s: JSONSchema = {
      allOf: [{ enum: ["a", "b", "c"] }, { enum: ["b", "c", "d"] }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.enum).toEqual(["b", "c"]);
  });

  it("enum: empty intersection drops the constraint", () => {
    const s: JSONSchema = {
      allOf: [{ enum: ["a"] }, { enum: ["b"] }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.enum).toBeUndefined();
  });

  it("resolves $ref entries via the provided resolver", () => {
    const definitions: Record<string, JSONSchema> = {
      ObjectMeta: {
        type: "object",
        properties: {
          name: { type: "string" },
          namespace: { type: "string" },
          labels: { type: "object", additionalProperties: { type: "string" } },
        },
      },
    };
    const s: JSONSchema = {
      allOf: [{ $ref: "#/components/schemas/ObjectMeta" }],
    };
    const merged = mergeAllOf(s, {
      resolveRef: (ref) => {
        const id = ref.replace("#/components/schemas/", "");
        return definitions[id];
      },
      seen: new Set(),
    });
    expect(merged.properties?.name).toEqual({ type: "string" });
    expect(merged.properties?.namespace).toEqual({ type: "string" });
    expect(merged.properties?.labels).toEqual({
      type: "object",
      additionalProperties: { type: "string" },
    });
  });

  it("aborts when an allOf entry has unresolvable $ref", () => {
    const s: JSONSchema = {
      allOf: [{ $ref: "#/components/schemas/NotARealRef" }],
    };
    const merged = mergeAllOf(s, {
      resolveRef: () => undefined,
      seen: new Set(),
    });
    expect(merged).toBe(s);
  });

  it("recursively flattens nested allOf inside an entry", () => {
    const definitions: Record<string, JSONSchema> = {
      Base: {
        type: "object",
        properties: { id: { type: "string" } },
      },
      Mid: {
        type: "object",
        allOf: [{ $ref: "#/components/schemas/Base" }],
        properties: { mid: { type: "string" } },
      },
    };
    const s: JSONSchema = {
      type: "object",
      allOf: [{ $ref: "#/components/schemas/Mid" }],
      properties: { top: { type: "string" } },
    };
    const merged = mergeAllOf(s, {
      resolveRef: (ref) => definitions[ref.replace("#/components/schemas/", "")],
      seen: new Set(),
    });
    expect(merged.properties?.id).toEqual({ type: "string" });
    expect(merged.properties?.mid).toEqual({ type: "string" });
    expect(merged.properties?.top).toEqual({ type: "string" });
  });

  it("preserves oneOf from any entry for downstream discriminator detection", () => {
    const s: JSONSchema = {
      type: "object",
      allOf: [
        {
          properties: { a: { type: "string" }, b: { type: "string" } },
          oneOf: [{ required: ["a"] }, { required: ["b"] }],
        },
      ],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.oneOf).toBeDefined();
    expect(merged.oneOf).toHaveLength(2);
  });

  it("preserves additionalProperties from any entry", () => {
    const s: JSONSchema = {
      allOf: [{ additionalProperties: { type: "string" } }],
    };
    const merged = mergeAllOf(s, { seen: new Set() });
    expect(merged.additionalProperties).toEqual({ type: "string" });
  });
});
