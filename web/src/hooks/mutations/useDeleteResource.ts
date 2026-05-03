// useDeleteResource — optimistic delete. The row vanishes from the
// list cache before the DELETE flies; on error we restore the
// snapshot. There is no undo affordance and there will not be one:
// Kubernetes has no transaction log to re-create from, delaying the
// API call risks orphaned actions if the tab closes during the undo
// window, and controllers may notice the resource going stale and
// react in ways the user can't unwind. Lens, Headlamp, and Rancher
// all match this confirm-then-go shape (per Lane 2 UX research).

import { ApiError, api, type YamlKind } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { queryKeys } from "../../lib/queryKeys";
import { removeRowFromList } from "../../lib/listShape";
import type { ResourceListResponse } from "../../lib/types";
import { useOptimisticMutation } from "./_useOptimistic";

interface DeleteArgs {
  cluster: string;
  kind: YamlKind;
  /** Empty string for cluster-scoped resources. */
  namespace: string;
  name: string;
}

// No mutation variables — the args carry everything.
type DeleteVars = void;

interface Snap {
  list: ResourceListResponse | undefined;
  detail: unknown;
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
  const listKey = queryKeys
    .cluster(args.cluster)
    .kind(args.kind)
    .list(args.namespace);

  return useOptimisticMutation<DeleteVars, Snap, unknown, ApiError | Error>({
    detailKey,
    metaKey,
    listKey,
    applyOptimistic: (qc) => {
      const list = qc.getQueryData<ResourceListResponse>(listKey);
      const detail = qc.getQueryData(detailKey);
      qc.setQueryData<ResourceListResponse | undefined>(listKey, (prev) =>
        removeRowFromList(prev, args.kind, {
          name: args.name,
          namespace: args.namespace || undefined,
        }),
      );
      qc.setQueryData(detailKey, undefined);
      return { list, detail };
    },
    rollback: (qc, snap) => {
      qc.setQueryData(listKey, snap.list);
      qc.setQueryData(detailKey, snap.detail);
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
