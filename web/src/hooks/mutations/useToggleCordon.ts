// useToggleCordon — flips a Node's `spec.unschedulable`. When true,
// the scheduler skips the node for new pod placements; existing pods
// stay running. The companion drain workflow (evict all pods on the
// node) is intentionally a separate, multi-step action — see #4 for
// follow-up.
//
// Optimistic patching flips the `unschedulable` flag in both detail
// and list caches so the cordon badge appears within one render.

import { ApiError, api } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { queryKeys } from "../../lib/queryKeys";
import { buildMinimalSSA, type Identity } from "../../lib/yamlPatch";
import { patchRowInList } from "../../lib/listShape";
import type { ResourceListResponse } from "../../lib/types";
import type { QueryKey } from "@tanstack/react-query";
import { useOptimisticMutation } from "./_useOptimistic";

interface ToggleCordonArgs {
  cluster: string;
  name: string;
}

interface ToggleCordonVars {
  unschedulable: boolean;
}

interface DetailLike {
  unschedulable?: boolean;
}

interface Snap {
  detail: DetailLike | undefined;
  lists: Array<[QueryKey, ResourceListResponse | undefined]>;
}

export function useToggleCordon(args: ToggleCordonArgs) {
  const meta = KIND_REGISTRY.nodes;
  // Nodes are cluster-scoped — the kind() factory's detail/list
  // functions take ns="" by convention.
  const detailKey = queryKeys
    .cluster(args.cluster)
    .kind("nodes")
    .detail("", args.name);
  const metaKey = queryKeys
    .cluster(args.cluster)
    .kind("nodes")
    .meta("", args.name);
  const kindPrefix = queryKeys.cluster(args.cluster).kind("nodes").all;

  return useOptimisticMutation<ToggleCordonVars, Snap, unknown, ApiError | Error>({
    detailKey,
    metaKey,
    listKey: kindPrefix,
    applyOptimistic: (qc, vars) => {
      const detail = qc.getQueryData<DetailLike>(detailKey);
      const lists = qc.getQueriesData<ResourceListResponse>({
        queryKey: kindPrefix,
        predicate: (q) =>
          Array.isArray(q.queryKey) && q.queryKey[4] === "list",
      });
      qc.setQueryData<DetailLike>(detailKey, (prev) =>
        prev ? { ...prev, unschedulable: vars.unschedulable } : prev,
      );
      qc.setQueriesData<ResourceListResponse | undefined>(
        {
          queryKey: kindPrefix,
          predicate: (q) =>
            Array.isArray(q.queryKey) && q.queryKey[4] === "list",
        },
        (prev) =>
          patchRowInList<{ name: string; unschedulable?: boolean }>(
            prev,
            "nodes",
            { name: args.name },
            (row) => ({ ...row, unschedulable: vars.unschedulable }),
          ),
      );
      return { detail, lists };
    },
    rollback: (qc, snap) => {
      qc.setQueryData(detailKey, snap.detail);
      for (const [key, data] of snap.lists) qc.setQueryData(key, data);
    },
    mutationFn: (vars) => {
      const identity: Identity = {
        apiVersion: meta.version,
        kind: meta.kind,
        name: args.name,
      };
      const yaml = buildMinimalSSA(
        [
          {
            op: "replace",
            path: ["spec", "unschedulable"],
            value: vars.unschedulable,
          },
        ],
        identity,
      );
      return api.applyResource({
        cluster: args.cluster,
        group: meta.group,
        version: meta.version,
        resource: meta.resource,
        name: args.name,
        yaml,
        force: false,
      });
    },
    successToast: (vars) =>
      `${vars.unschedulable ? "cordoned" : "uncordoned"} node ${args.name}`,
    errorToast: (err, vars) =>
      `failed to ${vars.unschedulable ? "cordon" : "uncordon"} ${args.name}: ${err?.message ?? "unknown"}`,
  });
}
