// _applyWithLenientConflict — shared mutation-fn wrapper for targeted
// single-field SSA actions (scale, label edit, future: annotate, toggle
// suspend/cordon if they want the same UX).
//
// The problem this solves: when a Deployment was originally created via
// `kubectl apply`, Rancher, or any tool other than Periscope, the
// existing field manager owns spec.replicas / metadata.labels / etc.
// Periscope's SSA writes go out with FieldManager=periscope-spa and
// Force=false, so the apiserver returns 409 FieldManagerConflict. The
// raw error ("409 on /api/clusters/.../deployments/...") is opaque and
// the affected actions look broken to the operator.
//
// The fix: classify the conflicting managers using the registry in
// lib/managers.ts. If every conflict is HUMAN (kubectl-*), UNKNOWN
// (unclassified custom controller), or PERISCOPE (ourselves), retry
// once with Force=true. We're only ever overwriting the single field
// the action's minimal SSA payload touches, so the takeover is
// well-scoped — the same thing kubectl does with --force-conflicts.
//
// If any conflict is GITOPS / HELM / CONTROLLER, we don't force: those
// will revert the change on the next reconcile (Flux), the next chart
// upgrade (Helm), or within seconds (HPA). Better to surface a clear
// "blocked by X on field Y, edit the source instead" message than to
// silently lose the change minutes later.

import {
  ApiError,
  api,
  type ClusterScopedKind,
  type ManagedFieldsEntry,
  type YamlKind,
} from "../../lib/api";
import {
  buildRetainedOwnershipBodyFromOps,
  ManagedFieldsUnavailableError,
} from "../../lib/applyBodyBuilder";
import { type Identity, type Op } from "../../lib/yamlPatch";
import { analyzeConflict, formatBlockingMessage } from "../../lib/conflictPolicy";

interface ApplyArgs {
  cluster: string;
  group: string;
  version: string;
  resource: string;
  namespace?: string;
  name: string;
  yaml: string;
}

interface RetainedApplyArgs extends Omit<ApplyArgs, "yaml"> {
  identity: Identity;
  ops: Op[];
  current: string;
  managedFields: ManagedFieldsEntry[] | null | undefined;
}

// The ClusterScopedKind union duplicated here so the helper can do
// a runtime membership check without re-exporting the array. Mirror
// of the type in lib/api.ts:234 — kept in sync by the compiler via
// the assignment below.
const CLUSTER_SCOPED_KINDS: ClusterScopedKind[] = [
  "namespaces",
  "pvs",
  "storageclasses",
  "clusterroles",
  "clusterrolebindings",
  "ingressclasses",
  "priorityclasses",
  "runtimeclasses",
  "nodes",
];
const CLUSTER_SCOPED_SET = new Set<string>(CLUSTER_SCOPED_KINDS);

/**
 * applyWithLenientConflict wraps api.applyResource with the
 * auto-takeover-on-safe-conflict policy described in the file header.
 *
 * @param actionPhrase  Verb used in the rewritten error message, e.g.
 *                      "scale", "update labels". Kept short — the toast
 *                      caller usually adds the resource name itself.
 */
/**
 * fetchCurrentYamlForKind picks the right namespaced vs cluster-scoped
 * GET endpoint based on the YamlKind enum. Mutation hooks call this
 * just before constructing the retained-ownership body so the builder
 * has current cluster values for periscope-spa's prior claims.
 */
export async function fetchCurrentYamlForKind(
  cluster: string,
  kind: YamlKind,
  namespace: string,
  name: string,
  signal?: AbortSignal,
): Promise<string> {
  if (CLUSTER_SCOPED_SET.has(kind)) {
    return api.clusterScopedYaml(cluster, kind as ClusterScopedKind, name, signal);
  }
  return api.yaml(
    cluster,
    kind as Exclude<YamlKind, ClusterScopedKind>,
    namespace,
    name,
    signal,
  );
}

/**
 * applyWithLenientConflictRetained — same lenient-conflict policy as
 * applyWithLenientConflict, but the request body is built via the
 * retained-ownership minimal patch (#181) instead of plain
 * buildMinimalSSA. Caller supplies the action's ops + identity plus
 * the current cluster YAML and managedFields snapshot; we re-assert
 * periscope-spa's prior claims alongside the user's edit so
 * untouched fields aren't released on every mutation.
 *
 * Callers fetch `current` + `managedFields` themselves so the right
 * API call (namespaced vs cluster-scoped) happens at the hook level
 * where the kind is statically known. `fetchCurrentYamlForKind` above
 * is the convenience for the namespaced/cluster-scoped routing.
 */
export async function applyWithLenientConflictRetained<T>(
  args: RetainedApplyArgs,
  actionPhrase: string,
): Promise<T> {
  const { identity, ops, current, managedFields, ...rest } = args;
  let yaml: string;
  try {
    ({ yaml } = buildRetainedOwnershipBodyFromOps({
      identity,
      ops,
      current,
      managedFields,
    }));
  } catch (e) {
    if (e instanceof ManagedFieldsUnavailableError) {
      // Should not happen — the caller is responsible for fetching
      // meta before reaching this wrapper. Re-throw as a programmer
      // error rather than letting the toast spell out an internal
      // message about the SSA body builder.
      throw new Error(
        "applyWithLenientConflictRetained called without managedFields — fetch api.getMeta first",
        { cause: e },
      );
    }
    throw e;
  }
  return applyWithLenientConflict<T>({ ...rest, yaml }, actionPhrase);
}

export async function applyWithLenientConflict<T>(
  args: ApplyArgs,
  actionPhrase: string,
): Promise<T> {
  try {
    return (await api.applyResource({ ...args, force: false })) as T;
  } catch (err) {
    const analysis = analyzeConflict(err);
    if (analysis === null) throw err;

    if (analysis.allSafeToTakeover) {
      // Retry once with Force=true. A second 409 here would be unusual
      // (forcing past HUMAN/UNKNOWN managers should always succeed); if
      // it does happen we let the new error propagate untouched so the
      // user sees the real apiserver message.
      return (await api.applyResource({ ...args, force: true })) as T;
    }

    // At least one cause comes from a manager category we won't
    // auto-force. Rewrite the error so the toast tells the operator
    // *why* and *what to do*, instead of "409 on /api/clusters/...".
    const message = formatBlockingMessage(actionPhrase, analysis.firstBlocking!);
    const apiErr = err as ApiError;
    throw new ApiError(message, apiErr.status, apiErr.bodyText);
  }
}
