// useToggleSuspend — flips a CronJob's `spec.suspend`. Suspended
// CronJobs skip their schedule until the field flips back. Exposes
// optimistic patching so the UI flips state before the SSA round-trip
// completes; both the detail cache and any loaded list caches are
// updated.

import { ApiError } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { queryKeys } from "../../lib/queryKeys";
import { buildMinimalSSA, type Identity } from "../../lib/yamlPatch";
import { patchRowInList } from "../../lib/listShape";
import type { ResourceListResponse } from "../../lib/types";
import type { QueryKey } from "@tanstack/react-query";
import { useOptimisticMutation } from "./_useOptimistic";
import { applyWithLenientConflict } from "./_applyWithLenientConflict";

interface ToggleSuspendArgs {
  cluster: string;
  namespace: string;
  name: string;
}

interface ToggleSuspendVars {
  suspend: boolean;
}

interface DetailLike {
  suspend?: boolean;
}

interface Snap {
  detail: DetailLike | undefined;
  lists: Array<[QueryKey, ResourceListResponse | undefined]>;
}

export function useToggleSuspend(args: ToggleSuspendArgs) {
  const meta = KIND_REGISTRY.cronjobs;
  const detailKey = queryKeys
    .cluster(args.cluster)
    .kind("cronjobs")
    .detail(args.namespace, args.name);
  const metaKey = queryKeys
    .cluster(args.cluster)
    .kind("cronjobs")
    .meta(args.namespace, args.name);
  const kindPrefix = queryKeys.cluster(args.cluster).kind("cronjobs").all;

  return useOptimisticMutation<ToggleSuspendVars, Snap, unknown, ApiError | Error>({
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
        prev ? { ...prev, suspend: vars.suspend } : prev,
      );
      qc.setQueriesData<ResourceListResponse | undefined>(
        {
          queryKey: kindPrefix,
          predicate: (q) =>
            Array.isArray(q.queryKey) && q.queryKey[4] === "list",
        },
        (prev) =>
          patchRowInList<{ name: string; namespace?: string; suspend?: boolean }>(
            prev,
            "cronjobs",
            { name: args.name, namespace: args.namespace },
            (row) => ({ ...row, suspend: vars.suspend }),
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
        apiVersion: `${meta.group}/${meta.version}`,
        kind: meta.kind,
        name: args.name,
        namespace: args.namespace,
      };
      const yaml = buildMinimalSSA(
        [{ op: "replace", path: ["spec", "suspend"], value: vars.suspend }],
        identity,
      );
      return applyWithLenientConflict(
        {
          cluster: args.cluster,
          group: meta.group,
          version: meta.version,
          resource: meta.resource,
          namespace: args.namespace,
          name: args.name,
          yaml,
        },
        "suspend toggle",
      );
    },
    successToast: (vars) =>
      `${vars.suspend ? "suspended" : "resumed"} cronjob ${args.name}`,
    errorToast: (err, vars) =>
      `failed to ${vars.suspend ? "suspend" : "resume"} ${args.name}: ${err?.message ?? "unknown"}`,
  });
}
