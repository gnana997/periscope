// k8sAllowlist.ts — per-kind sectioning + filtering for the schema-
// aware form editor.
//
// Two responsibilities:
//
//  1. **Section layout** — declare the L1 sections each kind splits
//     into (e.g. ConfigMap has Data / Metadata / Advanced; Deployment
//     has Containers / Volumes / Strategy / Pod metadata / Metadata /
//     Selector / Advanced). The renderer in `SchemaForm.tsx` reads
//     the section list to render `<section>` + `<details>` blocks
//     in the order declared here. Sub-section paths inside arrays of
//     objects (e.g. per-container probes / lifecycle / advanced)
//     also live in this table — see `Deployment.subSections` below.
//
//  2. **Schema narrowing** — `filterSchemaForKind` collects every
//     allowlisted path across every section, builds a path trie, and
//     prunes the OpenAPI schema down to those properties (recursively,
//     with `*` matching any array-element index). This keeps form-
//     mode focused on what we know how to render and hides noise
//     like status / managedFields.
//
// Path syntax: dotted, rooted at the K8s object root.
//   - `metadata.name`                        — depth 2 simple
//   - `spec.template.spec.containers`        — depth N simple
//   - `spec.template.spec.containers.*.image` — `*` is the array-
//     element boundary; the resolver / filter both handle it.

import type { FieldSection, JSONSchema } from "./types";

export type SupportedKind =
  | "ConfigMap"
  | "Secret"
  | "Service"
  | "Ingress"
  | "Deployment"
  | "StatefulSet";

/** One L1 section inside a kind's form. */
export interface SectionSpec {
  /** Identifier — unique within a KindSpec. The walker stamps this on
   *  matching descriptors as `section`. */
  id: FieldSection;
  /** Header text rendered above the section block. */
  label: string;
  /** Dotted paths whose descriptors land in this section. Order
   *  drives display order within the section. */
  paths: string[];
  /** When true, the section renders as `<section>` (always open).
   *  Default false → `<details>` (collapsed). Exactly one section
   *  per kind should be marked defaultOpen. */
  defaultOpen?: boolean;
  /** When true, the section starts open if any descriptor in its
   *  bucket has populated content. Useful for Volumes, where editing
   *  volumeMounts without seeing volumes is a footgun. */
  openWhenPopulated?: boolean;
  /** When true, the section renders a `(N)` field count in its
   *  summary — telegraphs "there's stuff inside" without forcing
   *  the operator to expand. */
  showCount?: boolean;
}

/** Sub-section grouping inside an array-of-objects ROW (e.g. per-
 *  container fields). The walker stamps row children with the
 *  matching sub-section id; the array-of-objects renderer reads
 *  these to draw a smaller stack of blocks INSIDE each row. */
export interface RowSubSection {
  id: FieldSection;
  label: string;
  /** Child field paths relative to the row item. e.g.
   *  `["image", "name", "ports"]` for container primary. */
  paths: string[];
  defaultOpen?: boolean;
  openWhenPopulated?: boolean;
  showCount?: boolean;
}

/** Curated layout for a single kind. */
export interface KindSpec {
  sections: SectionSpec[];
  /** Per-array-of-objects-parent sub-section table. The key is the
   *  parent path (e.g. `spec.template.spec.containers`); the value
   *  is the ordered sub-section list rendered inside each row. */
  subSections?: Record<string, RowSubSection[]>;
  /** Fields the renderer marks editable="create-only" — name and
   *  namespace are immutable post-create; some service / selector
   *  paths are also immutable and worth flagging. */
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
  Deployment: { group: "apps", version: "v1", kind: "Deployment", resource: "deployments" },
  StatefulSet: { group: "apps", version: "v1", kind: "StatefulSet", resource: "statefulsets" },
};

export function getKindGVK(kind: SupportedKind): KindGVK {
  return KIND_GVK[kind];
}

// ─── Per-container row sub-sections (shared by Deployment + StatefulSet) ──

const CONTAINER_SUBSECTIONS: RowSubSection[] = [
  {
    id: "primary",
    label: "Primary",
    paths: ["name", "image", "imagePullPolicy", "ports", "env", "envFrom", "resources"],
  },
  {
    id: "probes",
    label: "Probes & lifecycle",
    paths: ["livenessProbe", "readinessProbe", "startupProbe", "lifecycle"],
  },
  {
    id: "mounts",
    label: "Volume mounts",
    paths: ["volumeMounts"],
    openWhenPopulated: true,
  },
  {
    id: "advanced",
    label: "Container advanced",
    paths: [
      "command",
      "args",
      "workingDir",
      "securityContext",
      "terminationMessagePath",
      "terminationMessagePolicy",
      "tty",
      "stdin",
      "stdinOnce",
    ],
    showCount: true,
  },
];

// initContainers — same shape as containers minus lifecycle hooks
// (apiserver doesn't run them for init containers anyway).
const INIT_CONTAINER_SUBSECTIONS: RowSubSection[] = CONTAINER_SUBSECTIONS.map((s) =>
  s.id === "probes"
    ? { ...s, label: "Probes", paths: s.paths.filter((p) => p !== "lifecycle") }
    : s,
);

// ─── KIND_SPECS ──────────────────────────────────────────────────────────

const KIND_SPECS: Record<SupportedKind, KindSpec> = {
  ConfigMap: {
    sections: [
      { id: "primary", label: "Data", paths: ["data", "binaryData"] },
      {
        id: "metadata",
        label: "Metadata",
        paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
      },
      { id: "advanced", label: "Advanced", paths: ["immutable"], showCount: true },
    ],
    createOnly: ["metadata.name", "metadata.namespace"],
  },

  Secret: {
    sections: [
      { id: "primary", label: "Data", paths: ["type", "data", "stringData"] },
      {
        id: "metadata",
        label: "Metadata",
        paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
      },
      { id: "advanced", label: "Advanced", paths: ["immutable"], showCount: true },
    ],
    createOnly: ["metadata.name", "metadata.namespace"],
  },

  Service: {
    sections: [
      {
        id: "primary",
        label: "Networking",
        paths: ["spec.type", "spec.selector", "spec.ports"],
      },
      {
        id: "metadata",
        label: "Metadata",
        paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
      },
      {
        id: "advanced",
        label: "Advanced",
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
        showCount: true,
      },
    ],
    createOnly: [
      "metadata.name",
      "metadata.namespace",
      "spec.clusterIP",
      "spec.loadBalancerClass",
    ],
  },

  Ingress: {
    sections: [
      {
        id: "primary",
        label: "Routing & TLS",
        paths: ["spec.ingressClassName", "spec.rules", "spec.tls"],
      },
      {
        id: "metadata",
        label: "Metadata",
        paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
      },
      {
        id: "advanced",
        label: "Advanced",
        paths: ["spec.defaultBackend"],
        showCount: true,
      },
    ],
    createOnly: ["metadata.name", "metadata.namespace"],
  },

  // ─── Deployment ──────────────────────────────────────────────────────
  Deployment: {
    sections: [
      {
        id: "primary",
        label: "Containers",
        paths: [
          "spec.replicas",
          "spec.template.spec.containers",
          "spec.template.spec.initContainers",
        ],
      },
      {
        id: "volumes",
        label: "Volumes",
        paths: ["spec.template.spec.volumes"],
        openWhenPopulated: true,
      },
      {
        id: "strategy",
        label: "Strategy & lifecycle",
        paths: [
          "spec.strategy",
          "spec.minReadySeconds",
          "spec.revisionHistoryLimit",
          "spec.progressDeadlineSeconds",
          "spec.paused",
          "spec.template.spec.terminationGracePeriodSeconds",
          "spec.template.spec.restartPolicy",
        ],
      },
      {
        id: "podMetadata",
        label: "Pod metadata",
        paths: ["spec.template.metadata.labels", "spec.template.metadata.annotations"],
      },
      {
        id: "metadata",
        label: "Metadata",
        paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
      },
      {
        id: "selector",
        label: "Selector",
        paths: ["spec.selector"],
      },
      {
        id: "advanced",
        label: "Advanced",
        paths: [
          "spec.template.spec.serviceAccountName",
          "spec.template.spec.automountServiceAccountToken",
          "spec.template.spec.imagePullSecrets",
          "spec.template.spec.nodeSelector",
          "spec.template.spec.affinity",
          "spec.template.spec.tolerations",
          "spec.template.spec.topologySpreadConstraints",
          "spec.template.spec.securityContext",
          "spec.template.spec.hostNetwork",
          "spec.template.spec.hostPID",
          "spec.template.spec.hostIPC",
          "spec.template.spec.dnsPolicy",
          "spec.template.spec.dnsConfig",
          "spec.template.spec.priorityClassName",
          "spec.template.spec.runtimeClassName",
        ],
        showCount: true,
      },
    ],
    subSections: {
      "spec.template.spec.containers": CONTAINER_SUBSECTIONS,
      "spec.template.spec.initContainers": INIT_CONTAINER_SUBSECTIONS,
    },
    createOnly: ["metadata.name", "metadata.namespace", "spec.selector"],
  },

  // ─── StatefulSet ─────────────────────────────────────────────────────
  // Adds a Persistent storage section for volumeClaimTemplates +
  // retention policy. serviceName lives in primary (immutable, but
  // conceptually part of "what this StatefulSet runs"). Strategy
  // section uses updateStrategy instead of strategy.
  StatefulSet: {
    sections: [
      {
        id: "primary",
        label: "Containers",
        paths: [
          "spec.replicas",
          "spec.serviceName",
          "spec.template.spec.containers",
          "spec.template.spec.initContainers",
        ],
      },
      {
        id: "volumes",
        label: "Volumes",
        paths: ["spec.template.spec.volumes"],
        openWhenPopulated: true,
      },
      {
        id: "persistentStorage",
        label: "Persistent storage",
        paths: [
          "spec.volumeClaimTemplates",
          "spec.persistentVolumeClaimRetentionPolicy",
          "spec.ordinals",
        ],
        openWhenPopulated: true,
      },
      {
        id: "strategy",
        label: "Strategy & lifecycle",
        paths: [
          "spec.updateStrategy",
          "spec.podManagementPolicy",
          "spec.minReadySeconds",
          "spec.revisionHistoryLimit",
          "spec.template.spec.terminationGracePeriodSeconds",
          "spec.template.spec.restartPolicy",
        ],
      },
      {
        id: "podMetadata",
        label: "Pod metadata",
        paths: ["spec.template.metadata.labels", "spec.template.metadata.annotations"],
      },
      {
        id: "metadata",
        label: "Metadata",
        paths: ["metadata.name", "metadata.namespace", "metadata.labels", "metadata.annotations"],
      },
      {
        id: "selector",
        label: "Selector",
        paths: ["spec.selector"],
      },
      {
        id: "advanced",
        label: "Advanced",
        paths: [
          "spec.template.spec.serviceAccountName",
          "spec.template.spec.automountServiceAccountToken",
          "spec.template.spec.imagePullSecrets",
          "spec.template.spec.nodeSelector",
          "spec.template.spec.affinity",
          "spec.template.spec.tolerations",
          "spec.template.spec.topologySpreadConstraints",
          "spec.template.spec.securityContext",
          "spec.template.spec.hostNetwork",
          "spec.template.spec.hostPID",
          "spec.template.spec.hostIPC",
          "spec.template.spec.dnsPolicy",
          "spec.template.spec.dnsConfig",
          "spec.template.spec.priorityClassName",
          "spec.template.spec.runtimeClassName",
        ],
        showCount: true,
      },
    ],
    subSections: {
      "spec.template.spec.containers": CONTAINER_SUBSECTIONS,
      "spec.template.spec.initContainers": INIT_CONTAINER_SUBSECTIONS,
    },
    createOnly: [
      "metadata.name",
      "metadata.namespace",
      "spec.selector",
      "spec.serviceName",
      "spec.podManagementPolicy",
      "spec.volumeClaimTemplates",
    ],
  },
};

// ─── Public API ──────────────────────────────────────────────────────────

export function isSupportedKind(kind: string): kind is SupportedKind {
  return kind in KIND_SPECS;
}

export function getCreateOnlyPaths(kind: SupportedKind): string[] {
  return KIND_SPECS[kind].createOnly;
}

/** Return the section list for a kind, in render order. The renderer
 *  iterates this to draw the L1 layout. */
export function getSections(kind: SupportedKind): SectionSpec[] {
  return KIND_SPECS[kind].sections;
}

/** Build a path → { section, displayOrder } resolver for the given
 *  kind. The walker calls this per descriptor it emits during the
 *  schema walk; unmatched paths return undefined and the renderer
 *  puts them in a "Other" fallback section so future schema fields
 *  aren't dropped silently.
 *
 *  Paths use `*` as the array-element boundary marker. So
 *  `spec.template.spec.containers.*.image` matches the per-row
 *  child stamped during the array-of-objects walk. The walker passes
 *  these synthetic paths via parent-path concatenation; see
 *  walker.walkObject for the construction.
 *
 *  Includes per-row sub-section stamps too — the array-of-objects
 *  renderer consults `descriptor.section` on row children to do its
 *  L2 grouping. */
export function getSectionResolver(
  kind: SupportedKind,
): (path: string[]) => { section: FieldSection; displayOrder: number } | undefined {
  const spec = KIND_SPECS[kind];
  const map = new Map<string, { section: FieldSection; displayOrder: number }>();
  const stamp = (path: string, section: FieldSection, displayOrder: number) => {
    if (map.has(path)) {
      throw new Error(
        `[k8sAllowlist] duplicate path "${path}" for ${kind}: already in section "${map.get(path)!.section}", trying to add to "${section}"`,
      );
    }
    map.set(path, { section, displayOrder });
  };
  // L1: top-level sections.
  for (const s of spec.sections) {
    s.paths.forEach((p, i) => stamp(p, s.id, i));
  }
  // L2: per-row sub-sections inside arrays of objects. Stored under
  // a synthetic path "<parent>.*.<child>" so the resolver matches
  // when the walker descends into the array-element synthesizing
  // exactly that path.
  if (spec.subSections) {
    for (const [parentPath, subs] of Object.entries(spec.subSections)) {
      for (const sub of subs) {
        sub.paths.forEach((childPath, i) =>
          stamp(`${parentPath}.*.${childPath}`, sub.id, i),
        );
      }
    }
  }
  return (path) => map.get(path.join("."));
}

/** Per-array-of-objects sub-section list. Renderer consults this when
 *  rendering an array row to know how to group the row's children. */
export function getRowSubSections(
  kind: SupportedKind,
  parentPath: string,
): RowSubSection[] | undefined {
  return KIND_SPECS[kind].subSections?.[parentPath];
}

// ─── Schema narrowing ────────────────────────────────────────────────────

/** Return a narrowed schema whose `properties` only contain the
 *  allowlisted paths the form should expose. Recursive — handles
 *  arbitrary depth and array-element wildcards (`*`). */
export function filterSchemaForKind(schema: JSONSchema, kind: SupportedKind): JSONSchema {
  const spec = KIND_SPECS[kind];
  if (!schema || typeof schema !== "object") return schema;

  // Collect every path across all sections + all sub-section row paths.
  const allPaths: string[] = [];
  for (const s of spec.sections) allPaths.push(...s.paths);
  if (spec.subSections) {
    for (const [parent, subs] of Object.entries(spec.subSections)) {
      for (const sub of subs) {
        for (const child of sub.paths) {
          allPaths.push(`${parent}.*.${child}`);
        }
      }
    }
  }

  const trie = buildPathTrie(allPaths);
  return narrowBySectionTrie(schema, trie);
}

/** Path trie node. `terminal=true` means "include this node and the
 *  whole subtree below it." Children handle deeper allowlist paths. */
interface TrieNode {
  terminal: boolean;
  children: Map<string, TrieNode>;
}

function buildPathTrie(paths: string[]): TrieNode {
  const root: TrieNode = { terminal: false, children: new Map() };
  for (const p of paths) {
    const segs = p.split(".");
    let node = root;
    for (const seg of segs) {
      let next = node.children.get(seg);
      if (!next) {
        next = { terminal: false, children: new Map() };
        node.children.set(seg, next);
      }
      node = next;
    }
    node.terminal = true;
  }
  return root;
}

function narrowBySectionTrie(schema: JSONSchema, trie: TrieNode): JSONSchema {
  if (!schema || typeof schema !== "object") return schema;
  // K8s wraps refs in `allOf:[{$ref:...}]` envelopes for many object
  // types; can't narrow until the ref is resolved. Leave intact —
  // walker resolves on its own and the trie still narrows at deeper
  // levels via the resolver's path-based filtering.
  if (schema.allOf || schema.$ref) return schema;

  if (schema.type === "array") {
    const wildcard = trie.children.get("*");
    if (!wildcard) return schema;
    const items = schema.items;
    if (!items || typeof items !== "object") return schema;
    return { ...schema, items: narrowBySectionTrie(items, wildcard) };
  }

  if (!schema.properties) return schema;
  const next: JSONSchema = { ...schema, properties: {} };
  for (const [key, child] of Object.entries(schema.properties)) {
    const childTrie = trie.children.get(key);
    if (!childTrie) continue;
    if (childTrie.terminal && childTrie.children.size === 0) {
      // Whole subtree included.
      next.properties![key] = child;
    } else {
      // Recurse to narrow further.
      next.properties![key] = narrowBySectionTrie(child as JSONSchema, childTrie);
    }
  }
  // Preserve `required` only for fields we still surface.
  const surfaced = new Set(Object.keys(next.properties!));
  if (Array.isArray(schema.required)) {
    next.required = schema.required.filter((r) => surfaced.has(r));
  }
  return next;
}
