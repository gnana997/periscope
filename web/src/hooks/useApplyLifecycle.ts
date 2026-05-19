// useApplyLifecycle — the React glue that wires the pure
// applyStateReducer into the YAML editor's apply pipeline.
//
// Owns:
//   • The reducer state via useReducer.
//   • One AbortController shared across submit / dryRun / force —
//     a new action cancels the in-flight one.
//   • The api.applyResource calls: dry-run gate + commit, force=true,
//     etc.
//   • A 1500ms auto-dismiss timer fired after dry-run Success.
//   • The onCommitSuccess callback (typically: invalidate react-query
//     caches, close the editor pane). Called after commit Success;
//     not called for dry-run.
//
// Exposes: the reducer state, plus action callbacks. Components NEVER
// call dispatch directly — they call these wrappers, which combine
// the state transition with the corresponding side effect.
//
// Pure: toApplyResult exported separately so its branching can be
// tested without React.

import { useCallback, useEffect, useReducer, useRef } from "react";
import { ApiError, api, type ResourceRef } from "../lib/api";
import { buildApplyBody } from "../lib/applyBodyBuilder";
import { parseConflictForBanner } from "../lib/conflictPolicy";
import {
  applyStateReducer,
  initialApplyState,
  type ApplyResult,
  type ApplyState,
  type ConflictInfo,
} from "./useApplyState";
import { MultiDocumentError, YamlParseError, type Identity } from "../lib/yamlPatch";

/**
 * toApplyResult maps an error thrown by api.applyResource (or the
 * body-builder) into the ApplyResult shape the reducer consumes.
 *
 * Pure — exported for testing. Branching covered:
 *   • ApiError 409 with parseable conflict info → { ok: false, 409, conflict }
 *   • ApiError 409 without parseable conflict info → { ok: false, 409 } (no conflict)
 *   • Other ApiError → { ok: false, status, message }
 *   • MultiDocumentError / YamlParseError → { ok: false, 0, message } (pre-network)
 *   • Anything else → { ok: false, 0, message: e.message ?? "apply failed" }
 *
 * The reducer's resolveFrom handles the routing from here:
 *   • 409 + conflict → ForceRequired
 *   • everything-else-fail → Error
 */
export function toApplyResult(
  err: unknown,
  parseConflict: (err: unknown) => ConflictInfo | null = parseConflictForBanner,
): ApplyResult {
  if (err instanceof ApiError) {
    if (err.status === 409) {
      const conflict = parseConflict(err);
      return conflict
        ? {
            ok: false,
            status: 409,
            message: err.bodyText || err.message || "Apply conflict",
            conflict,
          }
        : {
            ok: false,
            status: 409,
            message: err.bodyText || err.message || "Apply conflict",
          };
    }
    return {
      ok: false,
      status: err.status,
      message: err.bodyText || err.message || "apply failed",
    };
  }
  if (err instanceof MultiDocumentError || err instanceof YamlParseError) {
    return { ok: false, status: 0, message: err.message };
  }
  const message = err instanceof Error ? err.message : "apply failed";
  return { ok: false, status: 0, message };
}

interface UseApplyLifecycleArgs {
  resource: ResourceRef;
  /** YAML the user mounted against (anchor for the diff). */
  baseline: string;
  /** Current buffer. */
  draft: string;
  /** Parsed identity; null disables every action (no apply target). */
  identity: Identity | null;
  /**
   * Called after a successful COMMIT (not dry-run). Typically:
   * await invalidateAfterApply(...), then drop ?edit=1 from the URL.
   * The caller closes over whatever it needs (qc, source, setParams).
   */
  onCommitSuccess: () => Promise<void> | void;
}

export interface UseApplyLifecycle {
  state: ApplyState;
  submit: () => void;
  dryRun: () => void;
  force: () => void;
  cancel: () => void;
  dismiss: () => void;
  retry: () => void;
}

const DRY_RUN_SUCCESS_TIMEOUT_MS = 1500;

export function useApplyLifecycle({
  resource,
  baseline,
  draft,
  identity,
  onCommitSuccess,
}: UseApplyLifecycleArgs): UseApplyLifecycle {
  const [state, dispatch] = useReducer(applyStateReducer, initialApplyState);
  const abortRef = useRef<AbortController | null>(null);
  const dismissTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Cancel any in-flight request + clear the auto-dismiss timer on
  // unmount. Without this, an apply still running when the operator
  // navigates away can land on a stale qc / setState pair.
  useEffect(() => {
    return () => {
      abortRef.current?.abort();
      if (dismissTimerRef.current !== null) clearTimeout(dismissTimerRef.current);
    };
  }, []);

  const clearDismissTimer = () => {
    if (dismissTimerRef.current !== null) {
      clearTimeout(dismissTimerRef.current);
      dismissTimerRef.current = null;
    }
  };

  const runApply = useCallback(
    async (mode: "submit" | "dryRun" | "force") => {
      if (!identity) return;

      // Build the body up front so YAML parse / multi-doc errors land
      // as Error state without an api.applyResource round trip.
      let yaml: string;
      try {
        ({ yaml } = buildApplyBody(baseline, draft, identity));
      } catch (e) {
        dispatch({ type: mode === "dryRun" ? "dryRun" : "submit" });
        dispatch({ type: "resolved", result: toApplyResult(e) });
        return;
      }

      // Kick off the new operation; cancel any prior in-flight one.
      abortRef.current?.abort();
      const ac = new AbortController();
      abortRef.current = ac;
      clearDismissTimer();

      // For submit: dispatch "submit", run dry-run + commit.
      // For dryRun: dispatch "dryRun", run dry-run only.
      // For force: state is already in ForceRequired; the reducer's
      //   "force" action moves it to Forcing. The caller (`force()`
      //   below) dispatches that before reaching here.
      if (mode === "submit") dispatch({ type: "submit" });
      if (mode === "dryRun") dispatch({ type: "dryRun" });

      const baseArgs = {
        cluster: resource.cluster,
        group: resource.group,
        version: resource.version,
        resource: resource.resource,
        namespace: resource.namespace,
        name: resource.name,
        yaml,
      };

      try {
        if (mode === "submit") {
          // L4 dry-run gate before the commit. force is always false
          // here — the operator hasn't seen a conflict yet.
          await api.applyResource({ ...baseArgs, dryRun: true, force: false }, ac.signal);
          await api.applyResource({ ...baseArgs, dryRun: false, force: false }, ac.signal);
        } else if (mode === "dryRun") {
          await api.applyResource({ ...baseArgs, dryRun: true, force: false }, ac.signal);
        } else {
          // mode === "force": commit with force=true. We skip the
          // dry-run gate here — the operator already saw the conflict
          // info from the prior dry-run/submit and explicitly chose to
          // proceed. A second dry-run would either reproduce the same
          // 409 (wasted round-trip) or pass against a transient state
          // we'd then immediately commit against anyway.
          await api.applyResource({ ...baseArgs, dryRun: false, force: true }, ac.signal);
        }
        dispatch({ type: "resolved", result: { ok: true } });
        if (mode === "dryRun") {
          // Auto-clear so the operator's next action sees Idle. The
          // reducer's Success state is brief by design; the hook owns
          // the timer because the reducer must stay pure.
          dismissTimerRef.current = setTimeout(() => {
            if (!ac.signal.aborted) dispatch({ type: "dismiss" });
            dismissTimerRef.current = null;
          }, DRY_RUN_SUCCESS_TIMEOUT_MS);
        } else {
          // Commit success: invalidate caches + close editor.
          await onCommitSuccess();
        }
      } catch (e) {
        if (ac.signal.aborted) return;
        dispatch({ type: "resolved", result: toApplyResult(e) });
      }
    },
    // qc and source are intentionally absent — they're only used
    // indirectly through onCommitSuccess (which is in the dep set).
    // identity / baseline / draft / resource flow into buildApplyBody
    // and the request args, so they're load-bearing here.
    [identity, baseline, draft, resource, onCommitSuccess],
  );

  const submit = useCallback(() => void runApply("submit"), [runApply]);
  const dryRun = useCallback(() => void runApply("dryRun"), [runApply]);
  const force = useCallback(() => {
    dispatch({ type: "force" });
    void runApply("force");
  }, [runApply]);
  const cancel = useCallback(() => {
    abortRef.current?.abort();
    clearDismissTimer();
    dispatch({ type: "cancel" });
  }, []);
  const dismiss = useCallback(() => {
    clearDismissTimer();
    dispatch({ type: "dismiss" });
  }, []);
  const retry = useCallback(() => {
    // Retry is "submit again" — the reducer transitions Error → Submitting
    // on retry, and runApply("submit") fires the side effect. We don't
    // call submit() directly because submit() dispatches "submit" which
    // would be ignored from Error state; "retry" is the explicit form.
    dispatch({ type: "retry" });
    void runApply("submit");
  }, [runApply]);

  return { state, submit, dryRun, force, cancel, dismiss, retry };
}

