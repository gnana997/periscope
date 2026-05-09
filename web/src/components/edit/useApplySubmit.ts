// useApplySubmit — thin form-mode submit pipeline. Dry-run, then
// apply, then invalidate the editor's react-query caches so the
// page re-fetches the live object. Errors surface as a banner the
// caller renders.
//
// This is intentionally narrower than YamlEditor's apply
// orchestration: no field-conflict resolution view (#116 form mode
// links operators to the YAML editor for that), no drift overlay,
// no patch preview. The backend pipeline behind `api.applyResource`
// is identical, so SSA semantics, the per-doc SelfSubjectAccessReview
// pre-flight (PR #100), and audit-log emission all still happen.
// Operators who hit a 409 conflict are routed to YAML mode where
// the full ConflictResolutionView is available.

import { useCallback, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, type ResourceRef } from "../../lib/api";
import {
  invalidateAfterApply,
  type EditorSource,
} from "../../lib/customResources";

export type SubmitState =
  | { kind: "idle" }
  | { kind: "dryRunning" }
  | { kind: "applying" }
  | { kind: "success" }
  | { kind: "error"; message: string; status?: number; isConflict: boolean };

export interface UseApplySubmit {
  state: SubmitState;
  submit: (yaml: string) => Promise<boolean>;
  reset: () => void;
}

export function useApplySubmit(
  source: EditorSource,
  resource: ResourceRef,
): UseApplySubmit {
  const qc = useQueryClient();
  const [state, setState] = useState<SubmitState>({ kind: "idle" });
  const abortRef = useRef<AbortController | null>(null);

  const submit = useCallback(
    async (yaml: string): Promise<boolean> => {
      if (abortRef.current) abortRef.current.abort();
      const ac = new AbortController();
      abortRef.current = ac;
      const args = {
        cluster: resource.cluster,
        group: resource.group,
        version: resource.version,
        resource: resource.resource,
        namespace: resource.namespace,
        name: resource.name,
        yaml,
      };
      try {
        setState({ kind: "dryRunning" });
        await api.applyResource({ ...args, dryRun: true }, ac.signal);
        setState({ kind: "applying" });
        await api.applyResource({ ...args, dryRun: false }, ac.signal);
        await invalidateAfterApply(qc, source, resource);
        setState({ kind: "success" });
        return true;
      } catch (e) {
        if (ac.signal.aborted) return false;
        const isApiError = e instanceof ApiError;
        const status = isApiError ? e.status : undefined;
        const isConflict = status === 409;
        const message =
          (isApiError ? e.bodyText : undefined) ||
          (e instanceof Error ? e.message : "apply failed");
        setState({ kind: "error", message, status, isConflict });
        return false;
      }
    },
    [qc, resource, source],
  );

  const reset = useCallback(() => setState({ kind: "idle" }), []);

  return { state, submit, reset };
}
