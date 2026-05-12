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
//
// As of #181 this hook routes through `buildRetainedOwnershipBody`,
// the same retained-ownership minimal-patch builder YamlEditor uses,
// so form-mode applies no longer claim ownership of every field in
// the resource — they only claim what the user actually edited plus
// what periscope-spa already owned.

import { useCallback, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, type ResourceMeta, type ResourceRef } from "../../lib/api";
import {
  buildRetainedOwnershipBody,
  ManagedFieldsUnavailableError,
} from "../../lib/applyBodyBuilder";
import {
  invalidateAfterApply,
  type EditorSource,
} from "../../lib/customResources";
import { parseIdentityFromYaml } from "../../lib/k8sSchema";
import { MultiDocumentError, YamlParseError } from "../../lib/yamlPatch";

export type SubmitState =
  | { kind: "idle" }
  | { kind: "dryRunning" }
  | { kind: "applying" }
  | { kind: "success" }
  | { kind: "error"; message: string; status?: number; isConflict: boolean };

/**
 * Maps errors thrown while constructing the apply body into the
 * `error` SubmitState the banner renders. Returns `null` for errors
 * the hook should re-throw (programmer bugs, not user-facing).
 *
 * Pure — exported for testing.
 */
export function classifyBuildError(e: unknown): SubmitState | null {
  if (e instanceof ManagedFieldsUnavailableError) {
    return {
      kind: "error",
      message: "Ownership info is still loading — try again in a moment.",
      isConflict: false,
    };
  }
  if (e instanceof MultiDocumentError || e instanceof YamlParseError) {
    return {
      kind: "error",
      message: (e as Error).message,
      isConflict: false,
    };
  }
  return null;
}

export interface SubmitInput {
  /** YAML the user mounted against (anchor for the user-edits diff). */
  baseline: string;
  /** Current form/YAML buffer (the user's intent). */
  draft: string;
  /**
   * Latest server YAML — values for prior-owned paths the user hasn't
   * touched. Caller threads this from the live `useEditorYaml` cache.
   */
  current: string;
  /**
   * ResourceMeta snapshot from `useResourceMeta`. `null` means the
   * 15s meta poll hasn't landed yet; we fail closed rather than apply
   * with an empty ownership set (which would behave like a first-apply
   * and silently release ownership of prior periscope-spa claims).
   */
  meta: ResourceMeta | null;
}

export interface UseApplySubmit {
  state: SubmitState;
  submit: (input: SubmitInput) => Promise<boolean>;
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
    async ({ baseline, draft, current, meta }: SubmitInput): Promise<boolean> => {
      if (abortRef.current) abortRef.current.abort();
      const ac = new AbortController();
      abortRef.current = ac;

      // Identity parsing precedes the builder so a malformed draft
      // surfaces the right error rather than going through the
      // builder's ManagedFieldsUnavailableError path.
      const identity = parseIdentityFromYaml(draft);
      if (!identity) {
        setState({
          kind: "error",
          message:
            "Could not parse apiVersion/kind/metadata.name from the buffer.",
          isConflict: false,
        });
        return false;
      }

      let yaml: string;
      try {
        ({ yaml } = buildRetainedOwnershipBody({
          baseline,
          draft,
          current,
          identity,
          managedFields: meta?.managedFields ?? null,
        }));
      } catch (e) {
        const classified = classifyBuildError(e);
        if (classified) {
          setState(classified);
          return false;
        }
        throw e;
      }

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
