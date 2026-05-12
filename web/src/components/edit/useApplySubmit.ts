// useApplySubmit — thin form-mode submit pipeline. Dry-run, then
// apply, then invalidate the editor's react-query caches so the
// page re-fetches the live object. Errors surface as a banner the
// caller renders.
//
// As of #181 this hook routes through `buildRetainedOwnershipBody`,
// the same retained-ownership minimal-patch builder YamlEditor uses,
// so form-mode applies no longer claim ownership of every field in
// the resource — they only claim what the user actually edited plus
// what periscope-spa already owned.
//
// The commit path also routes through `applyWithLenientConflictRetained`
// (same wrapper the row-action mutation hooks use). This means:
//   - HUMAN-class conflicts (kubectl-*, Rancher, unclassified) silently
//     auto-takeover on a second attempt — same UX as scale / labels /
//     suspend / restart / cordon.
//   - GITOPS / HELM / CONTROLLER conflicts surface a classified
//     "blocked by X" message instead of the raw apiserver 409 body.
// Dry-run stays on the raw `api.applyResource` call (the wrapper is
// commit-only — dry-runs can't conflict).

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
import { applyWithLenientConflictRetained } from "../../hooks/mutations/_applyWithLenientConflict";
import { parseIdentityFromYaml } from "../../lib/k8sSchema";
import { MultiDocumentError, YamlParseError, computeOps } from "../../lib/yamlPatch";

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

      // Defensive meta gate at the top so we don't reach the lenient
      // wrapper with managedFields=null (which would otherwise throw
      // an internal "fetch api.getMeta first" error). The builder also
      // throws ManagedFieldsUnavailableError, but checking here keeps
      // both this check and the lenient wrapper's call below in the
      // same code path with a single, user-friendly message.
      if (!meta) {
        setState({
          kind: "error",
          message: "Ownership info is still loading — try again in a moment.",
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

      // ops here are the *user-edit* ops (computeOps over baseline/draft).
      // The retained-ownership body the apiserver sees was already built
      // above; the wrapper needs the same ops shape to reconstruct an
      // equivalent body via buildRetainedOwnershipBodyFromOps. Passing
      // them avoids re-parsing baseline/draft twice.
      const ops = computeOps(baseline, draft);

      const baseArgs = {
        cluster: resource.cluster,
        group: resource.group,
        version: resource.version,
        resource: resource.resource,
        namespace: resource.namespace,
        name: resource.name,
      };
      try {
        // L4 dry-run via the raw client. The wrapper is commit-only
        // (auto-takeover doesn't make sense for dry-run since the
        // apiserver never mutates state). We still want pre-flight
        // validation surface (PSA, webhooks, schema) before claiming
        // ownership of anything.
        setState({ kind: "dryRunning" });
        await api.applyResource({ ...baseArgs, yaml, dryRun: true }, ac.signal);

        // L5 commit through the lenient retain wrapper — same auto-
        // takeover + classified-error behaviour the row actions get.
        setState({ kind: "applying" });
        await applyWithLenientConflictRetained(
          {
            ...baseArgs,
            identity,
            ops,
            current,
            managedFields: meta.managedFields,
          },
          "apply",
        );
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
