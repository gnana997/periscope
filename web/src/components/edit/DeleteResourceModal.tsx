// DeleteResourceModal — type-the-name confirmation for destructive deletes.
//
// kubectl delete is a one-step verb; for a UI, type-the-name is the
// industry-standard friction (GitHub repo delete, Stripe API key
// revoke, etc.). It prevents the muscle-memory delete that operators
// do at 11pm and immediately regret.
//
// Background propagation is the only mode v1 ships — same as kubectl's
// default. Foreground / Orphan are different verbs operationally and
// will get their own UI when actually needed.

import { useEffect, useState } from "react";
import { ApiError, api } from "../../lib/api";
import type { ResourceRef } from "./EditResourceModal";

interface DeleteResourceModalProps {
  resourceRef: ResourceRef;
  onClose: () => void;
  onDeleted?: () => void;
}

type DeleteState =
  | { kind: "idle" }
  | { kind: "running" }
  | { kind: "error"; message: string; status?: number };

export function DeleteResourceModal({
  resourceRef,
  onClose,
  onDeleted,
}: DeleteResourceModalProps) {
  const [confirm, setConfirm] = useState("");
  const [state, setState] = useState<DeleteState>({ kind: "idle" });

  const matches = confirm === resourceRef.name;
  const kindLabel = (resourceRef.kind ?? resourceRef.resource).toLowerCase();

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && state.kind !== "running") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, state.kind]);

  async function run() {
    setState({ kind: "running" });
    try {
      await api.deleteResource({
        cluster: resourceRef.cluster,
        group: resourceRef.group,
        version: resourceRef.version,
        resource: resourceRef.resource,
        namespace: resourceRef.namespace,
        name: resourceRef.name,
      });
      onDeleted?.();
      onClose();
    } catch (err) {
      const apiErr = err instanceof ApiError ? err : null;
      setState({
        kind: "error",
        status: apiErr?.status,
        message:
          apiErr?.bodyText?.trim() ||
          (err as Error)?.message ||
          "delete failed",
      });
    }
  }

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="delete-resource-title"
      onClick={(e) => {
        if (e.target === e.currentTarget && state.kind !== "running") onClose();
      }}
    >
      <div className="w-full max-w-md rounded-md border border-border-strong bg-surface shadow-2xl">
        <div className="border-b border-border px-5 py-3 font-mono text-sm">
          <span className="text-red">delete</span>{" "}
          <span id="delete-resource-title" className="text-ink">
            {kindLabel} {resourceRef.name}
          </span>
        </div>

        <div className="space-y-4 px-5 py-4">
          <p className="text-sm text-ink-muted">
            this will delete{" "}
            <span className="font-mono text-ink">{kindLabel}/{resourceRef.name}</span>
            {resourceRef.namespace && (
              <>
                {" "}in namespace{" "}
                <span className="font-mono text-ink">{resourceRef.namespace}</span>
              </>
            )}
            . propagation is <span className="font-mono">background</span> —
            owned objects will be deleted asynchronously.
          </p>
          <p className="text-sm text-ink-muted">
            type{" "}
            <span className="font-mono text-ink">{resourceRef.name}</span> to
            confirm:
          </p>
          <input
            type="text"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            disabled={state.kind === "running"}
            autoFocus
            className="w-full rounded-sm border border-border bg-bg px-3 py-2 font-mono text-sm text-ink outline-none focus:border-ink-muted"
            placeholder={resourceRef.name}
            spellCheck={false}
            aria-label={`Type ${resourceRef.name} to confirm deletion`}
          />

          {state.kind === "error" && (
            <div className="rounded-sm border border-red bg-red-soft px-3 py-2 font-mono text-xs text-red">
              <div className="mb-1 font-semibold">
                {state.status ? `error ${state.status}` : "error"}
              </div>
              <pre className="whitespace-pre-wrap">{state.message}</pre>
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
          <button
            type="button"
            onClick={onClose}
            disabled={state.kind === "running"}
            className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-sm text-ink-muted transition-colors hover:border-ink-muted hover:text-ink disabled:opacity-50"
          >
            cancel
          </button>
          <button
            type="button"
            onClick={run}
            disabled={!matches || state.kind === "running"}
            className="rounded-sm bg-red px-3 py-1.5 font-mono text-sm text-bg transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
          >
            {state.kind === "running" ? "deleting…" : "delete"}
          </button>
        </div>
      </div>
    </div>
  );
}
