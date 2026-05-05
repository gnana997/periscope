// useApplyYamlState — owns the ApplyYamlDialog's state.
//
// Lifted out of the dialog component so it can be tested in isolation
// (parsing + result transitions) without dragging in Modal / Monaco.
//
// Skeleton in this commit; orchestration arrives in later commits.

import { useCallback, useMemo, useState } from "react";
import { type ParsedDoc, parseMultiDocYaml } from "../lib/applyYamlParser";
import type { ApiError } from "../lib/api";

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
  /**
   * Reset everything except cluster context. Called when the operator
   * closes the dialog or hits a Clear action.
   */
  reset: () => void;
}

export function useApplyYamlState(): UseApplyYamlState {
  const [yamlText, setYamlText] = useState("");
  const [busy] = useState<DialogBusy>("idle");
  const [results] = useState<Map<string, DocResult>>(() => new Map());

  const docs = useMemo(() => parseMultiDocYaml(yamlText), [yamlText]);

  const reset = useCallback(() => {
    setYamlText("");
  }, []);

  return {
    yamlText,
    setYamlText,
    docs,
    results,
    busy,
    reset,
  };
}
