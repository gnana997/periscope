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

import { ApiError, api } from "../../lib/api";
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

/**
 * applyWithLenientConflict wraps api.applyResource with the
 * auto-takeover-on-safe-conflict policy described in the file header.
 *
 * @param actionPhrase  Verb used in the rewritten error message, e.g.
 *                      "scale", "update labels". Kept short — the toast
 *                      caller usually adds the resource name itself.
 */
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
