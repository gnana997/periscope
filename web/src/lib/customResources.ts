// customResources — types + helpers that let the same editor surface
// (YamlEditor, ResourceActions, DriftDiffOverlay, drift detection)
// drive both built-in K8s kinds AND user-installed Custom Resources.
//
// The discriminated `EditorSource` keeps cache keys segregated so a
// built-in `certificates` plural can't collide with a hypothetical
// CR with the same plural — and keeps TypeScript honest about which
// branch a caller is on.

import type { QueryClient } from "@tanstack/react-query";
import type { CRD } from "./types";
import type { ResourceRef, YamlKind, ClusterScopedKind } from "./api";
import { KIND_REGISTRY } from "./k8sKinds";
import { queryKeys } from "./queryKeys";

const CLUSTER_SCOPED_BUILTINS: ReadonlySet<YamlKind> = new Set<YamlKind>([
  "namespaces",
  "pvs",
  "storageclasses",
  "clusterroles",
  "clusterrolebindings",
  "ingressclasses",
  "priorityclasses",
  "runtimeclasses",
]);

export const CLUSTER_SCOPED_YAMLKINDS: readonly ClusterScopedKind[] = [
  "namespaces",
  "pvs",
  "storageclasses",
  "clusterroles",
  "clusterrolebindings",
  "ingressclasses",
  "priorityclasses",
  "runtimeclasses",
];

/** A custom resource the editor can read/edit. Mirrors the subset of
 *  CRD metadata the editor needs — separate type so callers don't pass
 *  a full CRD object around. */
export interface CustomResourceRef {
  group: string; // "cert-manager.io"
  version: string; // "v1" — storage version unless caller picked a specific served one
  resource: string; // plural URL segment, e.g. "issuers"
  kind: string; // "Issuer" — for schema label + UI title
  scope: "Namespaced" | "Cluster";
  shortNames?: string[];
}

/** Identifies the editor's data source. Built-in path stays
 *  yamlKind-keyed (no churn for the 30 existing pages); custom path
 *  carries a fully-resolved CR ref so we never have to look anything
 *  up at runtime. */
export type EditorSource =
  | { kind: "builtin"; yamlKind: YamlKind }
  | { kind: "custom"; cr: CustomResourceRef };

/** Build an EditorSource from a CRD definition. Defaults to the
 *  storage version (what the apiserver hands back when you GET); fall
 *  back to served version if storage isn't set (legacy CRDs). */
export function refFromCRD(crd: CRD, version?: string): CustomResourceRef {
  const v = version || crd.storageVersion || crd.servedVersion || crd.versions[0]?.name;
  return {
    group: crd.group,
    version: v ?? "v1",
    resource: crd.plural,
    kind: crd.kind,
    scope: crd.scope,
    shortNames: crd.shortNames,
  };
}

export function customSource(cr: CustomResourceRef): EditorSource {
  return { kind: "custom", cr };
}

export function builtinSource(yamlKind: YamlKind): EditorSource {
  return { kind: "builtin", yamlKind };
}

/** Stable string identity for a source — used for cache keys and the
 *  pub/sub channel for editor-dirty bits. CRs get a fully namespaced
 *  key so cross-CRD plural collisions are impossible. */
export function sourceCacheKey(s: EditorSource): string {
  if (s.kind === "builtin") return `builtin:${s.yamlKind}`;
  return `cr:${s.cr.group}/${s.cr.version}/${s.cr.resource}`;
}

/** Channel key for the editor-dirty pub/sub. Built-ins keep the bare
 *  yamlKind shape so the existing `useEditorDirty(cluster, "pods", …)`
 *  call sites in the 29 built-in pages don't have to change. CRs use
 *  the namespaced source key (no built-in collisions). */
export function dirtyChannelKey(s: EditorSource): string {
  return s.kind === "builtin" ? s.yamlKind : sourceCacheKey(s);
}

export function sourceLabel(s: EditorSource): string {
  return s.kind === "builtin" ? s.yamlKind : s.cr.kind;
}

export function isClusterScoped(s: EditorSource): boolean {
  return s.kind === "builtin"
    ? CLUSTER_SCOPED_BUILTINS.has(s.yamlKind)
    : s.cr.scope === "Cluster";
}

/** Resolve GVRK for any source — built-ins look up KIND_REGISTRY,
 *  custom returns its own carried fields. ResourceActions and any
 *  page that needs to construct a ResourceRef from a source go
 *  through here instead of branching at every call site. */
export function gvrkFromSource(s: EditorSource): {
  group: string;
  version: string;
  resource: string;
  kind: string;
} {
  if (s.kind === "builtin") {
    const meta = KIND_REGISTRY[s.yamlKind];
    return { group: meta.group, version: meta.version, resource: meta.resource, kind: meta.kind };
  }
  const { group, version, resource, kind } = s.cr;
  return { group, version, resource, kind };
}

/** Build a ResourceRef from a source + identity. Mirrors the inline
 *  construction in ResourceActions / YamlView so neither has to know
 *  whether the source is built-in or custom. */
export function sourceToResourceRef(
  source: EditorSource,
  cluster: string,
  namespace: string | null | undefined,
  name: string,
): ResourceRef {
  const { group, version, resource, kind } = gvrkFromSource(source);
  return {
    cluster,
    group,
    version,
    resource,
    namespace: namespace || undefined,
    name,
    kind,
  };
}

/** Query key for the editor's YAML fetch. Built-ins land under
 *  queryKeys.cluster(c).kind(yamlKind).yaml(...); CRs under
 *  queryKeys.cluster(c).cr(group, version, plural).yaml(...). Both
 *  share the `.all` prefix for prefix invalidation, but the key
 *  shapes are otherwise segregated so plural collisions across
 *  built-ins and CRDs are impossible. */
export function editorYamlQueryKey(
  s: EditorSource,
  cluster: string,
  ns: string,
  name: string,
): readonly unknown[] {
  if (s.kind === "builtin") {
    return queryKeys.cluster(cluster).kind(s.yamlKind).yaml(ns, name);
  }
  const { group, version, resource } = s.cr;
  return queryKeys.cluster(cluster).cr(group, version, resource).yaml(ns, name);
}

/** Side-channel YAML fetch key for the drift-diff overlay (distinct
 *  from the editor's pristine-flowing yaml query, but still under the
 *  same kind/cr subtree so prefix invalidation sweeps it). */
export function driftYamlQueryKey(
  s: EditorSource,
  cluster: string,
  ns: string,
  name: string,
): readonly unknown[] {
  if (s.kind === "builtin") {
    return queryKeys.cluster(cluster).kind(s.yamlKind).yamlDrift(ns, name);
  }
  const { group, version, resource } = s.cr;
  return queryKeys
    .cluster(cluster)
    .cr(group, version, resource)
    .yamlDrift(ns, name);
}

/** Invalidate only the editor's YAML cache. Used by drift
 *  silent-refresh and the [reload from cluster] button. */
export function invalidateEditorYaml(
  qc: QueryClient,
  s: EditorSource,
  cluster: string,
  ns: string,
  name: string,
): void {
  qc.invalidateQueries({ queryKey: editorYamlQueryKey(s, cluster, ns, name) });
}

/** Invalidate everything that should refresh after a successful
 *  apply: list cache, detail cache, YAML cache, events cache, meta
 *  cache. One prefix invalidation per source kind sweeps all of them
 *  — `.kind(plural).all` for built-ins, `.cr(g, v, p).all` for CRs.
 *  Plus `.kind(resource.resource).meta(...)` since useResourceMeta
 *  shares the kind subtree (see useResource.ts). */
export async function invalidateAfterApply(
  qc: QueryClient,
  s: EditorSource,
  ref: ResourceRef,
): Promise<void> {
  // Awaited so the post-apply refetch lands before the editor
  // unmounts — YamlReadView then opens to fresh data without the
  // race-mitigation setTimeout that the previous design relied on.
  if (s.kind === "builtin") {
    await qc.invalidateQueries({
      queryKey: queryKeys.cluster(ref.cluster).kind(s.yamlKind).all,
    });
  } else {
    const { group, version, resource } = s.cr;
    await qc.invalidateQueries({
      queryKey: queryKeys.cluster(ref.cluster).cr(group, version, resource).all,
    });
  }
  // useResourceMeta keys via .kind(resource.resource) for both built-
  // ins and CRs (see comment in useResource.ts), so this sweeps meta
  // for either source type. Skipped when source is built-in and
  // s.yamlKind === ref.resource (already covered by the first call).
  if (s.kind === "custom" || s.yamlKind !== ref.resource) {
    await qc.invalidateQueries({
      queryKey: queryKeys.cluster(ref.cluster).kind(ref.resource).all,
    });
  }
}
