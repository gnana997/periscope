// listShape — maps a YAML/URL plural kind to the field name on its
// corresponding *List response type, and provides per-kind row identity
// + max-items metadata used by the streaming cache mutators.
//
// The /api/clusters/{cluster}/{kind} endpoints return kind-specific
// shapes (DeploymentList = { deployments: Deployment[] },
// ReplicaSetList = { replicaSets: ReplicaSet[] }, …) — the array isn't
// always under the URL kind verbatim. Optimistic mutations and watch-
// stream delta application both need this map so a generic helper can
// locate and modify the array regardless of kind.
//
// Identity model: for most kinds, K8s resource (name, namespace) is
// unique within a cluster. Events break this — the row's `name` field
// is the *involved object's* name (e.g. "nginx-7d8") which is shared
// across every distinct event firing for that pod. Without a per-kind
// identity function, addRowToList would silently overwrite each event
// with the next one for the same object.
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
  events: "events",

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

  // discovery.k8s.io/v1
  endpointslices: "endpointSlices",

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
  /** Optional unique identity (K8s metadata.uid). Used by kinds where
   *  (name, namespace) is not unique — currently events. */
  uid?: string;
}

/**
 * ROW_IDENTITY maps a kind to a function returning a stable string
 * identity for a row. Two rows with equal identities are treated as
 * the same logical row by addRowToList / patchRowInList /
 * removeRowFromList.
 *
 * Default (kind not listed): `name|namespace`. Correct for K8s
 * resource lists where name+namespace is the canonical identity.
 *
 * Events override this with the K8s Event resource's metadata.uid
 * when present, falling back to the default for safety. The Event's
 * `name` field is the involved object's name (NOT the Event's own
 * metadata.name), so without UID, every event for the same pod
 * collapses to one row.
 */
const ROW_IDENTITY: Record<string, (row: NamedRow) => string> = {
  events: (row) => row.uid || `${row.name}|${row.namespace ?? ""}`,
};

function identityFor(kind: string, row: NamedRow): string {
  const fn = ROW_IDENTITY[kind];
  if (fn) return fn(row);
  return `${row.name}|${row.namespace ?? ""}`;
}

/**
 * LIST_MAX_ITEMS caps the in-memory cached list per kind. When
 * addRowToList would push over the cap, the OLDEST entries are
 * trimmed from the FRONT of the list. Insertion order is preserved
 * for everything that survives the trim.
 *
 * Currently only events have a cap, matching the backend snapshot's
 * clusterEventCap (500). Without it, a busy cluster's event list
 * grows monotonically until reconnect or unmount.
 *
 * Other kinds (pods, replicasets, jobs) are unbounded — typical
 * cluster sizes don't approach problematic counts, and the snapshot
 * already reflects the current cluster state.
 */
const LIST_MAX_ITEMS: Record<string, number> = {
  events: 500,
};

// Returns a shallow-cloned list with the row matching `match`'s
// identity filtered out. Returns the original reference unchanged if
// the kind has no known field or no row matches.
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
  const targetID = identityFor(kind, match);
  const next = items.filter(
    (row) => identityFor(kind, row as NamedRow) !== targetID,
  );
  if (next.length === items.length) return list;
  return { ...obj, [field]: next } as unknown as ResourceListResponse;
}

// Returns a shallow-cloned list with `row` appended (or replaced if a
// row with the same identity already exists — common when an ADDED
// watch event arrives for a row already inserted by an earlier
// optimistic update or by a snapshot that landed late).
//
// When LIST_MAX_ITEMS has a cap for this kind and adding the row would
// exceed it, the OLDEST entries are dropped from the front. In-place
// replaces never trigger the cap (replacing doesn't grow the list).
//
// Returns the original reference unchanged if the kind has no known
// field — the watch handler can safely treat the result as
// "successfully applied" since there's nothing to add to.
export function addRowToList<T extends NamedRow>(
  list: ResourceListResponse | undefined,
  kind: string,
  row: T,
): ResourceListResponse | undefined {
  if (!list) return list;
  const field = LIST_ITEMS_KEY[kind];
  if (!field) return list;
  const obj = list as unknown as ListLike;
  const items = obj[field];
  if (!Array.isArray(items)) return list;
  const rowID = identityFor(kind, row);
  const idx = items.findIndex(
    (existing) => identityFor(kind, existing as NamedRow) === rowID,
  );
  let next: unknown[];
  if (idx >= 0) {
    // Replace in place. The new row is the source of truth (just
    // arrived from the apiserver via watch); preserves order so the
    // table doesn't reshuffle.
    next = items.slice();
    next[idx] = row;
  } else {
    next = [...items, row];
    // Enforce per-kind cap. Trim from the front (oldest).
    const cap = LIST_MAX_ITEMS[kind];
    if (cap !== undefined && next.length > cap) {
      next = next.slice(next.length - cap);
    }
  }
  return { ...obj, [field]: next } as unknown as ResourceListResponse;
}

// Returns a shallow-cloned list with the row matching `match`'s
// identity patched via `patch(row)`. Returns the original reference
// unchanged if the kind has no known field or the row doesn't match.
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
  const targetID = identityFor(kind, match);
  let touched = false;
  const next = items.map((row) => {
    if (identityFor(kind, row as NamedRow) !== targetID) return row;
    touched = true;
    return patch(row as T);
  });
  if (!touched) return list;
  return { ...obj, [field]: next } as unknown as ResourceListResponse;
}
