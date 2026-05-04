// useScaleResource — optimistic scale for deployments / statefulsets /
// replicasets. Builds a minimal SSA payload touching only spec.replicas
// so periscope-spa claims ownership of just that field, leaving every
// other field's manager intact. The cache is updated optimistically so
// the StatStrip and the table flip to the new desired count within
// one render; readyReplicas stays at the prior value until the refetch
// lands — which is honest UX because the new pods aren't ready yet.
//
// As with useDeleteResource, we patch every loaded list cache for
// the kind via setQueriesData so the row updates wherever it's
// currently rendered (all-namespaces view, specific-namespace view,
// or both at once).

import { ApiError, type YamlKind } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { queryKeys } from "../../lib/queryKeys";
import { buildMinimalSSA, type Identity } from "../../lib/yamlPatch";
import { patchRowInList } from "../../lib/listShape";
import type { ResourceListResponse } from "../../lib/types";
import type { QueryKey } from "@tanstack/react-query";
import { useOptimisticMutation } from "./_useOptimistic";
import { applyWithLenientConflict } from "./_applyWithLenientConflict";

export type ScalableKind = "deployments" | "statefulsets" | "replicasets";

export const SCALABLE_KINDS: ScalableKind[] = [
  "deployments",
  "statefulsets",
  "replicasets",
];

export function isScalable(kind: string): kind is ScalableKind {
  return (SCALABLE_KINDS as string[]).includes(kind);
}

interface ScaleArgs {
  cluster: string;
  // Accepts any YamlKind for hook ergonomics (callers may construct
  // before narrowing). Non-scalable kinds will get a 422 from the
  // apiserver if the mutation is ever fired — the UI gates this via
  // isScalable() so it should be unreachable in practice.
  kind: YamlKind;
  namespace: string;
  name: string;
}

interface ScaleVars {
  replicas: number;
}

interface DetailLike {
  replicas?: number;
}

interface Snap {
  detail: DetailLike | undefined;
  lists: Array<[QueryKey, ResourceListResponse | undefined]>;
}

export function useScaleResource(args: ScaleArgs) {
  const meta = KIND_REGISTRY[args.kind];
  const detailKey = queryKeys
    .cluster(args.cluster)
    .kind(args.kind)
    .detail(args.namespace, args.name);
  const metaKey = queryKeys
    .cluster(args.cluster)
    .kind(args.kind)
    .meta(args.namespace, args.name);
  const kindPrefix = queryKeys.cluster(args.cluster).kind(args.kind).all;

  return useOptimisticMutation<ScaleVars, Snap, unknown, ApiError | Error>({
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
        prev ? { ...prev, replicas: vars.replicas } : prev,
      );
      qc.setQueriesData<ResourceListResponse | undefined>(
        {
          queryKey: kindPrefix,
          predicate: (q) =>
            Array.isArray(q.queryKey) && q.queryKey[4] === "list",
        },
        (prev) =>
          patchRowInList<{ name: string; namespace?: string; replicas?: number }>(
            prev as never,
            args.kind,
            { name: args.name, namespace: args.namespace },
            (row) => ({ ...row, replicas: vars.replicas }),
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
        apiVersion: meta.group ? `${meta.group}/${meta.version}` : meta.version,
        kind: meta.kind,
        name: args.name,
        namespace: args.namespace,
      };
      const yaml = buildMinimalSSA(
        [{ op: "replace", path: ["spec", "replicas"], value: vars.replicas }],
        identity,
      );
      // Lenient SSA: auto-takeover when the conflict is only with
      // HUMAN/UNKNOWN managers (kubectl-* / Rancher / unclassified).
      // GITOPS/HELM/CONTROLLER conflicts surface a classified error
      // instead — see _applyWithLenientConflict.ts.
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
        "scale",
      );
    },
    successToast: (vars) => `scaled ${args.name} to ${vars.replicas}`,
    errorToast: (err, vars) =>
      `failed to scale ${args.name} to ${vars.replicas}: ${err?.message ?? "unknown"}`,
  });
}
