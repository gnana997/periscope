// schemaForm/k8sAllowlist.ts — per-kind narrowing of the K8s
// OpenAPI v3 schema. Operators should never see status,
// managedFields, creationTimestamp, uid, resourceVersion, etc.;
// per #116 we filter the schema BEFORE the walker runs so the
// renderer never even considers those fields.
//
// Allowlist (not deny-list) per design call-out — predictable and
// auditable; if K8s adds a new noise field, it stays hidden by
// default until we explicitly allowlist it.

import type { JSONSchema } from "./types";

export type SupportedKind = "ConfigMap" | "Secret" | "Service" | "Ingress";

/** Each kind declares the metadata fields and the spec/data subtree
 *  paths it surfaces. Paths are dotted, rooted at the K8s object
 *  (so e.g. `metadata.name`, `data`, `spec.type`). */
interface KindSpec {
  metadata: string[];
  /** Top-level fields outside of `metadata` we surface (e.g. `data`,
   *  `type`, `spec.type`). Each entry is a dotted path. */
  paths: string[];
  /** Fields that should be marked editable="create-only" — name and
   *  namespace are immutable after create. */
  createOnly: string[];
}

export interface KindGVK {
  group: string;
  version: string;
  kind: string;
  /** The plural REST resource name; used by `api.applyResource`. */
  resource: string;
}

const KIND_GVK: Record<SupportedKind, KindGVK> = {
  ConfigMap: { group: "", version: "v1", kind: "ConfigMap", resource: "configmaps" },
  Secret: { group: "", version: "v1", kind: "Secret", resource: "secrets" },
  Service: { group: "", version: "v1", kind: "Service", resource: "services" },
  Ingress: { group: "networking.k8s.io", version: "v1", kind: "Ingress", resource: "ingresses" },
};

export function getKindGVK(kind: SupportedKind): KindGVK {
  return KIND_GVK[kind];
}

const KIND_SPECS: Record<SupportedKind, KindSpec> = {
  ConfigMap: {
    metadata: ["name", "namespace", "labels", "annotations"],
    paths: ["data", "binaryData", "immutable"],
    createOnly: ["metadata.name", "metadata.namespace"],
  },
  Secret: {
    metadata: ["name", "namespace", "labels", "annotations"],
    paths: ["type", "data", "stringData", "immutable"],
    createOnly: ["metadata.name", "metadata.namespace"],
  },
  Service: {
    metadata: ["name", "namespace", "labels", "annotations"],
    paths: [
      "spec.type",
      "spec.selector",
      "spec.ports",
      "spec.clusterIP",
      "spec.externalTrafficPolicy",
      "spec.internalTrafficPolicy",
      "spec.sessionAffinity",
      "spec.loadBalancerSourceRanges",
      "spec.loadBalancerClass",
      "spec.ipFamilies",
      "spec.ipFamilyPolicy",
    ],
    createOnly: [
      "metadata.name",
      "metadata.namespace",
      "spec.clusterIP",
      // loadBalancerClass is also immutable on update per the K8s
      // Service controller — picking a different LB implementation
      // requires recreating the Service.
      "spec.loadBalancerClass",
    ],
  },
  Ingress: {
    metadata: ["name", "namespace", "labels", "annotations"],
    paths: ["spec.ingressClassName", "spec.rules", "spec.tls", "spec.defaultBackend"],
    createOnly: ["metadata.name", "metadata.namespace"],
  },
};

export function isSupportedKind(kind: string): kind is SupportedKind {
  return kind in KIND_SPECS;
}

export function getCreateOnlyPaths(kind: SupportedKind): string[] {
  return KIND_SPECS[kind].createOnly;
}

/** Return a narrowed schema whose `properties` only contain the
 *  metadata + spec subtrees the form should expose. Operates on the
 *  result of `derefIfNeeded` against the GVK root, so the input is
 *  expected to have a `properties` map (apiVersion/kind/metadata/
 *  spec/data/etc.). Falls back to the input untouched when the
 *  shape is unexpected (e.g. a CRD piped in by accident). */
export function filterSchemaForKind(schema: JSONSchema, kind: SupportedKind): JSONSchema {
  const spec = KIND_SPECS[kind];
  if (!schema || typeof schema !== "object") return schema;
  if (!schema.properties) return schema;

  const next: JSONSchema = { ...schema, properties: {}, required: [] };
  const props = schema.properties;

  // metadata: narrow to the allowlisted sub-fields.
  if (props.metadata) {
    next.properties!.metadata = filterMetadata(props.metadata, spec.metadata);
  }

  // Top-level allowlisted paths. Each path may be top-level
  // (`data`) or nested (`spec.type`); group nested-under-spec paths
  // and emit one filtered `spec` object covering them.
  const topLevel = new Set<string>();
  const specSubFields = new Set<string>();
  for (const p of spec.paths) {
    if (p.startsWith("spec.")) specSubFields.add(p.slice("spec.".length));
    else topLevel.add(p);
  }

  for (const key of topLevel) {
    if (props[key]) next.properties![key] = props[key];
  }

  if (specSubFields.size > 0 && props.spec) {
    next.properties!.spec = filterByKeys(props.spec, specSubFields);
  }

  // Preserve `required` only for fields we still surface — the
  // renderer reads required[] off the parent.
  const surfaced = new Set(Object.keys(next.properties!));
  next.required = (schema.required ?? []).filter((r) => surfaced.has(r));

  return next;
}

function filterMetadata(metadata: JSONSchema, allowed: string[]): JSONSchema {
  if (!metadata || typeof metadata !== "object" || !metadata.properties) return metadata;
  const next: JSONSchema = { ...metadata, properties: {} };
  for (const key of allowed) {
    if (metadata.properties[key]) next.properties![key] = metadata.properties[key];
  }
  // Required gets pruned to the surfaced set; metadata typically
  // doesn't list required for these fields anyway.
  if (Array.isArray(metadata.required)) {
    next.required = metadata.required.filter((r) => allowed.includes(r));
  }
  return next;
}

function filterByKeys(schema: JSONSchema, allowed: Set<string>): JSONSchema {
  if (!schema || typeof schema !== "object" || !schema.properties) return schema;
  const next: JSONSchema = { ...schema, properties: {} };
  for (const key of allowed) {
    if (schema.properties[key]) next.properties![key] = schema.properties[key];
  }
  if (Array.isArray(schema.required)) {
    next.required = schema.required.filter((r) => allowed.has(r));
  }
  return next;
}
