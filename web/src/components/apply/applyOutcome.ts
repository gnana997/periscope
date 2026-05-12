// applyOutcome — pure derivation of the dialog's footer state from
// the hook's reactive inputs. Lives outside the component so vitest
// can exercise the branch matrix without dragging in Modal + Monaco.
//
// Why a discriminated union: the footer has four mutually-exclusive
// shapes (running, pre-apply, success, partial); rendering reads a
// single `kind` discriminant instead of recomputing flags. Same data
// model already used by other detail panes (e.g. helm release).

import type {
  DialogBusy,
  DocResult,
  LastBatchKind,
} from "../../hooks/useApplyYamlState";

export type ApplyOutcomeKind =
  | "idle" //  no valid docs entered yet
  | "running" //  a dry-run or apply is in flight
  | "pre-apply" //  docs ready, not yet run
  | "success" //  apply finished, every valid doc succeeded
  | "partial"; //  apply finished, at least one failure / conflict / pending

export interface ApplyOutcome {
  kind: ApplyOutcomeKind;
  successCount: number;
  failureCount: number;
  conflictCount: number;
  pendingCount: number;
  /** Number of valid docs in the batch (denominator for the banner). */
  totalCount: number;
}

export function deriveApplyOutcome(args: {
  busy: DialogBusy;
  lastBatchKind: LastBatchKind;
  results: ReadonlyMap<string, DocResult>;
  validCount: number;
}): ApplyOutcome {
  const { busy, lastBatchKind, results, validCount } = args;

  let successCount = 0;
  let failureCount = 0;
  let conflictCount = 0;
  let pendingCount = 0;
  for (const r of results.values()) {
    if (r.state === "success") successCount++;
    else if (r.state === "failure") failureCount++;
    else if (r.state === "conflict") conflictCount++;
    else if (r.state === "pending") pendingCount++;
  }

  const counts = {
    successCount,
    failureCount,
    conflictCount,
    pendingCount,
    totalCount: validCount,
  };

  if (busy !== "idle") return { ...counts, kind: "running" };

  // Only an apply batch produces a banner. Dry-run populates results
  // too, but we want the operator to see "pre-apply" so they know
  // they still need to commit.
  if (lastBatchKind === "apply") {
    const allDone =
      successCount + failureCount + conflictCount === validCount &&
      pendingCount === 0 &&
      validCount > 0;
    const allSuccess =
      allDone && failureCount === 0 && conflictCount === 0;
    if (allSuccess) return { ...counts, kind: "success" };
    if (successCount + failureCount + conflictCount > 0) {
      return { ...counts, kind: "partial" };
    }
  }

  if (validCount > 0) return { ...counts, kind: "pre-apply" };
  return { ...counts, kind: "idle" };
}
