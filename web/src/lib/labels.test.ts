import { describe, expect, it } from "vitest";
import {
  findDuplicateKeys,
  validateLabelKey,
  validateLabelValue,
} from "./labels";

describe("validateLabelKey", () => {
  it("accepts simple alphanumeric keys", () => {
    expect(validateLabelKey("env")).toBeNull();
    expect(validateLabelKey("app")).toBeNull();
    expect(validateLabelKey("Tier1")).toBeNull();
  });

  it("accepts keys with prefixes", () => {
    expect(validateLabelKey("app.kubernetes.io/name")).toBeNull();
    expect(validateLabelKey("example.com/team")).toBeNull();
  });

  it("accepts keys with - _ . in the interior", () => {
    expect(validateLabelKey("my-app")).toBeNull();
    expect(validateLabelKey("my_app")).toBeNull();
    expect(validateLabelKey("my.app")).toBeNull();
  });

  it("rejects empty key", () => {
    expect(validateLabelKey("")).toMatch(/required/);
  });

  it("rejects empty prefix", () => {
    expect(validateLabelKey("/name")).toMatch(/prefix/);
  });

  it("rejects empty name segment", () => {
    expect(validateLabelKey("example.com/")).toMatch(/name segment/);
  });

  it("rejects name segment longer than 63 chars", () => {
    expect(validateLabelKey("a".repeat(64))).toMatch(/63/);
  });

  it("rejects prefix longer than 253 chars", () => {
    expect(validateLabelKey(`${"a".repeat(254)}/name`)).toMatch(/253/);
  });

  it("rejects name segment with invalid edges", () => {
    expect(validateLabelKey("-foo")).not.toBeNull();
    expect(validateLabelKey("foo-")).not.toBeNull();
    expect(validateLabelKey(".foo")).not.toBeNull();
  });

  it("rejects prefix that's not a DNS subdomain", () => {
    expect(validateLabelKey("Foo.Bar/name")).toMatch(/DNS/);
    expect(validateLabelKey("foo_bar/name")).toMatch(/DNS/);
  });
});

describe("validateLabelValue", () => {
  it("allows empty value", () => {
    expect(validateLabelValue("")).toBeNull();
  });

  it("accepts simple values", () => {
    expect(validateLabelValue("production")).toBeNull();
    expect(validateLabelValue("v1.2.3")).toBeNull();
    expect(validateLabelValue("a-b_c.d")).toBeNull();
  });

  it("rejects values longer than 63 chars", () => {
    expect(validateLabelValue("a".repeat(64))).toMatch(/63/);
  });

  it("rejects values with invalid edges", () => {
    expect(validateLabelValue("-foo")).not.toBeNull();
    expect(validateLabelValue("foo-")).not.toBeNull();
  });
});

describe("findDuplicateKeys", () => {
  it("returns empty set when keys are unique", () => {
    const dups = findDuplicateKeys([
      { key: "a", value: "1" },
      { key: "b", value: "2" },
    ]);
    expect(dups.size).toBe(0);
  });

  it("flags duplicates", () => {
    const dups = findDuplicateKeys([
      { key: "a", value: "1" },
      { key: "a", value: "2" },
      { key: "b", value: "3" },
    ]);
    expect(dups.has("a")).toBe(true);
    expect(dups.has("b")).toBe(false);
  });

  it("ignores empty keys", () => {
    const dups = findDuplicateKeys([
      { key: "", value: "1" },
      { key: "", value: "2" },
    ]);
    expect(dups.size).toBe(0);
  });
});
