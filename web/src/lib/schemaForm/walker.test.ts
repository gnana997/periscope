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

describe("walker — oneOf discriminator (#132)", () => {
  // Property-level oneOf: value of the field IS one of these shapes
  const sourceOneOf = {
    type: "object",
    properties: {
      source: {
        oneOf: [
          {
            type: "object",
            title: "git",
            required: ["repoURL"],
            properties: { repoURL: { type: "string" }, ref: { type: "string" } },
          },
          {
            type: "object",
            title: "helm",
            required: ["chart"],
            properties: { chart: { type: "string" }, version: { type: "string" } },
          },
        ],
      },
    },
  };

  it("emits discriminator for property-level oneOf when allowOneOfDiscriminator is set", () => {
    const ds = buildFieldDescriptors(sourceOneOf, { allowOneOfDiscriminator: true });
    const source = ds.find((d) => d.path.join(".") === "source");
    expect(source?.type).toBe("discriminator");
    expect(source?.branches).toHaveLength(2);
    expect(source?.branches?.[0].label).toBe("git");
    expect(source?.branches?.[1].label).toBe("helm");
    // Each branch carries pre-walked descriptors with paths relative
    // to the discriminator's value (since Shape A — no discriminatorKey).
    expect(source?.branches?.[0].descriptors[0].path).toEqual(["repoURL"]);
    expect(source?.branches?.[1].descriptors[0].path).toEqual(["chart"]);
  });

  it("falls back to unsupported when allowOneOfDiscriminator is off (Helm path)", () => {
    const ds = buildFieldDescriptors(sourceOneOf);
    const source = ds.find((d) => d.path.join(".") === "source");
    expect(source?.type).toBe("unsupported");
  });

  // Object-level oneOf with required-key branches (cert-manager style)
  const issuerStyle = {
    type: "object",
    properties: {
      spec: {
        type: "object",
        properties: {
          selfSigned: { type: "object", properties: {} },
          ca: {
            type: "object",
            properties: { secretName: { type: "string" } },
            required: ["secretName"],
          },
          vault: {
            type: "object",
            properties: { server: { type: "string" }, path: { type: "string" } },
            required: ["server", "path"],
          },
        },
        oneOf: [
          { required: ["selfSigned"] },
          { required: ["ca"] },
          { required: ["vault"] },
        ],
      },
    },
  };

  it("emits discriminator for object-level oneOf with required-key branches", () => {
    const ds = buildFieldDescriptors(issuerStyle, { allowOneOfDiscriminator: true });
    const spec = ds.find((d) => d.path.join(".") === "spec");
    expect(spec?.type).toBe("discriminator");
    expect(spec?.branches).toHaveLength(3);
    expect(spec?.branches?.map((b) => b.label)).toEqual(["selfSigned", "ca", "vault"]);
    expect(spec?.branches?.map((b) => b.discriminatorKey)).toEqual([
      "selfSigned",
      "ca",
      "vault",
    ]);
  });

  it("Shape B branch descriptors have paths prefixed with the discriminator key", () => {
    const ds = buildFieldDescriptors(issuerStyle, { allowOneOfDiscriminator: true });
    const spec = ds.find((d) => d.path.join(".") === "spec");
    const caBranch = spec?.branches?.[1];
    expect(caBranch?.descriptors[0].path).toEqual(["ca", "secretName"]);
    const vaultBranch = spec?.branches?.[2];
    expect(vaultBranch?.descriptors.map((d) => d.path)).toEqual([
      ["vault", "server"],
      ["vault", "path"],
    ]);
  });

  it("empty oneOf falls back to unsupported", () => {
    const ds = buildFieldDescriptors(
      { type: "object", properties: { x: { oneOf: [] } } },
      { allowOneOfDiscriminator: true },
    );
    const x = ds.find((d) => d.path.join(".") === "x");
    expect(x?.type).toBe("unsupported");
  });
});

describe("walker — allOf merging (#132)", () => {
  it("metadata: allOf:[{$ref: ObjectMeta}] flattens into a regular object", () => {
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
    const schema: JSONSchema = {
      type: "object",
      properties: {
        metadata: { allOf: [{ $ref: "#/components/schemas/ObjectMeta" }] },
      },
    };
    const ds = buildFieldDescriptors(schema, {
      resolveRef: (ref) =>
        definitions[ref.replace("#/components/schemas/", "")],
      allowKvMap: true,
    });
    const metadata = ds.find((d) => d.path.join(".") === "metadata");
    // Was unsupported pre-merger; now renders as a regular object.
    expect(metadata?.type).toBe("object");
    expect(metadata?.children?.map((c) => c.path.join("."))).toEqual([
      "metadata.name",
      "metadata.namespace",
      "metadata.labels",
    ]);
    // labels comes through as kv-map (additionalProperties of primitive type).
    const labels = metadata?.children?.find((c) => c.path.join(".") === "metadata.labels");
    expect(labels?.type).toBe("kv-map");
  });

  it("allOf type conflict still surfaces as unsupported", () => {
    const schema: JSONSchema = {
      type: "object",
      properties: {
        bad: {
          type: "object",
          allOf: [{ type: "string" }],
        },
      },
    };
    const ds = buildFieldDescriptors(schema);
    const bad = ds.find((d) => d.path.join(".") === "bad");
    expect(bad?.type).toBe("unsupported");
  });
});
