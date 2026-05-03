// useRolloutRestart — kicks a rolling restart on a Deployment,
// StatefulSet, or DaemonSet by patching the well-known
// `kubectl.kubernetes.io/restartedAt` annotation on its pod template.
// The controller notices the template hash change and rolls the
// workload — same semantics as `kubectl rollout restart`.
//
// No optimistic update — there's no observable state we'd flip
// pre-roundtrip. The post-success invalidation refetches the kind
// subtree so the user sees fresh `availableReplicas` / `updatedReplicas`
// counts as the rollout progresses; the existing list-poll picks up
// the cascade pod churn within ~15s (issue #4).

import { ApiError, api, type YamlKind } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { queryKeys } from "../../lib/queryKeys";
import { buildMinimalSSA, type Identity } from "../../lib/yamlPatch";
import { useOptimisticMutation } from "./_useOptimistic";

export type RestartableKind = "deployments" | "statefulsets" | "daemonsets";

export const RESTARTABLE_KINDS: RestartableKind[] = [
  "deployments",
  "statefulsets",
  "daemonsets",
];

export function isRestartable(kind: string): kind is RestartableKind {
  return (RESTARTABLE_KINDS as string[]).includes(kind);
}

interface RestartArgs {
  cluster: string;
  kind: YamlKind;
  namespace: string;
  name: string;
}

type RestartVars = void;

interface Snap {
  detail: unknown;
}

export function useRolloutRestart(args: RestartArgs) {
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

  return useOptimisticMutation<RestartVars, Snap, unknown, ApiError | Error>({
    detailKey,
    metaKey,
    listKey: kindPrefix,
    applyOptimistic: (qc) => {
      // No visible optimistic state for restart — we just snapshot
      // the detail in case rollback is needed.
      return { detail: qc.getQueryData(detailKey) };
    },
    rollback: (qc, snap) => {
      qc.setQueryData(detailKey, snap.detail);
    },
    mutationFn: () => {
      const identity: Identity = {
        apiVersion: meta.group ? `${meta.group}/${meta.version}` : meta.version,
        kind: meta.kind,
        name: args.name,
        namespace: args.namespace,
      };
      const yaml = buildMinimalSSA(
        [
          {
            op: "replace",
            path: [
              "spec",
              "template",
              "metadata",
              "annotations",
              "kubectl.kubernetes.io/restartedAt",
            ],
            // ISO-8601 timestamp matches kubectl's format and is what
            // controllers/operators expect to see in the field.
            value: new Date().toISOString(),
          },
        ],
        identity,
      );
      return api.applyResource({
        cluster: args.cluster,
        group: meta.group,
        version: meta.version,
        resource: meta.resource,
        namespace: args.namespace,
        name: args.name,
        yaml,
        // Force on conflict — kubectl-rollout may currently own the
        // annotation. Operator opted into this; take ownership.
        force: true,
      });
    },
    successToast: () => `restarted ${args.kind.replace(/s$/, "")} ${args.name}`,
    errorToast: (err) =>
      `failed to restart ${args.name}: ${err?.message ?? "unknown"}`,
  });
}
