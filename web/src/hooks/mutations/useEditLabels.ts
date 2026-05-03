// useEditLabels — optimistic label editor. Builds a minimal SSA payload
// touching only metadata.labels so periscope-spa registers as the
// manager of the labels we wrote, leaving every other field's manager
// intact. Detail cache is patched optimistically so MetaPills updates
// pre-roundtrip; list cache is invalidated rather than optimistically
// patched (some pages derive filter chips from labels and rebuilding
// those derivations correctly in the optimistic phase is fragile).

import { ApiError, api, type YamlKind } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { queryKeys } from "../../lib/queryKeys";
import { buildMinimalSSA, type Identity } from "../../lib/yamlPatch";
import { useOptimisticMutation } from "./_useOptimistic";

interface EditLabelsArgs {
  cluster: string;
  kind: YamlKind;
  /** Empty string for cluster-scoped resources. */
  namespace: string;
  name: string;
}

interface EditLabelsVars {
  labels: Record<string, string>;
}

interface DetailLike {
  labels?: Record<string, string>;
}

interface Snap {
  detail: DetailLike | undefined;
}

export function useEditLabels(args: EditLabelsArgs) {
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

  return useOptimisticMutation<EditLabelsVars, Snap, unknown, ApiError | Error>({
    detailKey,
    metaKey,
    listKey,
    applyOptimistic: (qc, vars) => {
      const detail = qc.getQueryData<DetailLike>(detailKey);
      qc.setQueryData<DetailLike>(detailKey, (prev) =>
        prev ? { ...prev, labels: vars.labels } : prev,
      );
      return { detail };
    },
    rollback: (qc, snap) => {
      qc.setQueryData(detailKey, snap.detail);
    },
    mutationFn: (vars) => {
      const identity: Identity = {
        apiVersion: meta.group ? `${meta.group}/${meta.version}` : meta.version,
        kind: meta.kind,
        name: args.name,
        namespace: args.namespace || undefined,
      };
      const yaml = buildMinimalSSA(
        [{ op: "replace", path: ["metadata", "labels"], value: vars.labels }],
        identity,
      );
      return api.applyResource({
        cluster: args.cluster,
        group: meta.group,
        version: meta.version,
        resource: meta.resource,
        namespace: args.namespace || undefined,
        name: args.name,
        yaml,
        force: false,
      });
    },
    successToast: () => `updated labels on ${args.name}`,
    errorToast: (err) =>
      `failed to update labels on ${args.name}: ${err?.message ?? "unknown"}`,
  });
}
