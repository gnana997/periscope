// k8sAllowlist.test.ts — direct coverage for the path-trie filter
// extracted from filterSchemaForKind. The roundTrip suite exercises
// filter+walker integration against full Deployment / ConfigMap /
// Service shapes; this file isolates the filter so failures point
// straight at the trie or expansion logic.

import { describe, expect, it } from "vitest";
import { filterSchemaForKind } from "./k8sAllowlist";
import { buildRefResolver } from "./refResolver";
import type { OpenAPIDoc } from "../api";
import type { JSONSchema } from "./types";

// ── Minimal docs purpose-built per test ──────────────────────────

/** ConfigMap-shaped doc where `metadata` is a direct `$ref` (the
 *  shape used by the existing synthDoc in roundTrip.test.ts). The
 *  filter should narrow inside it when given a resolver. */
const docDirectRefMetadata: OpenAPIDoc = {
  components: {
    schemas: {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {
        type: "object",
        properties: {
          name: { type: "string" },
          namespace: { type: "string" },
          labels: { type: "object", additionalProperties: { type: "string" } },
          annotations: { type: "object", additionalProperties: { type: "string" } },
          uid: { type: "string" },
          creationTimestamp: { type: "string" },
          managedFields: { type: "array", items: { type: "object" } },
        },
      },
      "io.k8s.api.core.v1.ConfigMap": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "", version: "v1", kind: "ConfigMap" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: {
            $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta",
          },
          data: { type: "object", additionalProperties: { type: "string" } },
          binaryData: { type: "object", additionalProperties: { type: "string" } },
          immutable: { type: "boolean" },
        },
      },
    },
  },
} as unknown as OpenAPIDoc;

/** ConfigMap-shaped doc where `metadata` is wrapped in `allOf:
 *  [{$ref: ObjectMeta}]` — the shape K8s OpenAPI v3 actually emits.
 *  The filter has to merge the allOf to peek inside. */
const docAllOfWrappedMetadata: OpenAPIDoc = {
  components: {
    schemas: {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {
        type: "object",
        properties: {
          name: { type: "string" },
          namespace: { type: "string" },
          labels: { type: "object", additionalProperties: { type: "string" } },
          annotations: { type: "object", additionalProperties: { type: "string" } },
          uid: { type: "string" },
          creationTimestamp: { type: "string" },
          managedFields: { type: "array", items: { type: "object" } },
        },
      },
      "io.k8s.api.core.v1.ConfigMap": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "", version: "v1", kind: "ConfigMap" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
            ],
          },
          data: { type: "object", additionalProperties: { type: "string" } },
          binaryData: { type: "object", additionalProperties: { type: "string" } },
          immutable: { type: "boolean" },
        },
      },
    },
  },
} as unknown as OpenAPIDoc;

const cmRoot = (doc: OpenAPIDoc): JSONSchema =>
  (doc.components!.schemas as Record<string, JSONSchema>)["io.k8s.api.core.v1.ConfigMap"];

describe("filterSchemaForKind — metadata narrowing", () => {
  it("narrows metadata.* inside a direct $ref envelope when resolveRef is supplied", () => {
    const filtered = filterSchemaForKind(cmRoot(docDirectRefMetadata), "ConfigMap", {
      resolveRef: buildRefResolver(docDirectRefMetadata),
    });
    const meta = filtered.properties?.metadata;
    expect(meta?.properties).toBeDefined();
    expect(Object.keys(meta!.properties!)).toEqual(
      expect.arrayContaining(["name", "namespace", "labels", "annotations"]),
    );
    expect(Object.keys(meta!.properties!)).not.toContain("uid");
    expect(Object.keys(meta!.properties!)).not.toContain("creationTimestamp");
    expect(Object.keys(meta!.properties!)).not.toContain("managedFields");
  });

  it("narrows metadata.* inside an allOf-wrapped $ref envelope (the real K8s shape)", () => {
    const filtered = filterSchemaForKind(cmRoot(docAllOfWrappedMetadata), "ConfigMap", {
      resolveRef: buildRefResolver(docAllOfWrappedMetadata),
    });
    const meta = filtered.properties?.metadata;
    expect(Object.keys(meta!.properties!)).toEqual(
      expect.arrayContaining(["name", "namespace", "labels", "annotations"]),
    );
    expect(Object.keys(meta!.properties!)).not.toContain("uid");
  });

  it("strips $ref and allOf on inlined output so the walker sees the narrowed properties", () => {
    const filtered = filterSchemaForKind(cmRoot(docAllOfWrappedMetadata), "ConfigMap", {
      resolveRef: buildRefResolver(docAllOfWrappedMetadata),
    });
    const meta = filtered.properties?.metadata;
    // After narrowing the envelope is gone — the walker would
    // otherwise re-resolve the ref and re-expose uid / managedFields.
    expect(meta?.$ref).toBeUndefined();
    expect(meta?.allOf).toBeUndefined();
    expect(meta?.properties).toBeDefined();
  });

  it("degrades gracefully when resolveRef is omitted (envelope surfaces whole)", () => {
    // Without a resolver the filter can't peek inside the $ref, so
    // the envelope passes through unchanged. The walker will then
    // resolve and render the full ObjectMeta — same behaviour as
    // before this filter rewrite.
    const filtered = filterSchemaForKind(cmRoot(docDirectRefMetadata), "ConfigMap");
    const meta = filtered.properties?.metadata;
    expect(meta?.$ref).toBe(
      "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta",
    );
    expect(meta?.properties).toBeUndefined();
  });
});

// ── Array-item descent ([*]) ─────────────────────────────────────
//
// The Deployment allowlist uses `spec.template.spec.containers[*].image`
// to mean "for each container item, surface only .image (and the
// other listed fields)." Validate that descent here without dragging
// in the full Deployment synthDoc.

const docDeploymentMini: OpenAPIDoc = {
  components: {
    schemas: {
      "io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta": {
        type: "object",
        properties: {
          name: { type: "string" },
          namespace: { type: "string" },
          labels: { type: "object", additionalProperties: { type: "string" } },
          annotations: { type: "object", additionalProperties: { type: "string" } },
        },
      },
      "io.k8s.api.core.v1.Container": {
        type: "object",
        properties: {
          name: { type: "string" },
          image: { type: "string" },
          // Anything not in the allowlist must be pruned.
          lifecycle: { type: "object", properties: { preStop: { type: "object" } } },
          securityContext: { type: "object", properties: { runAsUser: { type: "integer" } } },
        },
      },
      "io.k8s.api.core.v1.PodSpec": {
        type: "object",
        properties: {
          restartPolicy: { type: "string" },
          containers: {
            type: "array",
            items: { $ref: "#/components/schemas/io.k8s.api.core.v1.Container" },
          },
          volumes: { type: "array", items: { type: "object" } },
        },
      },
      "io.k8s.api.core.v1.PodTemplateSpec": {
        type: "object",
        properties: {
          metadata: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
            ],
          },
          spec: { allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.PodSpec" }] },
        },
      },
      "io.k8s.api.apps.v1.DeploymentSpec": {
        type: "object",
        properties: {
          replicas: { type: "integer" },
          selector: { type: "object", properties: { matchLabels: { type: "object" } } },
          template: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.core.v1.PodTemplateSpec" }],
          },
        },
      },
      "io.k8s.api.apps.v1.Deployment": {
        type: "object",
        "x-kubernetes-group-version-kind": [
          { group: "apps", version: "v1", kind: "Deployment" },
        ],
        properties: {
          apiVersion: { type: "string" },
          kind: { type: "string" },
          metadata: {
            allOf: [
              { $ref: "#/components/schemas/io.k8s.apimachinery.pkg.apis.meta.v1.ObjectMeta" },
            ],
          },
          spec: {
            allOf: [{ $ref: "#/components/schemas/io.k8s.api.apps.v1.DeploymentSpec" }],
          },
        },
      },
    },
  },
} as unknown as OpenAPIDoc;

const dRoot = (doc: OpenAPIDoc): JSONSchema =>
  (doc.components!.schemas as Record<string, JSONSchema>)["io.k8s.api.apps.v1.Deployment"];

describe("filterSchemaForKind — [*] array-item descent (Deployment)", () => {
  const filtered = filterSchemaForKind(dRoot(docDeploymentMini), "Deployment", {
    resolveRef: buildRefResolver(docDeploymentMini),
  });

  it("descends into containers[] items and narrows their properties", () => {
    const containers = filtered.properties?.spec?.properties?.template?.properties?.spec?.properties
      ?.containers as JSONSchema | undefined;
    expect(containers?.type).toBe("array");
    const itemProps = (containers?.items as JSONSchema | undefined)?.properties;
    expect(itemProps).toBeDefined();
    expect(Object.keys(itemProps!)).toEqual(expect.arrayContaining(["name", "image"]));
    // Excluded by the allowlist — must not surface even though the
    // synthetic Container schema defines them.
    expect(Object.keys(itemProps!)).not.toContain("lifecycle");
    expect(Object.keys(itemProps!)).not.toContain("securityContext");
  });

  it("prunes sibling PodSpec fields (volumes) outside the allowlist", () => {
    const podSpec = filtered.properties?.spec?.properties?.template?.properties?.spec as
      | JSONSchema
      | undefined;
    expect(podSpec?.properties).toBeDefined();
    expect(Object.keys(podSpec!.properties!)).not.toContain("volumes");
    expect(Object.keys(podSpec!.properties!)).toEqual(
      expect.arrayContaining(["restartPolicy", "containers"]),
    );
  });

  it("preserves allowlisted leaves (spec.replicas, spec.selector) at the right depth", () => {
    const specProps = filtered.properties?.spec?.properties;
    expect(specProps).toBeDefined();
    expect(Object.keys(specProps!)).toEqual(expect.arrayContaining(["replicas", "selector"]));
  });

  it("template.metadata is narrowed to the labels/annotations subset (no name/namespace at template level)", () => {
    const templateMeta = filtered.properties?.spec?.properties?.template?.properties?.metadata as
      | JSONSchema
      | undefined;
    expect(templateMeta?.properties).toBeDefined();
    // The Deployment allowlist asks for `spec.template.metadata.labels`
    // and `.annotations` only — name / namespace are NOT in the trie
    // for this nested metadata.
    expect(Object.keys(templateMeta!.properties!)).toEqual(
      expect.arrayContaining(["labels", "annotations"]),
    );
    expect(Object.keys(templateMeta!.properties!)).not.toContain("name");
    expect(Object.keys(templateMeta!.properties!)).not.toContain("namespace");
  });
});
