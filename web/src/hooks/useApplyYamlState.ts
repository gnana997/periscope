// useApplyYamlState — owns the ApplyYamlDialog's state.
//
// Lifted out of the dialog component so it can be tested in isolation
// (parsing + result transitions) without dragging in Modal / Monaco.
//
// Responsibilities:
//   - yamlText buffer, parsed-doc derivation
//   - per-doc results map (dry-run + apply outcomes)
//   - busy state machine (idle | dry-run | apply)
//   - concurrent dry-run / apply orchestration with a 6-way fan-out
//     limiter (matches multiYaml.ts; same browser-connection-budget
//     reasoning)
//   - cancel via AbortController

import { useCallback, useMemo, useRef, useState } from "react";
import {
  type ParsedDoc,
  parseMultiDocYaml,
  gvrFromApiVersionAndKind,
} from "../lib/applyYamlParser";
import { ApiError, api } from "../lib/api";

const MAX_PARALLEL = 6;

export type DocResultState =
  | "idle"
  | "pending"
  | "success"
  | "failure"
  | "conflict";

export interface DocResult {
  state: DocResultState;
  /** Dry-run output rendered as a YAML/diff string. */
  diff?: string;
  /** Human-readable error message; populated on `failure` / `conflict`. */
  errorMessage?: string;
  /** Underlying error for handlers that want to inspect status codes. */
  error?: ApiError;
}

export type DialogBusy = "idle" | "dry-run" | "apply";

export interface UseApplyYamlState {
  yamlText: string;
  setYamlText: (next: string) => void;
  docs: ParsedDoc[];
  results: ReadonlyMap<string, DocResult>;
  busy: DialogBusy;
  /** Cancel any in-flight orchestration. No-op when idle. */
  cancel: () => void;
  /** Reset everything. Called when the dialog closes or operator clears. */
  reset: () => void;
  /** Run dry-run for every valid doc; populates results.diff per doc. */
  runDryRun: (cluster: string) => Promise<void>;
  /** Run real apply for every valid doc. Skips docs with parse errors. */
  runApply: (cluster: string) => Promise<void>;
  /** Retry a single conflicted doc with force=true. */
  forceApplyOne: (doc: ParsedDoc, cluster: string) => Promise<void>;
}

export function useApplyYamlState(): UseApplyYamlState {
  const [yamlText, setYamlText] = useState("");
  const [busy, setBusy] = useState<DialogBusy>("idle");
  const [results, setResults] = useState<Map<string, DocResult>>(
    () => new Map(),
  );
  const abortRef = useRef<AbortController | null>(null);

  const docs = useMemo(() => parseMultiDocYaml(yamlText), [yamlText]);

  const reset = useCallback(() => {
    abortRef.current?.abort();
    abortRef.current = null;
    setYamlText("");
    setResults(new Map());
    setBusy("idle");
  }, []);

  const cancel = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const updateResult = useCallback((id: string, patch: DocResult) => {
    setResults((prev) => {
      const next = new Map(prev);
      next.set(id, patch);
      return next;
    });
  }, []);

  const runOne = useCallback(
    async (
      doc: ParsedDoc,
      cluster: string,
      dryRun: boolean,
      signal: AbortSignal,
    ): Promise<void> => {
      if (!doc.valid || !doc.apiVersion || !doc.kind || !doc.name) return;
      const gvr = gvrFromApiVersionAndKind(doc.apiVersion, doc.kind);
      updateResult(doc.id, { state: "pending" });
      try {
        const response = await api.applyResource(
          {
            cluster,
            group: gvr.group,
            version: gvr.version,
            resource: gvr.resource,
            namespace: doc.namespace,
            name: doc.name,
            yaml: doc.raw,
            dryRun,
          },
          signal,
        );
        // applyResource returns the post-apply object as JSON. For
        // dry-run we render it as the diff payload; for real apply we
        // just mark success.
        updateResult(doc.id, {
          state: "success",
          diff: dryRun ? safeStringify(response) : undefined,
        });
      } catch (err) {
        if (signal.aborted) return;
        const apiErr = err instanceof ApiError ? err : null;
        const status = apiErr?.status ?? 0;
        const isConflict = status === 409;
        updateResult(doc.id, {
          state: isConflict ? "conflict" : "failure",
          errorMessage: errorMessage(err),
          error: apiErr ?? undefined,
        });
      }
    },
    [updateResult],
  );

  /**
   * forceApplyOne — retry a single doc with force=true. Used when
   * runApply() leaves a doc in `conflict` state and the operator
   * confirms takeover via the per-row Force button. Independent of
   * the batch worker pool — runs immediately, ignores the busy
   * state machine, and updates only this doc's result entry.
   */
  const forceApplyOne = useCallback(
    async (doc: ParsedDoc, cluster: string): Promise<void> => {
      if (!doc.valid || !doc.apiVersion || !doc.kind || !doc.name) return;
      const gvr = gvrFromApiVersionAndKind(doc.apiVersion, doc.kind);
      const ctrl = new AbortController();
      updateResult(doc.id, { state: "pending" });
      try {
        await api.applyResource(
          {
            cluster,
            group: gvr.group,
            version: gvr.version,
            resource: gvr.resource,
            namespace: doc.namespace,
            name: doc.name,
            yaml: doc.raw,
            force: true,
          },
          ctrl.signal,
        );
        updateResult(doc.id, { state: "success" });
      } catch (err) {
        const apiErr = err instanceof ApiError ? err : null;
        updateResult(doc.id, {
          state: "failure",
          errorMessage: errorMessage(err),
          error: apiErr ?? undefined,
        });
      }
    },
    [updateResult],
  );


  const runBatch = useCallback(
    async (cluster: string, dryRun: boolean): Promise<void> => {
      if (busy !== "idle") return;
      const valid = docs.filter((d) => d.valid);
      if (valid.length === 0) return;

      const ctrl = new AbortController();
      abortRef.current = ctrl;
      setBusy(dryRun ? "dry-run" : "apply");

      // Worker-pool concurrency limiter. Pull next index off `cursor`
      // until exhausted. Same shape as multiYaml.ts.
      let cursor = 0;
      const worker = async () => {
        while (true) {
          if (ctrl.signal.aborted) return;
          const idx = cursor++;
          if (idx >= valid.length) return;
          await runOne(valid[idx], cluster, dryRun, ctrl.signal);
        }
      };

      try {
        await Promise.all(
          Array.from({ length: Math.min(MAX_PARALLEL, valid.length) }, worker),
        );
      } finally {
        if (abortRef.current === ctrl) abortRef.current = null;
        setBusy("idle");
      }
    },
    [busy, docs, runOne],
  );

  const runDryRun = useCallback(
    (cluster: string) => runBatch(cluster, true),
    [runBatch],
  );
  const runApply = useCallback(
    (cluster: string) => runBatch(cluster, false),
    [runBatch],
  );

  return {
    yamlText,
    setYamlText,
    docs,
    results,
    busy,
    cancel,
    reset,
    runDryRun,
    runApply,
    forceApplyOne,
  };
}

function safeStringify(v: unknown): string {
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return "";
  }
}

function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    // The error message has the URL in it; trim to the status part for
    // a tidier per-doc display.
    return err.message.replace(/ on \/api\/.*$/, "");
  }
  if (err instanceof Error) return err.message;
  return String(err);
}
