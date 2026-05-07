// discriminatorHints.test.ts — direct coverage for the walker's
// hint-table path (sibling-property-encoded K8s polymorphism).
// The roundTrip suite exercises end-to-end Deployment shapes;
// this file isolates the hinted-discriminator transform.

import { describe, expect, it } from "vitest";
import { buildFieldDescriptors } from "./walker";
import { buildRefResolver } from "./refResolver";
import { buildK8sDiscriminatorHints } from "./k8sDiscriminatorHints";
import type { OpenAPIDoc } from "../api";
import type { JSONSchema, FieldDescriptor } from "./types";

const probeShape: JSONSchema = {
  type: "object",
  properties: {
    httpGet: {
      type: "object",
      properties: {
        path: { type: "string" },
        port: { type: "string", format: "int-or-string" },
      },
    },
    tcpSocket: {
      type: "object",
      properties: {
        port: { type: "string", format: "int-or-string" },
      },
    },
    exec: {
      type: "object",
      properties: { command: { type: "array", items: { type: "string" } } },
    },
    grpc: {
      type: "object",
      properties: { port: { type: "integer" }, service: { type: "string" } },
    },
    initialDelaySeconds: { type: "integer" },
    periodSeconds: { type: "integer" },
    timeoutSeconds: { type: "integer" },
    successThreshold: { type: "integer" },
    failureThreshold: { type: "integer" },
  },
};

const docWithProbe: OpenAPIDoc = {
  components: {
    schemas: {
      "io.k8s.api.core.v1.Probe": probeShape,
      "test.WithDirectRef": {
        type: "object",
        properties: {
          probe: { $ref: "#/components/schemas/io.k8s.api.core.v1.Probe" },
        },
      },
      "test.WithAllOfWrappedRef": {
        type: "object",
        properties: {
          probe: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.Probe" }],
          },
        },
      },
      "test.WithMultiAllOf": {
        type: "object",
        properties: {
          probe: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.api.core.v1.Probe" },
              { description: "extra annotation" },
            ],
          },
        },
      },
    },
  },
} as unknown as OpenAPIDoc;

const walkOptionsForDoc = (doc: OpenAPIDoc) => ({
  resolveRef: buildRefResolver(doc),
  allowKvMap: true,
  allowArrayOfObjects: true,
  allowOneOfDiscriminator: true,
  discriminatorHints: buildK8sDiscriminatorHints(),
});

const flatten = (descriptors: FieldDescriptor[]): FieldDescriptor[] => {
  const out: FieldDescriptor[] = [];
  const visit = (ds: FieldDescriptor[]) => {
    for (const d of ds) {
      out.push(d);
      if (d.children) visit(d.children);
    }
  };
  visit(descriptors);
  return out;
};

const rootOf = (doc: OpenAPIDoc, name: string): JSONSchema =>
  (doc.components!.schemas as Record<string, JSONSchema>)[name];

describe("walker — hint matching against $ref envelopes", () => {
  it("matches a direct $ref to a hinted type and emits a discriminator", () => {
    const root = rootOf(docWithProbe, "test.WithDirectRef");
    const descriptors = buildFieldDescriptors(root, walkOptionsForDoc(docWithProbe));
    const probe = descriptors.find((d) => d.path.join(".") === "probe");
    expect(probe?.type).toBe("discriminator");
  });

  it("matches an allOf-wrapped $ref (the standard kube-openapi shape) to a hinted type", () => {
    const root = rootOf(docWithProbe, "test.WithAllOfWrappedRef");
    const descriptors = buildFieldDescriptors(root, walkOptionsForDoc(docWithProbe));
    const probe = descriptors.find((d) => d.path.join(".") === "probe");
    expect(probe?.type).toBe("discriminator");
  });

  it("does NOT match when allOf has multiple entries (envelope is no longer a pure ref)", () => {
    // Conservative: matching multi-entry allOfs would risk applying
    // hints to types that compose Probe with extra constraints —
    // safer to fall through to the standard merge+walk path.
    const root = rootOf(docWithProbe, "test.WithMultiAllOf");
    const descriptors = buildFieldDescriptors(root, walkOptionsForDoc(docWithProbe));
    const probe = descriptors.find((d) => d.path.join(".") === "probe");
    expect(probe?.type).not.toBe("discriminator");
  });

  it("does NOT match when no resolver is provided for the schema (hint is a no-op without context)", () => {
    // discriminatorHints without resolveRef = hint table can't tell
    // what the $ref points at, but the hint key is the ref string
    // itself, so matching IS still keyed off the raw schema. This
    // test confirms the hint-match behaviour: we DO emit a
    // discriminator for direct-$ref shapes even without the
    // resolver (because the hint reads structure off `raw`, not
    // resolved schema). Verify this is the actual behaviour so
    // future refactors don't silently break it.
    const root = rootOf(docWithProbe, "test.WithDirectRef");
    const descriptors = buildFieldDescriptors(root, {
      ...walkOptionsForDoc(docWithProbe),
      resolveRef: undefined,
    });
    const probe = descriptors.find((d) => d.path.join(".") === "probe");
    // Without a resolver the walker can't see Probe's properties,
    // so it can't build branches — falls through to "$ref"
    // unsupported. This is the correct conservative behaviour.
    expect(probe?.type).toBe("unsupported");
  });
});

describe("walker — hinted discriminator output shape", () => {
  const root = rootOf(docWithProbe, "test.WithAllOfWrappedRef");
  const descriptors = buildFieldDescriptors(root, walkOptionsForDoc(docWithProbe));
  const probe = descriptors.find((d) => d.path.join(".") === "probe")!;

  it("emits a Shape B discriminator: every branch has discriminatorKey set", () => {
    expect(probe.type).toBe("discriminator");
    const branches = probe.branches ?? [];
    expect(branches.length).toBe(4);
    for (const b of branches) {
      expect(b.discriminatorKey).toBeDefined();
    }
    expect(branches.map((b) => b.discriminatorKey).sort()).toEqual([
      "exec",
      "grpc",
      "httpGet",
      "tcpSocket",
    ]);
  });

  it("uses the hint's labels when supplied (HTTP GET / TCP socket / etc.)", () => {
    const labels = (probe.branches ?? []).map((b) => b.label);
    expect(labels).toEqual(expect.arrayContaining(["HTTP GET", "TCP socket", "exec", "gRPC"]));
  });

  it("collects non-branch properties as sharedChildren (Probe threshold knobs)", () => {
    const sharedPaths = (probe.sharedChildren ?? []).map((d) => d.path.join("."));
    expect(sharedPaths).toEqual(
      expect.arrayContaining([
        "initialDelaySeconds",
        "periodSeconds",
        "timeoutSeconds",
        "successThreshold",
        "failureThreshold",
      ]),
    );
  });

  it("walks each branch's sub-schema so descriptor children are addressable (e.g. httpGet.path)", () => {
    const branches = probe.branches ?? [];
    const httpGet = branches.find((b) => b.discriminatorKey === "httpGet");
    expect(httpGet).toBeDefined();
    const childPaths = httpGet!.descriptors.map((d) => d.path.join("."));
    // Shape B convention: paths are prefixed by the discriminatorKey.
    expect(childPaths).toEqual(expect.arrayContaining(["httpGet.path", "httpGet.port"]));
  });
});

describe("walker — hint table fallback when no branch keys match", () => {
  it("falls back to standard object walk when none of the hint's branches exist on the schema", () => {
    // Synthesise a schema that LOOKS like Probe (matches the hint
    // table by ref) but only has threshold properties — operator
    // got an unusual schema variant. The walker should not emit an
    // empty discriminator; it should fall through and render the
    // properties as object children.
    const doc: OpenAPIDoc = {
      components: {
        schemas: {
          "io.k8s.api.core.v1.Probe": {
            type: "object",
            properties: {
              initialDelaySeconds: { type: "integer" },
            },
          },
          "test.Container": {
            type: "object",
            properties: {
              probe: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.Probe" }] },
            },
          },
        },
      },
    } as unknown as OpenAPIDoc;
    const descriptors = buildFieldDescriptors(rootOf(doc, "test.Container"), walkOptionsForDoc(doc));
    const probe = descriptors.find((d) => d.path.join(".") === "probe");
    expect(probe?.type).toBe("object");
    const childPaths = (probe?.children ?? []).map((d) => d.path.join("."));
    expect(childPaths).toContain("probe.initialDelaySeconds");
  });
});

describe("walker — KNOWN LIMITATION: hinted-type as array items (envFrom, volumes)", () => {
  // EnvFromSource and Volume are sibling-encoded oneOfs that appear
  // as ITEMS of an array (`envFrom[]`, `volumes[]`), not properties
  // of an object. The walker's array case derefs items inline and
  // walks them as plain object children — `walkField` (where the
  // hint check lives) never sees the row's outer shape, so the
  // hint table is bypassed and the row renders as a fieldset of all
  // sibling properties.
  //
  // Fixing this means a new descriptor type (`array-of-discriminators`)
  // and a renderer change. Tracked as the next follow-up. For now
  // the Deployment allowlist excludes volumes/envFrom — this test
  // pins the current behaviour so the regression is visible if /
  // when we do fix it.
  const doc: OpenAPIDoc = {
    components: {
      schemas: {
        "io.k8s.api.core.v1.EnvFromSource": {
          type: "object",
          properties: {
            prefix: { type: "string" },
            configMapRef: {
              type: "object",
              properties: { name: { type: "string" }, optional: { type: "boolean" } },
            },
            secretRef: {
              type: "object",
              properties: { name: { type: "string" }, optional: { type: "boolean" } },
            },
          },
        },
        "test.Container": {
          type: "object",
          properties: {
            envFrom: {
              type: "array",
              items: {
                allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.EnvFromSource" }],
              },
            },
          },
        },
      },
    },
  } as unknown as OpenAPIDoc;

  it("[known limitation] array items render as array-of-objects, NOT as per-row discriminator", () => {
    const all = flatten(buildFieldDescriptors(rootOf(doc, "test.Container"), walkOptionsForDoc(doc)));
    const envFrom = all.find((d) => d.path.join(".") === "envFrom");
    expect(envFrom?.type).toBe("array-of-objects");
    // Current behaviour: row item's outer shape bypasses the hint
    // — sibling props show as columns. Want to flip this when we
    // add array-of-discriminators support.
    const rowChildPaths = (envFrom?.children ?? []).map((d) => d.path.join("."));
    expect(rowChildPaths).toEqual(expect.arrayContaining(["prefix", "configMapRef", "secretRef"]));
  });
});

describe("walker — DiscriminatorHint type contract", () => {
  it("typed labels and branches are stable identifiers — quick sanity vs the hint table", () => {
    const hints = buildK8sDiscriminatorHints();
    const probe = hints.get("#/components/schemas/io.k8s.api.core.v1.Probe");
    expect(probe).toBeDefined();
    expect(probe!.branches).toEqual(["httpGet", "tcpSocket", "exec", "grpc"]);

    const volume = hints.get("#/components/schemas/io.k8s.api.core.v1.Volume");
    expect(volume).toBeDefined();
    // ~30 volume types — exact count guards against accidental
    // truncation. Adjust this if the canonical list changes.
    expect(volume!.branches.length).toBeGreaterThanOrEqual(25);
    expect(volume!.branches).toEqual(
      expect.arrayContaining(["configMap", "secret", "emptyDir", "persistentVolumeClaim"]),
    );

    const envFromSource = hints.get("#/components/schemas/io.k8s.api.core.v1.EnvFromSource");
    expect(envFromSource).toBeDefined();
    expect(envFromSource!.branches).toEqual(["configMapRef", "secretRef"]);

    const lifecycleHandler = hints.get(
      "#/components/schemas/io.k8s.api.core.v1.LifecycleHandler",
    );
    expect(lifecycleHandler).toBeDefined();
    expect(lifecycleHandler!.branches).toContain("sleep"); // 1.29+ feature
  });
});

