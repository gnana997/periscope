// UninstallReleaseModal — type-the-name confirmation for helm
// release uninstall (#123).
//
// Mirrors web/src/components/edit/DeleteResourceModal's pattern: a
// dedicated dialog with type-the-name friction + cancel / run
// buttons, separate from the single-click ConfirmActionModal
// primitive. Helm uninstall is destructive enough (manifest sweep
// across the namespace plus storage prune) that the friction is
// genuinely warranted.
//
// Two operator-controllable flags surface as checkboxes — both
// default off:
//   - keepHistory: leave the release in storage marked deleted so a
//     future helm rollback or audit can read the prior revisions.
//   - disableHooks: skip pre-/post-delete hooks. Use only when a
//     hook is stuck and the operator needs to force-clean.

import { useState } from "react";
import { Modal } from "../ui/Modal";

interface UninstallReleaseModalProps {
  open: boolean;
  releaseName: string;
  namespace: string;
  resourceCount: number;
  revisionCount: number;
  pending: boolean;
  error?: { status?: number; message: string } | null;
  onClose: () => void;
  onConfirm: (opts: { keepHistory: boolean; disableHooks: boolean }) => void;
}

export function UninstallReleaseModal({
  open,
  releaseName,
  namespace,
  resourceCount,
  revisionCount,
  pending,
  error,
  onClose,
  onConfirm,
}: UninstallReleaseModalProps) {
  const [confirm, setConfirm] = useState("");
  const [keepHistory, setKeepHistory] = useState(false);
  const [disableHooks, setDisableHooks] = useState(false);
  const matches = confirm === releaseName;

  return (
    <Modal
      open={open}
      onClose={pending ? () => {} : onClose}
      labelledBy="uninstall-release-title"
      size="sm"
      dismissOnEsc={!pending}
      dismissOnBackdrop={!pending}
    >
      <div className="border-b border-border px-5 py-3 font-mono text-sm">
        <span className="text-red">uninstall</span>{" "}
        <span id="uninstall-release-title" className="text-ink">
          release {releaseName}
        </span>
      </div>

      <div className="space-y-4 px-5 py-4">
        <p className="text-sm text-ink-muted">
          this will tear down{" "}
          <span className="font-mono text-ink">{resourceCount}</span>{" "}
          {resourceCount === 1 ? "resource" : "resources"} across namespace{" "}
          <span className="font-mono text-ink">{namespace}</span>{" "}
          and prune{" "}
          <span className="font-mono text-ink">{revisionCount}</span>{" "}
          {revisionCount === 1 ? "revision" : "revisions"} from helm storage.
          this cannot be undone.
        </p>
        <p className="text-sm text-ink-muted">
          type{" "}
          <span className="font-mono text-ink">{releaseName}</span> to confirm:
        </p>
        <input
          type="text"
          value={confirm}
          onChange={(e) => setConfirm(e.target.value)}
          disabled={pending}
          autoFocus
          className="w-full rounded-sm border border-border bg-bg px-3 py-2 font-mono text-sm text-ink outline-none focus:border-ink-muted"
          placeholder={releaseName}
          spellCheck={false}
          aria-label={`Type ${releaseName} to confirm uninstall`}
        />

        <div className="space-y-2 border-t border-border pt-3 font-mono text-[11.5px]">
          <p className="text-ink-faint uppercase tracking-[0.16em]">
            advanced
          </p>
          <label className="flex items-start gap-2 text-ink-muted">
            <input
              type="checkbox"
              checked={keepHistory}
              onChange={(e) => setKeepHistory(e.target.checked)}
              disabled={pending}
              className="mt-0.5"
            />
            <span>
              <span className="text-ink">keep history</span> — leave revisions in helm storage marked deleted (rollback / audit later)
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
              <span className="text-ink">disable hooks</span> — skip pre-/post-delete hooks (only for stuck hooks)
            </span>
          </label>
        </div>

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
          onClick={() => onConfirm({ keepHistory, disableHooks })}
          disabled={!matches || pending}
          className="rounded-sm bg-red px-3 py-1.5 font-mono text-sm text-bg transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-40"
        >
          {pending ? "uninstalling…" : "uninstall"}
        </button>
      </div>
    </Modal>
  );
}
