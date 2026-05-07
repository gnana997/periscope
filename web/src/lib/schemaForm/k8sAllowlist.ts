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
// Path syntax (used by `KindSpec.paths` and `KindSpec.createOnly`):
//
//   - Dotted segments rooted at the K8s object:
//       `metadata.name`, `data`, `spec.type`
//   - `[*]` segment for "descend into array items":
//       `spec.template.spec.containers[*].image`
//   - A path is the leaf of a subtree: when the filter walks down
//     to that node, the schema below it is surfaced unchanged.
//
// To narrow inside K8s `{allOf:[{$ref:...}]}` envelopes (which is
// how every K8s resource wraps `metadata` and `spec`), the filter
// needs the same `resolveRef` the walker uses. Pass it via
// `filterSchemaForKind(schema, kind, { resolveRef })`. When omitted
// the filter degrades gracefully — refs/allOfs aren't peeked into,
// so paths underneath them surface their parent envelope as-is.

import { mergeAllOf } from "./allOfMerger";
import type { JSONSchema } from "./types";

export type SupportedKind =
  | "ConfigMap"
  | "Secret"
  | "Service"
  | "Ingress"
  | "Deployment";

interface KindSpec {
  /** Sub-fields of `metadata` to surface. Sugar for prefixing each
   *  with `metadata.` and adding to `paths`. Kept separate because
   *  every kind shares the same metadata allowlist shape. */
  metadata: string[];
  /** Allowlisted paths into the K8s object root. Each is a dotted
   *  path; segments may include `[*]` to descend into array items.
   *  Paths terminate at the surfaced subtree — anything below the
   *  terminal segment is rendered in full. */
  paths: string[];
  /** Paths to mark `editable="create-only"`. Same syntax as `paths`
   *  but consumed by the walker, not the filter. */
  createOnly: string[];
  /** Paths to mark `advanced: true` (collapsed-by-default in the
   *  renderer; auto-opens when the value is non-empty). Optional;
   *  most kinds have no advanced fields. Same path syntax as
   *  `paths`, but matching is currently against the descriptor's
   *  absolute path — paths inside array-item rows like
   *  `containers[*].securityContext` won't match. */
  advanced?: string[];
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
  // Deployment — curated PodSpec subset.
  //
  // Sibling-encoded oneOfs render as proper discriminator pickers
  // via the K8s hint table in `k8sDiscriminatorHints.ts`:
  //   - PROPERTY-level (probes, lifecycle hooks, env.valueFrom):
  //     Shape B discriminator with sharedChildren.
  //   - ARRAY-ITEM (volumes[], envFrom[]): array-of-discriminators
  //     where each row is a per-row picker.
  //
  // Pod-level admin surface (securityContext, affinity, tolerations,
  // topologySpreadConstraints) is allowlisted but flagged as
  // `advanced` — the renderer collapses these by default and
  // auto-opens when the value is non-empty.
  //
  // initContainers reuses the same Container shape as containers;
  // multi-container forms are now manageable thanks to per-row
  // collapse in array-of-objects.
  //
  // Still excluded:
  //   - container-level securityContext: advancedPaths matching is
  //     currently absolute-only, so paths inside `containers[*]`
  //     don't match. Drop to YAML for now.
  //
  // spec.selector is create-only — the apiserver rejects Deployment
  // selector mutations after create.
  Deployment: {
    metadata: ["name", "namespace", "labels", "annotations"],
    paths: [
      "spec.replicas",
      "spec.selector",
      "spec.minReadySeconds",
      "spec.revisionHistoryLimit",
      "spec.progressDeadlineSeconds",
      "spec.paused",
      "spec.strategy.type",
      "spec.strategy.rollingUpdate",
      "spec.template.metadata.labels",
      "spec.template.metadata.annotations",
      "spec.template.spec.restartPolicy",
      "spec.template.spec.serviceAccountName",
      "spec.template.spec.nodeSelector",
      "spec.template.spec.terminationGracePeriodSeconds",
      "spec.template.spec.volumes",
      "spec.template.spec.affinity",
      "spec.template.spec.tolerations",
      "spec.template.spec.topologySpreadConstraints",
      "spec.template.spec.securityContext",
      "spec.template.spec.containers[*].name",
      "spec.template.spec.containers[*].image",
      "spec.template.spec.containers[*].imagePullPolicy",
      "spec.template.spec.containers[*].command",
      "spec.template.spec.containers[*].args",
      "spec.template.spec.containers[*].workingDir",
      "spec.template.spec.containers[*].ports",
      "spec.template.spec.containers[*].env",
      "spec.template.spec.containers[*].envFrom",
      "spec.template.spec.containers[*].resources",
      "spec.template.spec.containers[*].volumeMounts",
      "spec.template.spec.containers[*].livenessProbe",
      "spec.template.spec.containers[*].readinessProbe",
      "spec.template.spec.containers[*].startupProbe",
      "spec.template.spec.containers[*].lifecycle",
      "spec.template.spec.containers[*].terminationMessagePath",
      "spec.template.spec.containers[*].terminationMessagePolicy",
      "spec.template.spec.containers[*].tty",
      "spec.template.spec.containers[*].stdin",
      "spec.template.spec.containers[*].stdinOnce",
      // initContainers reuses the same Container subset above —
      // duplicating the path list is verbose but keeps the
      // allowlist explicit (and would let us diverge if we ever
      // need to, e.g. excluding `lifecycle` for init containers).
      "spec.template.spec.initContainers[*].name",
      "spec.template.spec.initContainers[*].image",
      "spec.template.spec.initContainers[*].imagePullPolicy",
      "spec.template.spec.initContainers[*].command",
      "spec.template.spec.initContainers[*].args",
      "spec.template.spec.initContainers[*].workingDir",
      "spec.template.spec.initContainers[*].env",
      "spec.template.spec.initContainers[*].envFrom",
      "spec.template.spec.initContainers[*].resources",
      "spec.template.spec.initContainers[*].volumeMounts",
      "spec.template.spec.initContainers[*].terminationMessagePath",
      "spec.template.spec.initContainers[*].terminationMessagePolicy",
    ],
    createOnly: ["metadata.name", "metadata.namespace", "spec.selector"],
    advanced: [
      "spec.template.spec.affinity",
      "spec.template.spec.tolerations",
      "spec.template.spec.topologySpreadConstraints",
      "spec.template.spec.securityContext",
    ],
  },
};

export function isSupportedKind(kind: string): kind is SupportedKind {
  return kind in KIND_SPECS;
}

export function getCreateOnlyPaths(kind: SupportedKind): string[] {
  return KIND_SPECS[kind].createOnly;
}

export function getAdvancedPaths(kind: SupportedKind): string[] {
  return KIND_SPECS[kind].advanced ?? [];
}

export interface FilterOptions {
  /** Resolve `$ref` strings the same way the walker does. Required
   *  to narrow inside K8s `{allOf:[{$ref:...}]}` envelopes. Without
   *  it the filter degrades — anything wrapped in a ref is surfaced
   *  whole and the walker handles it.
   *
   *  Concretely: K8s spells `metadata` as `{allOf:[{$ref: ObjectMeta}]}`
   *  in OpenAPI v3. To narrow it down to `{name, namespace, labels,
   *  annotations}` we have to resolve the ref and merge the allOf
   *  before pruning properties. */
  resolveRef?: (ref: string) => JSONSchema | undefined;
}

/** Return a narrowed schema whose `properties` (recursively) only
 *  contain the subtrees declared by the kind's allowlist. */
export function filterSchemaForKind(
  schema: JSONSchema,
  kind: SupportedKind,
  options: FilterOptions = {},
): JSONSchema {
  if (!schema || typeof schema !== "object") return schema;
  const spec = KIND_SPECS[kind];
  const allPaths = [
    ...spec.metadata.map((m) => `metadata.${m}`),
    ...spec.paths,
  ];
  const trie = buildTrie(allPaths);
  return filterBySchemaTrie(schema, trie, options);
}

// ── Path trie ────────────────────────────────────────────────────
//
// Each node carries a Map of child-segment → child-trie plus a
// `terminal` flag. A `terminal` node means "the operator wants this
// whole subtree surfaced unchanged" — descendants below it are not
// pruned. A non-terminal node with children means "narrow to these
// children only."
//
// Paths can include `[*]` segments which the filter consumes when it
// reaches an `array` schema, descending into `items`.
//
// Example: `["metadata.name", "spec.template.spec.containers[*].image"]`
//   metadata
//     name (terminal)
//   spec
//     template
//       spec
//         containers
//           [*]
//             image (terminal)

interface PathTrie {
  children: Map<string, PathTrie>;
  /** True when a path terminated here. Subtree below this node is
   *  surfaced unchanged. */
  terminal: boolean;
}

function newTrieNode(): PathTrie {
  return { children: new Map(), terminal: false };
}

function buildTrie(paths: string[]): PathTrie {
  const root = newTrieNode();
  for (const p of paths) {
    let node = root;
    for (const seg of splitPath(p)) {
      let next = node.children.get(seg);
      if (!next) {
        next = newTrieNode();
        node.children.set(seg, next);
      }
      node = next;
    }
    node.terminal = true;
  }
  return root;
}

/** Split a dotted path into segments, treating a `[*]` suffix on a
 *  segment as its own segment. So `containers[*].image` →
 *  `["containers", "[*]", "image"]`. */
function splitPath(path: string): string[] {
  const out: string[] = [];
  for (const part of path.split(".")) {
    let rest = part;
    while (rest.length > 0) {
      const open = rest.indexOf("[");
      if (open < 0) {
        out.push(rest);
        break;
      }
      if (open > 0) out.push(rest.slice(0, open));
      const close = rest.indexOf("]", open);
      if (close < 0) {
        // Malformed — push the rest as-is and stop.
        out.push(rest);
        break;
      }
      out.push(rest.slice(open, close + 1)); // includes the brackets
      rest = rest.slice(close + 1);
    }
  }
  return out;
}

// ── Schema filtering ─────────────────────────────────────────────

function filterBySchemaTrie(
  schema: JSONSchema,
  trie: PathTrie,
  options: FilterOptions,
): JSONSchema {
  if (!schema || typeof schema !== "object") return schema;
  // Terminal node: surface the subtree as-is. Descendants stay
  // intact, including any noise the underlying type happens to carry
  // (rare — most allowlist leaves point at narrowly-typed K8s fields).
  if (trie.terminal) return schema;
  // Nothing to narrow further; leaf without children means "no
  // matching path under this node, drop everything."
  if (trie.children.size === 0) return emptyObject(schema);

  // Expand `{$ref}` and `{allOf:[{$ref}, ...]}` envelopes so we can
  // peek at the underlying `properties` / `items`. Without a
  // resolveRef this no-ops and the schema below stays as a ref the
  // walker will deref later — but then we can't narrow inside it,
  // so the parent surfaces whole.
  const expanded = expandSchema(schema, options);

  // Array-item descent: if the schema is `type: array` and the trie
  // has a `[*]` child, recurse into `items` and discard non-`[*]`
  // children at this level.
  const wildcardTrie = trie.children.get("[*]");
  if (wildcardTrie && expanded.items && typeof expanded.items === "object") {
    const filteredItems = filterBySchemaTrie(expanded.items, wildcardTrie, options);
    return inlinedCopy(expanded, { items: filteredItems });
  }

  // Property-level descent: keep only the properties the trie names.
  if (expanded.properties) {
    const next: Record<string, JSONSchema> = {};
    for (const [seg, childTrie] of trie.children) {
      if (seg === "[*]") continue; // not at array level
      const childSchema = expanded.properties[seg];
      if (!childSchema) continue;
      next[seg] = filterBySchemaTrie(childSchema, childTrie, options);
    }
    const surfaced = new Set(Object.keys(next));
    const required = Array.isArray(expanded.required)
      ? expanded.required.filter((r) => surfaced.has(r))
      : undefined;
    return inlinedCopy(expanded, { properties: next, required });
  }

  // Trie said "narrow further" but the schema isn't object-shaped.
  // Best effort — surface as-is so the walker can decide what to do.
  return schema;
}

/** Resolve `$ref` and merge `allOf` so the caller sees a flat
 *  schema with `properties` / `items`. Returns the input untouched
 *  when expansion isn't possible (no resolver, unresolvable ref,
 *  type-conflicting allOf). */
function expandSchema(schema: JSONSchema, options: FilterOptions): JSONSchema {
  let s = schema;
  if (typeof s.$ref === "string" && options.resolveRef) {
    const resolved = options.resolveRef(s.$ref);
    if (resolved) s = resolved;
  }
  if (Array.isArray(s.allOf) && s.allOf.length > 0 && options.resolveRef) {
    s = mergeAllOf(s, { resolveRef: options.resolveRef, seen: new Set() });
  }
  return s;
}

/** Build a copy of `schema` with the supplied overrides applied,
 *  stripping `$ref` and `allOf` since we've inlined them. The result
 *  is what the walker reads — it must not re-resolve and re-expose
 *  the fields we just pruned. */
function inlinedCopy(
  schema: JSONSchema,
  overrides: { properties?: Record<string, JSONSchema>; items?: JSONSchema; required?: string[] },
): JSONSchema {
  const next: JSONSchema = { ...schema };
  delete next.$ref;
  delete next.allOf;
  if (overrides.properties) next.properties = overrides.properties;
  if (overrides.items) next.items = overrides.items;
  if (overrides.required !== undefined) {
    if (overrides.required.length > 0) next.required = overrides.required;
    else delete next.required;
  }
  return next;
}

/** Replace the schema with an empty-properties object. Used when the
 *  trie has children but none match — better to render an empty
 *  object than to surface noise the operator didn't ask for. */
function emptyObject(schema: JSONSchema): JSONSchema {
  return { ...schema, $ref: undefined, allOf: undefined, properties: {}, required: [] };
}
