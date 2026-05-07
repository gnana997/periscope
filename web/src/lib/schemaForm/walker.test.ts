import { describe, expect, it } from "vitest";
import { buildFieldDescriptors } from "./walker";
import { buildRefResolver, findSchemaByGVK } from "./refResolver";
import { filterSchemaForKind, getCreateOnlyPaths } from "./k8sAllowlist";
import type { JSONSchema } from "./types";
import type { OpenAPIDoc } from "../api";

// Helper: build an OpenAPIDoc with a components.schemas bundle
// without per-schema cast verbosity in the call sites.
const mockDoc = (schemas: Record<string, JSONSchema>): OpenAPIDoc =>
  ({ components: { schemas } } as unknown as OpenAPIDoc);

describe("walker — kv-map", () => {
  it("emits kv-map for additionalProperties: { type: string } when allowKvMap is set", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        labels: {
          type: "object",
          additionalProperties: { type: "string" },
        },
      },
    };
    const out = buildFieldDescriptors(schema, { allowKvMap: true });
    expect(out[0].type).toBe("kv-map");
    expect(out[0].kvValueType).toBe("string");
  });

  it("falls back to nested object when allowKvMap is unset (Helm behavior)", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        labels: {
          type: "object",
          additionalProperties: { type: "string" },
        },
      },
    };
    const out = buildFieldDescriptors(schema);
    expect(out[0].type).toBe("object");
  });

  it("does not emit kv-map when properties exist alongside additionalProperties", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        meta: {
          type: "object",
          properties: { name: { type: "string" } },
          additionalProperties: { type: "string" },
        },
      },
    };
    const out = buildFieldDescriptors(schema, { allowKvMap: true });
    expect(out[0].type).toBe("object");
  });
});

describe("walker — array-of-objects", () => {
  it("emits array-of-objects with relative-path children when allowArrayOfObjects is set", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        ports: {
          type: "array",
          items: {
            type: "object",
            properties: {
              name: { type: "string" },
              port: { type: "integer" },
            },
            required: ["port"],
          },
        },
      },
    };
    const out = buildFieldDescriptors(schema, { allowArrayOfObjects: true });
    expect(out[0].type).toBe("array-of-objects");
    expect(out[0].children).toHaveLength(2);
    // Children paths are relative to the row item, not absolute.
    expect(out[0].children?.[0].path).toEqual(["name"]);
    expect(out[0].children?.[1].path).toEqual(["port"]);
    expect(out[0].children?.[1].required).toBe(true);
  });

  it("falls back to unsupported when allowArrayOfObjects is unset (Helm behavior)", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        ports: { type: "array", items: { type: "object", properties: {} } },
      },
    };
    const out = buildFieldDescriptors(schema);
    expect(out[0].type).toBe("unsupported");
  });
});

describe("walker — $ref resolution", () => {
  const doc = mockDoc({
    "io.example.v1.Outer": {
      type: "object",
      properties: {
        inner: { $ref: "#/components/schemas/io.example.v1.Inner" },
      },
    },
    "io.example.v1.Inner": {
      type: "object",
      properties: { value: { type: "string" } },
    },
  });

  it("resolves $ref via WalkOptions.resolveRef", () => {
    const out = buildFieldDescriptors(
      { $ref: "#/components/schemas/io.example.v1.Outer" },
      { resolveRef: buildRefResolver(doc) },
    );
    expect(out[0].type).toBe("object");
    expect(out[0].path).toEqual(["inner"]);
    expect(out[0].children?.[0].path).toEqual(["inner", "value"]);
    expect(out[0].children?.[0].type).toBe("string");
  });

  it("breaks recursion safely on self-referential $ref", () => {
    const recursiveDoc = mockDoc({
      "io.example.v1.Tree": {
        type: "object",
        properties: {
          child: { $ref: "#/components/schemas/io.example.v1.Tree" },
        },
      },
    });
    const out = buildFieldDescriptors(
      { $ref: "#/components/schemas/io.example.v1.Tree" },
      { resolveRef: buildRefResolver(recursiveDoc) },
    );
    // First level resolves; the SECOND nested level encounters the
    // same ref and stops, so the form shows one level of nesting
    // and an "edit in YAML" hint beneath it.
    expect(out[0].type).toBe("object");
    const grandchild = out[0].children?.[0];
    expect(grandchild?.type).toBe("unsupported");
    expect(grandchild?.unsupportedReason).toMatch(/recursive|\$ref/);
  });

  it("allows sibling fields to share a $ref without false-recursion", () => {
    const sharedDoc = mockDoc({
      "io.example.v1.Outer": {
        type: "object",
        properties: {
          a: { $ref: "#/components/schemas/io.example.v1.Inner" },
          b: { $ref: "#/components/schemas/io.example.v1.Inner" },
        },
      },
      "io.example.v1.Inner": {
        type: "object",
        properties: { x: { type: "string" } },
      },
    });
    const out = buildFieldDescriptors(
      { $ref: "#/components/schemas/io.example.v1.Outer" },
      { resolveRef: buildRefResolver(sharedDoc) },
    );
    expect(out).toHaveLength(2);
    expect(out[0].type).toBe("object");
    expect(out[1].type).toBe("object");
    expect(out[0].children?.[0].type).toBe("string");
    expect(out[1].children?.[0].type).toBe("string");
  });

  it("flags $ref as unsupported when no resolver is supplied", () => {
    const out = buildFieldDescriptors({
      type: "object",
      properties: { x: { $ref: "#/components/schemas/Foo" } },
    });
    expect(out[0].type).toBe("unsupported");
  });
});

describe("walker — createOnlyPaths", () => {
  it("flags matching descriptor paths as editable=create-only", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        metadata: {
          type: "object",
          properties: {
            name: { type: "string" },
            namespace: { type: "string" },
            labels: { type: "object", additionalProperties: { type: "string" } },
          },
        },
      },
    };
    const out = buildFieldDescriptors(schema, {
      createOnlyPaths: ["metadata.name", "metadata.namespace"],
      allowKvMap: true,
    });
    const meta = out[0];
    expect(meta.children?.[0].path).toEqual(["metadata", "name"]);
    expect(meta.children?.[0].editable).toBe("create-only");
    expect(meta.children?.[1].editable).toBe("create-only");
    expect(meta.children?.[2].editable).toBeUndefined();
  });
});

describe("findSchemaByGVK", () => {
  it("locates the schema tagged with the matching GVK", () => {
    const doc = mockDoc({
      "io.k8s.api.core.v1.ConfigMap": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "", version: "v1", kind: "ConfigMap" },
        ],
        properties: { data: { type: "object", additionalProperties: { type: "string" } } },
      },
    });
    const s = findSchemaByGVK(doc, "", "v1", "ConfigMap");
    expect(s).toBeDefined();
    expect(s?.type).toBe("object");
  });

  it("returns undefined when no matching GVK exists", () => {
    expect(findSchemaByGVK(mockDoc({}), "", "v1", "ConfigMap")).toBeUndefined();
  });
});

describe("filterSchemaForKind", () => {
  const cmRoot: JSONSchema = {
    type: "object",
    properties: {
      apiVersion: { type: "string" },
      kind: { type: "string" },
      metadata: {
        type: "object",
        properties: {
          name: { type: "string" },
          namespace: { type: "string" },
          labels: { type: "object", additionalProperties: { type: "string" } },
          annotations: { type: "object", additionalProperties: { type: "string" } },
          // noise to filter out:
          uid: { type: "string" },
          creationTimestamp: { type: "string" },
          managedFields: { type: "array", items: { type: "object" } },
          resourceVersion: { type: "string" },
        },
      },
      data: { type: "object", additionalProperties: { type: "string" } },
      binaryData: { type: "object", additionalProperties: { type: "string" } },
      immutable: { type: "boolean" },
      // noise to filter out:
      status: { type: "object", properties: {} },
    },
  };

  it("strips status / managedFields / uid / creationTimestamp from ConfigMap", () => {
    const out = filterSchemaForKind(cmRoot, "ConfigMap");
    const props = out.properties!;
    expect(Object.keys(props)).toEqual(
      expect.arrayContaining(["metadata", "data", "binaryData", "immutable"]),
    );
    expect(props.status).toBeUndefined();
    const metaProps = props.metadata.properties!;
    expect(metaProps.uid).toBeUndefined();
    expect(metaProps.creationTimestamp).toBeUndefined();
    expect(metaProps.managedFields).toBeUndefined();
    expect(metaProps.resourceVersion).toBeUndefined();
    expect(metaProps.name).toBeDefined();
    expect(metaProps.namespace).toBeDefined();
  });

  it("groups spec.* allowlist entries into a filtered spec object for Service", () => {
    const svcRoot: JSONSchema = {
      type: "object",
      properties: {
        metadata: {
          type: "object",
          properties: { name: { type: "string" }, namespace: { type: "string" } },
        },
        spec: {
          type: "object",
          properties: {
            type: { type: "string" },
            selector: { type: "object", additionalProperties: { type: "string" } },
            ports: { type: "array", items: { type: "object" } },
            clusterIP: { type: "string" },
            // noise to filter:
            externalName: { type: "string" },
          },
        },
        status: { type: "object" },
      },
    };
    const out = filterSchemaForKind(svcRoot, "Service");
    const specProps = out.properties!.spec.properties!;
    expect(Object.keys(specProps)).toEqual(
      expect.arrayContaining(["type", "selector", "ports", "clusterIP"]),
    );
    expect(specProps.externalName).toBeUndefined();
    expect(out.properties!.status).toBeUndefined();
  });

  it("getCreateOnlyPaths returns the immutable-after-create paths", () => {
    expect(getCreateOnlyPaths("ConfigMap")).toEqual(
      expect.arrayContaining(["metadata.name", "metadata.namespace"]),
    );
    expect(getCreateOnlyPaths("Service")).toEqual(
      expect.arrayContaining(["metadata.name", "metadata.namespace", "spec.clusterIP"]),
    );
  });
});
