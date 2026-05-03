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

/** Query key for the editor's YAML fetch. Built-in retains the
 *  existing `["yaml", cluster, yamlKind, ns, name]` shape so the
 *  read-only YamlView and other consumers share the cache. CRs use
 *  `["yaml-cr", ...]` — fully segregated. */
export function editorYamlQueryKey(
  s: EditorSource,
  cluster: string,
  ns: string,
  name: string,
): readonly unknown[] {
  if (s.kind === "builtin") {
    return ["yaml", cluster, s.yamlKind, ns, name];
  }
  return ["yaml-cr", cluster, s.cr.group, s.cr.version, s.cr.resource, ns, name];
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
 *  cache. Source-aware — CRs use their own query-key shapes. */
export function invalidateAfterApply(
  qc: QueryClient,
  s: EditorSource,
  ref: ResourceRef,
): void {
  const ns = ref.namespace ?? "";
  if (s.kind === "builtin") {
    const k = s.yamlKind;
    qc.invalidateQueries({ queryKey: [k] });
    qc.invalidateQueries({ queryKey: ["yaml", ref.cluster, k, ns, ref.name] });
    qc.invalidateQueries({
      queryKey: [`${singularize(k)}-detail`, ref.cluster, ns, ref.name],
    });
    qc.invalidateQueries({ queryKey: ["events", ref.cluster, k, ns, ref.name] });
  } else {
    const { group, version, resource } = s.cr;
    qc.invalidateQueries({
      queryKey: ["customresources", ref.cluster, group, version, resource, ns],
    });
    qc.invalidateQueries({
      queryKey: ["customresource", ref.cluster, group, version, resource, ns, ref.name],
    });
    qc.invalidateQueries({
      queryKey: ["yaml-cr", ref.cluster, group, version, resource, ns, ref.name],
    });
  }
  // meta cache key shape is GVR-keyed already (built-ins + CRs share).
  qc.invalidateQueries({
    queryKey: [
      "meta",
      ref.cluster,
      ref.group,
      ref.version,
      ref.resource,
      ns,
      ref.name,
    ],
  });
}

function singularize(kind: string): string {
  return kind.endsWith("s") ? kind.slice(0, -1) : kind;
}
