// useEditLabels — optimistic label editor. Builds a minimal SSA payload
// touching only metadata.labels so periscope-spa registers as the
// manager of the labels we wrote, leaving every other field's manager
// intact. Detail cache is patched optimistically so MetaPills updates
// pre-roundtrip; list cache is invalidated rather than optimistically
// patched (some pages derive filter chips from labels and rebuilding
// those derivations correctly in the optimistic phase is fragile).

import { ApiError, type YamlKind } from "../../lib/api";
import { KIND_REGISTRY } from "../../lib/k8sKinds";
import { queryKeys } from "../../lib/queryKeys";
import { buildMinimalSSA, type Identity, type Op } from "../../lib/yamlPatch";
import { useOptimisticMutation } from "./_useOptimistic";
import { applyWithLenientConflict } from "./_applyWithLenientConflict";

// TODO(#224 follow-up): the current contract takes a whole new labels
// map and replaces metadata.labels wholesale. Under SSA per-key
// ownership this means periscope-spa claims every key in vars.labels,
// including labels the user didn't actually touch (e.g. Helm's
// app.kubernetes.io/* labels that the modal pre-populated from the
// resource). Pre-existing behavior — retained-ownership had the same
// creep — but worth fixing as a separate change: take {before, after}
// and emit per-key add/replace/remove ops so periscope-spa only claims
// the keys the operator actually changed.

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
  // Kind prefix so the post-success invalidation sweeps every loaded
  // list cache for this kind (all-namespaces view + any specific-
  // namespace view). Pinning to a single ns would miss the visible
  // list when the user is browsing all namespaces.
  const listKey = queryKeys.cluster(args.cluster).kind(args.kind).all;

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
    mutationFn: async (vars) => {
      const identity: Identity = {
        apiVersion: meta.group ? `${meta.group}/${meta.version}` : meta.version,
        kind: meta.kind,
        name: args.name,
        namespace: args.namespace || undefined,
      };
      const ops: Op[] = [
        { op: "replace", path: ["metadata", "labels"], value: vars.labels },
      ];
      // Pure minimal-diff SSA (issue #224): apply body is identity +
      // metadata.labels only. See TODO at the top of the file for the
      // per-key diff follow-up.
      const yaml = buildMinimalSSA(ops, identity);
      return applyWithLenientConflict(
        {
          cluster: args.cluster,
          group: meta.group,
          version: meta.version,
          resource: meta.resource,
          namespace: args.namespace || undefined,
          name: args.name,
          yaml,
        },
        "update labels",
      );
    },
    successToast: () => `updated labels on ${args.name}`,
    errorToast: (err) =>
      `failed to update labels on ${args.name}: ${err?.message ?? "unknown"}`,
  });
}
