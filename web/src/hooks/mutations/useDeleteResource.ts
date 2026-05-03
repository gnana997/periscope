// useDeleteResource — optimistic delete. The row vanishes from every
// loaded list cache before the DELETE flies; on error we restore the
// snapshot. There is no undo affordance and there will not be one:
// Kubernetes has no transaction log to re-create from, delaying the
// API call risks orphaned actions if the tab closes during the undo
// window, and controllers may notice the resource going stale and
// react in ways the user can't unwind. Lens, Headlamp, and Rancher
// all match this confirm-then-go shape (per Lane 2 UX research).
//
// IMPORTANT: list caches are namespace-keyed
// (queryKeys.cluster(c).kind(k).list(ns)). The user may be viewing
// all namespaces (ns="") AND have a per-namespace cache loaded
// elsewhere from a prior selection. Patching only the deleted row's
// namespace would miss the all-namespaces view that's actually on
// screen. We use setQueriesData with the kind prefix to patch every
// loaded list at once.

import { ApiError, api, type YamlKind } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { queryKeys } from "../../lib/queryKeys";
import { patchRowInList, removeRowFromList } from "../../lib/listShape";
import type { ResourceListResponse } from "../../lib/types";
import type { QueryKey } from "@tanstack/react-query";
import { useOptimisticMutation } from "./_useOptimistic";

interface DeleteArgs {
  cluster: string;
  kind: YamlKind;
  /** Empty string for cluster-scoped resources. */
  namespace: string;
  name: string;
}

type DeleteVars = void;

interface Snap {
  detail: unknown;
  lists: Array<[QueryKey, ResourceListResponse | undefined]>;
}

export function useDeleteResource(args: DeleteArgs) {
  const meta = KIND_REGISTRY[args.kind];
  const detailKey = queryKeys
    .cluster(args.cluster)
    .kind(args.kind)
    .detail(args.namespace, args.name);
  const metaKey = queryKeys
    .cluster(args.cluster)
    .kind(args.kind)
    .meta(args.namespace, args.name);
  // Prefix that covers every list / detail / yaml / events / meta /
  // metrics under this kind. cancelQueries + invalidateQueries
  // operate on the prefix so we don't have to enumerate every
  // namespace-pinned list cache.
  const kindPrefix = queryKeys.cluster(args.cluster).kind(args.kind).all;

  return useOptimisticMutation<DeleteVars, Snap, unknown, ApiError | Error>({
    detailKey,
    metaKey,
    listKey: kindPrefix,
    applyOptimistic: (qc) => {
      const detail = qc.getQueryData(detailKey);
      // Snapshot every loaded list cache for this kind, then patch
      // each one. setQueriesData reaches both list("") (the
      // all-namespaces view) and list("<ns>") (a specific namespace
      // view) so the row disappears wherever it's currently
      // rendered.
      const lists = qc.getQueriesData<ResourceListResponse>({
        queryKey: kindPrefix,
        predicate: (q) =>
          Array.isArray(q.queryKey) && q.queryKey[4] === "list",
      });
      // Pods linger in Terminating phase for ~30s after delete;
      // patching their phase keeps the row visible and matches what
      // the refetch will return. Every other kind disappears promptly,
      // so removing the row optimistically gives the cleaner UX.
      const isPod = args.kind === "pods";
      qc.setQueriesData<ResourceListResponse | undefined>(
        {
          queryKey: kindPrefix,
          predicate: (q) =>
            Array.isArray(q.queryKey) && q.queryKey[4] === "list",
        },
        (prev) =>
          isPod
            ? patchRowInList<{ name: string; namespace?: string; phase?: string }>(
                prev,
                args.kind,
                { name: args.name, namespace: args.namespace || undefined },
                (row) => ({ ...row, phase: "Terminating" }),
              )
            : removeRowFromList(prev, args.kind, {
                name: args.name,
                namespace: args.namespace || undefined,
              }),
      );
      return { detail, lists };
    },
    rollback: (qc, snap) => {
      qc.setQueryData(detailKey, snap.detail);
      for (const [key, data] of snap.lists) qc.setQueryData(key, data);
    },
    mutationFn: () =>
      api.deleteResource({
        cluster: args.cluster,
        group: meta.group,
        version: meta.version,
        resource: meta.resource,
        namespace: args.namespace || undefined,
        name: args.name,
      }),
    successToast: () => `deleted ${args.kind}/${args.name}`,
    errorToast: (err) =>
      `failed to delete ${args.kind}/${args.name}: ${err?.message ?? "unknown"}`,
  });
}
