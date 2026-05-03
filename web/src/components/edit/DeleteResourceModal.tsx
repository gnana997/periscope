// DeleteResourceModal — type-the-name confirmation for destructive deletes.
//
// Presentational only as of Lane 2: the mutation lives in
// useDeleteResource (web/src/hooks/mutations/useDeleteResource.ts), which
// optimistically removes the row from the list cache before the DELETE
// fires. This modal owns confirmation UX (type-the-name + cancel/run
// buttons) and surfaces the hook's pending/error state.
//
// kubectl delete is a one-step verb; for a UI, type-the-name is the
// industry-standard friction (GitHub repo delete, Stripe API key
// revoke, etc.). It prevents the muscle-memory delete that operators
// do at 11pm and immediately regret.
//
// Background propagation is the only mode v1 ships — same as kubectl's
// default. Foreground / Orphan are different verbs operationally and
// will get their own UI when actually needed.

import { useState } from "react";
import type { ResourceRef } from "../../lib/api";
import { Modal } from "../ui/Modal";

interface DeleteResourceModalProps {
  resourceRef: ResourceRef;
  pending: boolean;
  error?: { status?: number; message: string } | null;
  onClose: () => void;
  onConfirm: () => void;
}

export function DeleteResourceModal({
  resourceRef,
  pending,
  error,
  onClose,
  onConfirm,
}: DeleteResourceModalProps) {
  const [confirm, setConfirm] = useState("");
  const matches = confirm === resourceRef.name;
  const kindLabel = (resourceRef.kind ?? resourceRef.resource).toLowerCase();

  return (
    <Modal
      open
      onClose={onClose}
      labelledBy="delete-resource-title"
      size="sm"
      dismissOnEsc={!pending}
      dismissOnBackdrop={!pending}
    >
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
          disabled={pending}
          autoFocus
          className="w-full rounded-sm border border-border bg-bg px-3 py-2 font-mono text-sm text-ink outline-none focus:border-ink-muted"
          placeholder={resourceRef.name}
          spellCheck={false}
          aria-label={`Type ${resourceRef.name} to confirm deletion`}
        />

        {error && (
          <div className="rounded-sm border border-red bg-red-soft px-3 py-2 font-mono text-xs text-red">
            <div className="mb-1 font-semibold">
              {error.status ? `error ${error.status}` : "error"}
            </div>
            <pre className="whitespace-pre-wrap">{error.message}</pre>
          </div>
        )}
      </div>

      <div className="flex items-center justify-end gap-2 border-t border-border px-5 py-3">
        <button
          type="button"
          onClick={onClose}
          disabled={pending}
          className="rounded-sm border border-border-strong px-3 py-1.5 font-mono text-sm text-ink-muted transition-colors hover:border-ink-muted hover:text-ink disabled:opacity-50"
        >
          cancel
        </button>
        <button
          type="button"
          onClick={onConfirm}
          disabled={!matches || pending}
          className="rounded-sm bg-red px-3 py-1.5 font-mono text-sm text-bg transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
        >
          {pending ? "deleting…" : "delete"}
        </button>
      </div>
    </Modal>
  );
}
