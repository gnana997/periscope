// schemaForm/SchemaForm.test.ts — pure-logic tests for the
// section-grouping helpers. The DOM render path (primary visible /
// metadata collapsed / advanced count) is verified manually since
// this app's vitest runs in a node environment without
// @testing-library/react. The data flow that decides which
// descriptor lands in which bucket is fully testable here.

import { describe, it, expect } from "vitest";
import { collectSectioned, descendantHasSection } from "./SchemaForm";
import type { FieldDescriptor } from "./types";

const stringField = (
  path: string[],
  extras: Partial<FieldDescriptor> = {},
): FieldDescriptor => ({
  type: "string",
  path,
  label: path[path.length - 1] ?? "",
  required: false,
  ...extras,
});

const objectField = (
  path: string[],
  children: FieldDescriptor[],
  extras: Partial<FieldDescriptor> = {},
): FieldDescriptor => ({
  type: "object",
  path,
  label: path[path.length - 1] ?? "",
  required: false,
  children,
  ...extras,
});

describe("collectSectioned", () => {
  it("buckets top-level descriptors by their stamped section", () => {
    const ds: FieldDescriptor[] = [
      stringField(["data"], { section: "primary", displayOrder: 0 }),
      stringField(["binaryData"], { section: "primary", displayOrder: 1 }),
      stringField(["immutable"], { section: "advanced", displayOrder: 0 }),
    ];
    const out = collectSectioned(ds);
    expect(out.total).toBe(3);
    expect(out.primary.map((d) => d.path.join("."))).toEqual([
      "data",
      "binaryData",
    ]);
    expect(out.advanced.map((d) => d.path.join("."))).toEqual(["immutable"]);
    expect(out.metadata).toEqual([]);
  });

  it("promotes nested children of an unsectioned parent into their section bucket", () => {
    // metadata container has no section; its children have section="metadata".
    const ds: FieldDescriptor[] = [
      objectField(["metadata"], [
        stringField(["metadata", "name"], { section: "metadata", displayOrder: 0 }),
        stringField(["metadata", "namespace"], { section: "metadata", displayOrder: 1 }),
        stringField(["metadata", "annotations"], { section: "metadata", displayOrder: 3 }),
        stringField(["metadata", "labels"], { section: "metadata", displayOrder: 2 }),
      ]),
      stringField(["data"], { section: "primary", displayOrder: 0 }),
    ];
    const out = collectSectioned(ds);
    expect(out.total).toBe(5);
    // Sorted by displayOrder regardless of the source-tree order.
    expect(out.metadata.map((d) => d.path.join("."))).toEqual([
      "metadata.name",
      "metadata.namespace",
      "metadata.labels",
      "metadata.annotations",
    ]);
  });

  it("returns total=0 for fully unsectioned descriptors (Helm path)", () => {
    const ds: FieldDescriptor[] = [
      stringField(["replicas"]),
      objectField(["image"], [stringField(["image", "tag"])]),
    ];
    const out = collectSectioned(ds);
    expect(out.total).toBe(0);
    expect(out.primary).toEqual([]);
    expect(out.metadata).toEqual([]);
    expect(out.advanced).toEqual([]);
  });

  it("descriptors without displayOrder sort to the end of their bucket", () => {
    const ds: FieldDescriptor[] = [
      stringField(["a"], { section: "primary" }),
      stringField(["b"], { section: "primary", displayOrder: 0 }),
      stringField(["c"], { section: "primary", displayOrder: 1 }),
    ];
    const out = collectSectioned(ds);
    expect(out.primary.map((d) => d.path.join("."))).toEqual(["b", "c", "a"]);
  });
});

describe("descendantHasSection", () => {
  it("true when a child has a section", () => {
    const d = objectField(["spec"], [
      stringField(["spec", "type"], { section: "primary" }),
    ]);
    expect(descendantHasSection(d)).toBe(true);
  });

  it("false when neither the descriptor nor any descendant has a section", () => {
    const d = objectField(["spec"], [
      objectField(["spec", "nested"], [stringField(["spec", "nested", "leaf"])]),
    ]);
    expect(descendantHasSection(d)).toBe(false);
  });

  it("recurses past unsectioned intermediate containers", () => {
    const d = objectField(["spec"], [
      objectField(["spec", "nested"], [
        stringField(["spec", "nested", "leaf"], { section: "advanced" }),
      ]),
    ]);
    expect(descendantHasSection(d)).toBe(true);
  });

  it("false for a leaf descriptor", () => {
    const d = stringField(["replicas"]);
    expect(descendantHasSection(d)).toBe(false);
  });
});
