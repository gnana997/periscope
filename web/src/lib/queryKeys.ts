// queryKeys — single source of truth for every React Query key in the
// app. Replaces the prior pattern of inline string-literal keys
// scattered across hooks and mutation call sites, which let small
// drift (e.g. plural/singular mismatches) silently break invalidation.
//
// Shape: hierarchical tuples that nest the cluster id, the resource
// kind, and the view (list / detail / yaml / events / meta / metrics).
// Each branch exposes an `all` prefix so a mutation can invalidate the
// whole subtree in one call:
//
//     qc.invalidateQueries({
//       queryKey: queryKeys.cluster(c).kind("deployments").all,
//     });
//
// cascades to the deployments list, every loaded deployment detail,
// yaml, events, meta, and metrics for that cluster.
//
// Two top-level subtrees plus two registry siblings:
//   ['cluster', c, ...]   — every per-cluster query.
//   ['edit', ...]         — editor dirty-bit pub/sub. Sibling of
//                            ['cluster', ...] so resource invalidation
//                            cannot blow away the editor's dirty
//                            indicator.
//   ['clusters']          — the cluster registry (list of clusters);
//                            sibling, not nested under ['cluster', c],
//                            because it is not data inside a cluster.
//
// Adding a new query? Add the factory here, then call it from the
// hook. Static check (run before merge):
//
//     grep -RnE 'queryKey:\s*\[' web/src --include='*.ts' --include='*.tsx'
//
// Only this file should match.

export const queryKeys = {
  clusters: () => ["clusters"] as const,
  fleet: () => ["fleet"] as const,

  cluster: (c: string) => ({
    all: ["cluster", c] as const,

    summary: () => ["cluster", c, "summary"] as const,
    namespaces: () => ["cluster", c, "namespaces"] as const,
    crds: () => ["cluster", c, "crds"] as const,
    openapi: (group: string, version: string) =>
      ["cluster", c, "openapi", group, version] as const,
    search: (q: string) => ["cluster", c, "search", q] as const,

    // Pre-flight RBAC check used by useCanI. Keyed on the sorted check
    // tuple so two components asking the same question share the cache
    // entry (the action toolbar and a single button for the same
    // verb/resource/ns will collapse).
    canI: (checkKey: string) =>
      ["cluster", c, "canI", checkKey] as const,

    // Per-kind cascade. The `kind` string is the YAML/URL plural
    // ("deployments", "ingresses", …) — the same token the rest of
    // the app uses to address the resource type.
    kind: (kind: string) => ({
      all: ["cluster", c, "kind", kind] as const,
      list: (ns: string) => ["cluster", c, "kind", kind, "list", ns] as const,
      detail: (ns: string, name: string) =>
        ["cluster", c, "kind", kind, "detail", ns, name] as const,
      yaml: (ns: string, name: string) =>
        ["cluster", c, "kind", kind, "yaml", ns, name] as const,
      events: (ns: string, name: string) =>
        ["cluster", c, "kind", kind, "events", ns, name] as const,
      meta: (ns: string, name: string) =>
        ["cluster", c, "kind", kind, "meta", ns, name] as const,
      metrics: (ns: string, name: string) =>
        ["cluster", c, "kind", kind, "metrics", ns, name] as const,
      // Side-channel YAML fetch for the drift-diff modal — distinct
      // from `yaml(...)` so it doesn't compete with the editor's
      // pristine-flowing yamlQuery. Still under the kind subtree so a
      // single prefix invalidation sweeps it.
      yamlDrift: (ns: string, name: string) =>
        ["cluster", c, "kind", kind, "yaml-drift", ns, name] as const,
    }),

    // Custom resources are addressed by GVR (no static registry), so
    // they get a parallel subtree keyed on (group, version, plural).
    cr: (group: string, version: string, plural: string) => ({
      all: ["cluster", c, "cr", group, version, plural] as const,
      list: (ns: string) =>
        ["cluster", c, "cr", group, version, plural, "list", ns] as const,
      detail: (ns: string, name: string) =>
        ["cluster", c, "cr", group, version, plural, "detail", ns, name] as const,
      yaml: (ns: string, name: string) =>
        ["cluster", c, "cr", group, version, plural, "yaml", ns, name] as const,
      events: (ns: string, name: string) =>
        ["cluster", c, "cr", group, version, plural, "events", ns, name] as const,
      meta: (ns: string, name: string) =>
        ["cluster", c, "cr", group, version, plural, "meta", ns, name] as const,
      // Side-channel YAML fetch for the drift-diff modal — distinct
      // from `yaml(...)` so it doesn't compete with the editor's
      // pristine-flowing yamlQuery.
      yamlDrift: (ns: string, name: string) =>
        ["cluster", c, "cr", group, version, plural, "yaml-drift", ns, name] as const,
    }),
  }),

  // Editor dirty-bit pub/sub channel (see hooks/useEditorDirty). Lives
  // outside the ['cluster', ...] subtree on purpose: a resource
  // invalidation must NOT clear the dirty indicator the user is
  // currently looking at.
  edit: (cluster: string, kind: string, ns: string, name: string) =>
    ["edit", cluster, kind, ns, name] as const,
} as const;
