// RollbackModal — confirmation dialog for helm release rollback
// (#77). Mirrors UninstallReleaseModal's pattern for spinner +
// error + denial surfacing rather than the lighter
// ConfirmActionModal — rollback is destructive enough (manifest
// patch across the namespace, can't be undone except by a forward
// rollback) to warrant the same friction.
//
// Layout:
//   - Header: from rev → to rev transition
//   - Body: rendered diff (current vs target manifests) so the
//     operator can see what's about to change
//   - Advanced: wait / cleanupOnFail / disableHooks toggles
//   - Pre-flight denials surface inline when the backend returns
//     E_HELM_PREFLIGHT_DENIED — listed by GVR + verb so the operator
//     can take it to their cluster admin without re-trying
//
// The modal stays open while the mutation is pending and only
// dismisses on success (caller closes it). Cancel + ESC + backdrop
// are disabled while pending so the operator can't navigate away
// mid-rollback and lose the response.

import { useState } from "react";
import { useHelmDiff } from "../../hooks/useHelm";
import { InlineDiff } from "../detail/yaml/InlineDiff";
import { ErrorState, LoadingState } from "../table/states";
import { Modal } from "../ui/Modal";

export interface RollbackModalDenial {
  group: string;
  resource: string;
  verb: string;
  reason?: string;
}

export interface RollbackModalError {
  status?: number;
  message: string;
  denied?: RollbackModalDenial[];
}

interface RollbackModalProps {
  open: boolean;
  cluster: string;
  namespace: string;
  releaseName: string;
  currentRevision: number;
  targetRevision: number;
  pending: boolean;
  error?: RollbackModalError | null;
  onClose: () => void;
  onConfirm: (opts: {
    wait: boolean;
    cleanupOnFail: boolean;
    disableHooks: boolean;
  }) => void;
}

export function RollbackModal({
  open,
  cluster,
  namespace,
  releaseName,
  currentRevision,
  targetRevision,
  pending,
  error,
  onClose,
  onConfirm,
}: RollbackModalProps) {
  const diffQuery = useHelmDiff(
    cluster,
    namespace,
    releaseName,
    currentRevision,
    targetRevision,
  );

  const [wait, setWait] = useState(true);
  const [cleanupOnFail, setCleanupOnFail] = useState(false);
  const [disableHooks, setDisableHooks] = useState(false);

  // The submit button is disabled when:
  //  - the mutation is in flight
  //  - the diff hasn't loaded yet (we want the operator to SEE the
  //    diff before approving — denying the click protects them
  //    from a "what did I just rollback?" moment)
  //  - the backend has rejected with a denial; the operator must
  //    close + fix RBAC + re-open
  const blockedByDenial = (error?.denied?.length ?? 0) > 0;
  const canSubmit = !pending && !!diffQuery.data && !blockedByDenial;

  let diffBody: React.ReactNode;
  if (diffQuery.isLoading) {
    diffBody = <LoadingState resource="diff" />;
  } else if (diffQuery.isError) {
    diffBody = (
      <ErrorState
        title="couldn't compute diff"
        message={(diffQuery.error as Error)?.message ?? "unknown"}
      />
    );
  } else if (diffQuery.data) {
    diffBody = (
      <InlineDiff
        original={diffQuery.data.from.yaml}
        proposed={diffQuery.data.to.yaml}
      />
    );
  }

  return (
    <Modal
      open={open}
      onClose={pending ? () => {} : onClose}
      labelledBy="rollback-release-title"
      size="lg"
      dismissOnEsc={!pending}
      dismissOnBackdrop={!pending}
    >
      <div className="border-b border-border px-5 py-3 font-mono text-sm">
        <span className="text-accent">rollback</span>{" "}
        <span id="rollback-release-title" className="text-ink">
          {releaseName}
        </span>
        <span className="ml-2 text-ink-faint">
          revision {currentRevision} → {targetRevision}
        </span>
      </div>

      <div className="space-y-4 px-5 py-4">
        <p className="text-sm text-ink-muted">
          helm will compute the patch from revision{" "}
          <span className="font-mono text-ink">{currentRevision}</span> to{" "}
          <span className="font-mono text-ink">{targetRevision}</span> and
          apply it across namespace{" "}
          <span className="font-mono text-ink">{namespace}</span>. helm
          assigns a new revision; the rollback is itself reversible by
          another rollback.
        </p>

        <div className="h-64 overflow-y-auto rounded-sm border border-border">
          {diffBody}
        </div>

        <div className="space-y-2 border-t border-border pt-3 font-mono text-[11.5px]">
          <p className="text-ink-faint uppercase tracking-[0.16em]">
            advanced
          </p>
          <label className="flex items-start gap-2 text-ink-muted">
            <input
              type="checkbox"
              checked={wait}
              onChange={(e) => setWait(e.target.checked)}
              disabled={pending}
              className="mt-0.5"
            />
            <span>
              <span className="text-ink">wait</span> — block until rolled-back
              resources are ready (recommended)
            </span>
          </label>
          <label className="flex items-start gap-2 text-ink-muted">
            <input
              type="checkbox"
              checked={cleanupOnFail}
              onChange={(e) => setCleanupOnFail(e.target.checked)}
              disabled={pending}
              className="mt-0.5"
            />
            <span>
              <span className="text-ink">cleanup on fail</span> — remove
              partially-applied resources if the rollback errors mid-flight
            </span>
          </label>
          <label className="flex items-start gap-2 text-ink-muted">
            <input
              type="checkbox"
              checked={disableHooks}
              onChange={(e) => setDisableHooks(e.target.checked)}
              disabled={pending}
              className="mt-0.5"
            />
            <span>
              <span className="text-ink">disable hooks</span> — skip pre-/
              post-rollback hooks (only when a hook is the thing that's
              stuck)
            </span>
          </label>
        </div>

        {/* Pre-flight denial — explicit list of (verb, GVR) tuples
            so the operator can take it to their cluster admin. */}
        {blockedByDenial && (
          <div className="rounded-sm border border-yellow/40 bg-yellow/5 px-3 py-2 font-mono text-xs">
            <div className="mb-1 font-semibold uppercase tracking-[0.14em] text-yellow">
              rbac pre-flight denied
            </div>
            <p className="mb-2 text-ink-muted">
              your role can't apply one or more resources this rollback would
              touch. ask your cluster admin for the verbs below before
              retrying.
            </p>
            <ul className="space-y-0.5 text-ink-muted">
              {error?.denied?.map((d, i) => (
                <li key={i} className="text-[11px]">
                  <span className="text-red">×</span>{" "}
                  <span className="text-ink">{d.verb}</span>{" "}
                  {d.group ? `${d.group}/` : ""}
                  {d.resource}
                  {d.reason ? (
                    <span className="text-ink-faint"> — {d.reason}</span>
                  ) : null}
                </li>
              ))}
            </ul>
          </div>
        )}

        {/* Non-denial errors (release not found, helm SDK failure,
            timeout, etc.). Denials get the dedicated treatment above
            so the operator sees the verb list, not just a string. */}
        {error && !blockedByDenial && (
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
          onClick={() => onConfirm({ wait, cleanupOnFail, disableHooks })}
          disabled={!canSubmit}
          className="rounded-sm bg-accent px-3 py-1.5 font-mono text-sm text-bg transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
        >
          {pending ? "rolling back…" : "rollback"}
        </button>
      </div>
    </Modal>
  );
}
