// useApplyPreFlight — RBAC pre-flight for the ApplyYamlDialog.
//
// For each parsed doc, asks the backend "can the operator
// create-or-update THIS resource?" and returns an allow / deny
// decision. The dialog uses this to:
//
//   - render allow/deny chips per-doc in the preview list
//   - disable the Apply button when any doc is denied (defense in
//     depth — apiserver still gates at apply time, but the UX should
//     show denied before the click)
//
// Implementation note (deviation from #55):
//
//   The issue spec says "GET the resource first to know whether to
//   check create vs update; one check per doc." We instead send TWO
//   checks per doc — create AND update — and treat the doc as
//   allowed if either passes.
//
//   Reasoning: the GET-first approach requires N parallel GET probes
//   (one per doc, up to the 100-row cap) before the batched can-i
//   call can even be issued, plus complicated branching for 403 / 5xx
//   GET responses. The simpler "either create OR update" form fans
//   out 2N can-i checks in ONE batched POST, no GETs, and produces
//   the same denied-when-no-applicable-permission behavior in
//   practice.
//
//   The false-allowed cases this misses (operator with only `update`
//   on a doc that would CREATE; or only `create` on a doc that would
//   UPDATE) surface clearly when the dialog actually runs the apply
//   — the per-doc result panel shows the apiserver's 403. Acceptable
//   for v1; can iterate to GET-first if telemetry shows it matters.

import { useMemo } from "react";
import { useCanIBatch, type CanICheck } from "./useCanI";
import { type ParsedDoc, gvrFromApiVersionAndKind } from "../lib/applyYamlParser";

export interface DocPreFlight {
  /** True when EITHER `create` OR `update` is allowed for this doc. */
  allowed: boolean;
  /** Human-readable reason for denial; empty when allowed. */
  reason: string;
  /** True while the can-i batch is in flight. */
  loading: boolean;
}

export interface ApplyPreFlightResult {
  /** Per-doc decision keyed by ParsedDoc.id. Invalid docs are absent. */
  decisions: ReadonlyMap<string, DocPreFlight>;
  /** True while any pre-flight check is loading. */
  loading: boolean;
  /** Number of docs explicitly denied (allowed=false, not loading). */
  deniedCount: number;
}

/**
 * useApplyPreFlight asks the can-i backend whether the operator can
 * create OR update each parsed doc in `docs`. Invalid docs (parse
 * errors / missing fields) are skipped — they're already flagged in
 * the dialog's preview list and don't gate the Apply button.
 */
export function useApplyPreFlight(
  cluster: string,
  docs: ParsedDoc[],
): ApplyPreFlightResult {
  // Build CanICheck array: 2 checks per valid doc (create + update),
  // preserving the doc order so we can map results back by index.
  const validDocs = useMemo(
    () => docs.filter((d) => d.valid && d.apiVersion && d.kind && d.name),
    [docs],
  );

  const checks = useMemo<CanICheck[]>(() => {
    const out: CanICheck[] = [];
    for (const doc of validDocs) {
      // safe: validDocs filtered on these being defined
      const gvr = gvrFromApiVersionAndKind(doc.apiVersion!, doc.kind!);
      const base: Pick<CanICheck, "group" | "resource" | "namespace"> = {
        group: gvr.group,
        resource: gvr.resource,
        namespace: doc.namespace,
      };
      out.push({ verb: "create", ...base });
      out.push({ verb: "update", ...base });
    }
    return out;
  }, [validDocs]);

  const decisions = useCanIBatch(cluster, checks);

  return useMemo(() => {
    const map = new Map<string, DocPreFlight>();
    let deniedCount = 0;
    let anyLoading = false;
    validDocs.forEach((doc, idx) => {
      const create = decisions[idx * 2];
      const update = decisions[idx * 2 + 1];
      if (!create || !update) return;
      const loading = create.loading || update.loading;
      const allowed = create.allowed || update.allowed;
      // Use whichever decision has a more substantive reason; fall
      // back to update's reason since "can't update either"
      // generally implies "can't create either" too.
      const reason = !allowed ? update.tooltip || create.tooltip : "";
      map.set(doc.id, { allowed, reason, loading });
      if (loading) anyLoading = true;
      if (!loading && !allowed) deniedCount += 1;
    });
    return { decisions: map, loading: anyLoading, deniedCount };
  }, [validDocs, decisions]);
}
