// schemaForm/k8sAllowlist.ts — per-kind narrowing of the K8s
// OpenAPI v3 schema. Operators should never see status,
// managedFields, creationTimestamp, uid, resourceVersion, etc.;
// per #116 we filter the schema BEFORE the walker runs so the
// renderer never even considers those fields.
//
// Allowlist (not deny-list) per design call-out — predictable and
// auditable; if K8s adds a new noise field, it stays hidden by
// default until we explicitly allowlist it.
//
// Sectioned shape (added for the section-grouping refactor): each
// kind splits its allowlisted paths into 3 groups so the renderer
// can present them as primary (always visible) / metadata (collapsed)
// / advanced (hidden behind toggle). Path order within each group
// drives display order. The flat list of "all allowlisted paths"
// (primary ∪ metadata ∪ advanced) is what filterSchemaForKind hands
// to the schema-narrowing pass.

import type { FieldSection, JSONSchema } from "./types";

export type SupportedKind = "ConfigMap" | "Secret" | "Service" | "Ingress";

/** Curated layout for a single kind. Each section's `paths` array
 *  is dotted, rooted at the K8s object (e.g. `metadata.name`,
 *  `data`, `spec.type`). Path order within an array drives display
 *  order in that section. */
interface KindSpec {
  primary: {
    /** Section header — kind-specific so it tells the operator what
     *  they're editing ("Data" for ConfigMap, "Networking" for
     *  Service, "Routing & TLS" for Ingress). */
    label: string;
    paths: string[];
  };
  metadata: {
    label: string;
    paths: string[];
  };
  advanced: {
    label: string;
    paths: string[];
  };
  /** Fields the renderer marks editable="create-only" — name and
   *  namespace are immutable after create; spec.clusterIP and
   *  spec.loadBalancerClass are also immutable on Service updates. */
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
    primary: {
      label: "Data",
      // The whole point of a ConfigMap.
      paths: ["data", "binaryData"],
    },
    metadata: {
      label: "Metadata",
      // Annotations live last so name/namespace/labels aren't
      // visually crowded by controller-set annotations.
      paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
    },
    advanced: {
      label: "Advanced",
      // immutable=true is a one-way door (apiserver rejects mutation
      // after) — hide behind the advanced toggle so it can't be
      // toggled accidentally from the primary section.
      paths: ["immutable"],
    },
    createOnly: ["metadata.name", "metadata.namespace"],
  },
  Secret: {
    primary: {
      label: "Data",
      // type drives data semantics (Opaque vs kubernetes.io/tls vs
      // kubernetes.io/dockerconfigjson etc.); data + stringData are
      // the actual payload. base64 layer applies on top of `data`.
      paths: ["type", "data", "stringData"],
    },
    metadata: {
      label: "Metadata",
      paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
    },
    advanced: {
      label: "Advanced",
      paths: ["immutable"],
    },
    createOnly: ["metadata.name", "metadata.namespace"],
  },
  Service: {
    primary: {
      label: "Networking",
      // The "why isn't this routing traffic?" investigation surface:
      // type (ClusterIP / NodePort / LoadBalancer / ExternalName),
      // selector (label selector matching backing pods), ports
      // (port → targetPort + protocol).
      paths: ["spec.type", "spec.selector", "spec.ports"],
    },
    metadata: {
      label: "Metadata",
      paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
    },
    advanced: {
      label: "Advanced",
      // Traffic policy + IP family + LB-specific knobs operators
      // touch maybe once per Service lifetime. clusterIP is also
      // immutable on update (createOnly enforces read-only after
      // create) but we still surface it so it's not dropped from
      // the form silently.
      paths: [
        "spec.clusterIP",
        "spec.externalTrafficPolicy",
        "spec.internalTrafficPolicy",
        "spec.sessionAffinity",
        "spec.ipFamilies",
        "spec.ipFamilyPolicy",
        "spec.loadBalancerClass",
        "spec.loadBalancerSourceRanges",
      ],
    },
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
    primary: {
      label: "Routing & TLS",
      // ingressClassName picks the controller (nginx / alb / etc.);
      // rules carry host + path → backend; tls carries cert refs.
      paths: ["spec.ingressClassName", "spec.rules", "spec.tls"],
    },
    metadata: {
      label: "Metadata",
      paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
    },
    advanced: {
      label: "Advanced",
      // defaultBackend is the catch-all when no rule matches; rare
      // in practice but valid.
      paths: ["spec.defaultBackend"],
    },
    createOnly: ["metadata.name", "metadata.namespace"],
  },
};

export function isSupportedKind(kind: string): kind is SupportedKind {
  return kind in KIND_SPECS;
}

export function getCreateOnlyPaths(kind: SupportedKind): string[] {
  return KIND_SPECS[kind].createOnly;
}

/** Section labels for SchemaForm's grouped renderer. The primary
 *  label is kind-specific; metadata + advanced are constant. */
export function getSectionLabels(kind: SupportedKind): {
  primary: string;
  metadata: string;
  advanced: string;
} {
  const s = KIND_SPECS[kind];
  return {
    primary: s.primary.label,
    metadata: s.metadata.label,
    advanced: s.advanced.label,
  };
}

/** Build a path → { section, displayOrder } resolver for the given
 *  kind. The walker calls this per top-level descriptor it emits;
 *  unmatched paths return undefined and the renderer puts them in a
 *  "Other" fallback section so future schema fields aren't dropped
 *  silently. Dev-mode duplicate-path detection runs once at build
 *  time and throws — a path may not legitimately appear in two
 *  section lists for the same kind. */
export function getSectionResolver(
  kind: SupportedKind,
): (path: string[]) => { section: FieldSection; displayOrder: number } | undefined {
  const spec = KIND_SPECS[kind];
  const map = new Map<string, { section: FieldSection; displayOrder: number }>();
  const stamp = (paths: string[], section: FieldSection) => {
    paths.forEach((p, i) => {
      if (map.has(p)) {
        // Authoring bug (same path in two sections). Surface loudly
        // in dev so the duplicate is fixed at the spec level rather
        // than silently winning the last write.
        throw new Error(
          `[k8sAllowlist] duplicate path "${p}" for ${kind}: already in section "${map.get(p)!.section}", trying to add to "${section}"`,
        );
      }
      map.set(p, { section, displayOrder: i });
    });
  };
  stamp(spec.primary.paths, "primary");
  stamp(spec.metadata.paths, "metadata");
  stamp(spec.advanced.paths, "advanced");
  return (path: string[]) => map.get(path.join("."));
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

  // Collect every allowlisted path across all sections — this is
  // what the schema narrowing operates on.
  const allPaths = [
    ...spec.primary.paths,
    ...spec.metadata.paths,
    ...spec.advanced.paths,
  ];

  const next: JSONSchema = { ...schema, properties: {}, required: [] };
  const props = schema.properties;

  // metadata: narrow to the allowlisted sub-fields. metadata paths
  // are always of shape "metadata.<key>", so we strip the prefix.
  const metadataKeys = spec.metadata.paths
    .filter((p) => p.startsWith("metadata."))
    .map((p) => p.slice("metadata.".length));
  if (props.metadata && metadataKeys.length > 0) {
    next.properties!.metadata = filterMetadata(props.metadata, metadataKeys);
  }

  // Top-level + nested-under-spec paths. Everything that doesn't
  // start with `metadata.` either lives at the root (e.g. `data`,
  // `binaryData`, `immutable`, `type`, `stringData`) or under spec.
  const topLevel = new Set<string>();
  const specSubFields = new Set<string>();
  for (const p of allPaths) {
    if (p.startsWith("metadata.")) continue;
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
