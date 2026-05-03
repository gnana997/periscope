// listShape — maps a YAML/URL plural kind to the field name on its
// corresponding *List response type.
//
// The /api/clusters/{cluster}/{kind} endpoints return kind-specific
// shapes (DeploymentList = { deployments: Deployment[] },
// ReplicaSetList = { replicaSets: ReplicaSet[] }, …) — the array isn't
// always under the URL kind verbatim. Optimistic mutations that need
// to splice the list (delete, scale-affecting-list-row) need this map
// so a generic helper can locate the array regardless of kind.
//
// Keep in sync with src/lib/types.ts list-interface field names.

import type { ResourceListResponse } from "./types";

export const LIST_ITEMS_KEY: Record<string, string> = {
  // core/v1
  pods: "pods",
  services: "services",
  configmaps: "configMaps",
  secrets: "secrets",
  namespaces: "namespaces",
  pvs: "pvs",
  pvcs: "pvcs",
  serviceaccounts: "serviceAccounts",
  resourcequotas: "resourceQuotas",
  limitranges: "limitRanges",
  nodes: "nodes",

  // apps/v1
  deployments: "deployments",
  statefulsets: "statefulSets",
  daemonsets: "daemonSets",
  replicasets: "replicaSets",

  // batch/v1
  jobs: "jobs",
  cronjobs: "cronJobs",

  // networking.k8s.io/v1
  ingresses: "ingresses",
  ingressclasses: "ingressClasses",
  networkpolicies: "networkPolicies",

  // rbac.authorization.k8s.io/v1
  roles: "roles",
  rolebindings: "roleBindings",
  clusterroles: "clusterRoles",
  clusterrolebindings: "clusterRoleBindings",

  // storage.k8s.io/v1
  storageclasses: "storageClasses",

  // autoscaling/v2
  horizontalpodautoscalers: "hpas",

  // policy/v1
  poddisruptionbudgets: "pdbs",

  // scheduling.k8s.io/v1
  priorityclasses: "priorityClasses",

  // node.k8s.io/v1
  runtimeclasses: "runtimeClasses",
};

interface ListLike {
  [field: string]: unknown;
}

interface NamedRow {
  name: string;
  namespace?: string;
}

// Returns a shallow-cloned list with the row matching (name, namespace)
// filtered out. Returns the original reference unchanged if the kind
// has no known field or the row doesn't match.
export function removeRowFromList(
  list: ResourceListResponse | undefined,
  kind: string,
  match: NamedRow,
): ResourceListResponse | undefined {
  if (!list) return list;
  const field = LIST_ITEMS_KEY[kind];
  if (!field) return list;
  const obj = list as unknown as ListLike;
  const items = obj[field];
  if (!Array.isArray(items)) return list;
  const next = items.filter((row) => {
    const r = row as NamedRow;
    if (r.name !== match.name) return true;
    // Match namespace too when present (cluster-scoped resources have
    // no namespace; treat undefined === undefined as a match).
    return (r.namespace ?? undefined) !== (match.namespace ?? undefined);
  });
  if (next.length === items.length) return list;
  return { ...obj, [field]: next } as unknown as ResourceListResponse;
}

// Returns a shallow-cloned list with the row matching (name, namespace)
// patched via `patch(row)`. Returns the original reference unchanged if
// the kind has no known field or the row doesn't match.
export function patchRowInList<T extends NamedRow>(
  list: ResourceListResponse | undefined,
  kind: string,
  match: NamedRow,
  patch: (row: T) => T,
): ResourceListResponse | undefined {
  if (!list) return list;
  const field = LIST_ITEMS_KEY[kind];
  if (!field) return list;
  const obj = list as unknown as ListLike;
  const items = obj[field];
  if (!Array.isArray(items)) return list;
  let touched = false;
  const next = items.map((row) => {
    const r = row as T;
    if (r.name !== match.name) return row;
    if ((r.namespace ?? undefined) !== (match.namespace ?? undefined)) return row;
    touched = true;
    return patch(r);
  });
  if (!touched) return list;
  return { ...obj, [field]: next } as unknown as ResourceListResponse;
}
