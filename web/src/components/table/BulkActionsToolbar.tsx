// BulkActionsToolbar — surface for bulk operations on a DataTable.
//
// Renders nothing when nothing is selected, so pages can mount it
// unconditionally above their table without reserving space.
//
// Owns the YAML-download workflow end-to-end:
//   - "Strip server fields" toggle (default ON; produces re-applyable
//     YAML by feeding each fetched doc through stripForEdit)
//   - Confirm dialog for selections containing Secrets — the existing
//     /yaml endpoint returns base64 secret data, so a bulk download is
//     a reveal. Operators see how many will be revealed before commit
//   - In-flight progress + Cancel button (each in-flight fetch is
//     wired to a single AbortController)
//   - Best-effort partial-failure reporting: failed rows surface as a
//     `# FAILED:` comment block at the head of the file plus a toast

import { useCallback, useState } from "react";
import { cn } from "../../lib/cn";
import { showToast } from "../../lib/toastBus";
import {
  bulkFetchYaml,
  buildFilename,
  triggerYamlDownload,
} from "../../lib/multiYaml";
import { recordBulkDownload } from "../../lib/api";
import type { TableSelection } from "../../hooks/useTableSelection";

export interface BulkActionsToolbarProps<T> {
  selection: TableSelection;
  /** Currently-rendered rows. */
  rows: T[];
  /** Same row-key fn the DataTable uses; needed to resolve IDs → rows. */
  rowKey: (row: T) => string;

  /**
   * Optional explicit list of currently-rendered row IDs in
   * render order. Defaults to `rows.map(rowKey)` — correct for
   * flat tables. Grouped layouts (e.g. ReplicaSetsPage's
   * deployment groupings) should pass this so the "(N hidden)"
   * qualifier accounts for collapsed groups, not just filter
   * chips.
   */
  visibleIds?: readonly string[];
  /** Cluster name — used in the filename. */
  cluster: string;
  /** Lowercase plural kind (e.g. "pods", "configmaps"). Used in the filename. */
  kindLabel: string;
  /** Per-row YAML fetcher. */
  fetchYaml: (row: T, signal: AbortSignal) => Promise<string>;
  /**
   * Returns true when downloading the row's YAML reveals sensitive data
   * (base64 secret payloads, encrypted ciphertext, etc.). Any selected
   * row matching `confirmReveal` triggers a confirm dialog before the
   * download proceeds. Pass `() => true` for kinds where every row is
   * sensitive (Secrets); use a per-row predicate for mixed kinds.
   */
  confirmReveal?: (row: T) => boolean;
}

export function BulkActionsToolbar<T>({
  selection,
  rows,
  rowKey,
  cluster,
  kindLabel,
  visibleIds,
  fetchYaml,
  confirmReveal,
}: BulkActionsToolbarProps<T>) {
  const [stripServerFields, setStripServerFields] = useState(true);
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [abortController, setAbortController] = useState<AbortController | null>(null);
  const [confirmSecrets, setConfirmSecrets] = useState<{
    selectedRows: T[];
    secretCount: number;
  } | null>(null);

  // Resolve selected IDs → row references against the *current* rows
  // array. IDs that aren't currently visible (filtered out, or removed
  // by a watch-stream delete) are dropped from this download — we
  // can't fetch what we don't have.
  const resolveSelected = useCallback((): {
    selectedRows: T[];
    missing: number;
  } => {
    const byId = new Map<string, T>();
    for (const row of rows) byId.set(rowKey(row), row);
    const selectedRows: T[] = [];
    let missing = 0;
    for (const id of selection.ids) {
      const row = byId.get(id);
      if (row) selectedRows.push(row);
      else missing += 1;
    }
    return { selectedRows, missing };
  }, [rows, rowKey, selection.ids]);

  const runDownload = useCallback(
    async (selectedRows: T[]) => {
      const ctrl = new AbortController();
      setAbortController(ctrl);
      setProgress({ done: 0, total: selectedRows.length });
      try {
        const result = await bulkFetchYaml({
          items: selectedRows.map((row) => ({ id: rowKey(row), row })),
          fetchYaml,
          stripServerFields,
          signal: ctrl.signal,
          onProgress: (done, total) => setProgress({ done, total }),
        });
        if (ctrl.signal.aborted) {
          showToast("download canceled", "info");
          return;
        }
        // Emit one structured audit row per download — fire-and-forget
        // so a failed audit POST never blocks the operator. Both the
        // success and the all-failed paths emit; both are auditable
        // operator intent. See RFC 0003 §4 (`bulk_download` verb).
        const auditOutcome = result.successCount === 0 ? "failure" : "success";
        void recordBulkDownload(cluster, {
          kind: kindLabel,
          count: selectedRows.length,
          ids: selectedRows.slice(0, 50).map((row) => rowKey(row)),
          outcome: auditOutcome,
          failure_count: result.failures.length,
        }).catch(() => {
          // Audit POST failed — log nothing visible. The download
          // already completed. The audit gap is acceptable: missing
          // audit row beats blocking the user on a transient API
          // failure of the audit endpoint itself.
        });

        if (result.successCount === 0) {
          showToast(
            `download failed — 0 of ${selectedRows.length} fetched`,
            "error",
            5000,
          );
          return;
        }
        const filename = buildFilename(cluster, kindLabel, result.successCount);
        triggerYamlDownload(result.yaml, filename);
        if (result.failures.length > 0) {
          showToast(
            `downloaded ${result.successCount} — ${result.failures.length} failed (see file header)`,
            "warn",
            5000,
          );
        } else {
          showToast(`downloaded ${result.successCount} ${kindLabel}`, "success");
        }
        selection.clear();
      } finally {
        setProgress(null);
        setAbortController(null);
      }
    },
    [cluster, fetchYaml, kindLabel, rowKey, selection, stripServerFields],
  );

  const onDownloadClick = useCallback(() => {
    const { selectedRows, missing } = resolveSelected();
    if (selectedRows.length === 0) {
      showToast(
        missing > 0
          ? "selected rows are no longer visible — clear and re-select"
          : "nothing selected",
        "info",
      );
      return;
    }
    if (missing > 0) {
      showToast(
        `${missing} selected row(s) no longer in view — downloading the remaining ${selectedRows.length}`,
        "info",
        4500,
      );
    }
    const secretCount = confirmReveal
      ? selectedRows.filter(confirmReveal).length
      : 0;
    if (secretCount > 0) {
      setConfirmSecrets({ selectedRows, secretCount });
      return;
    }
    void runDownload(selectedRows);
  }, [resolveSelected, confirmReveal, runDownload]);

  const onConfirmSecrets = useCallback(() => {
    const pending = confirmSecrets;
    setConfirmSecrets(null);
    if (pending) void runDownload(pending.selectedRows);
  }, [confirmSecrets, runDownload]);

  const onCancel = useCallback(() => {
    abortController?.abort();
  }, [abortController]);

  if (selection.count === 0 && progress === null) return null;

  const downloading = progress !== null;
  const effectiveVisibleIds = visibleIds ?? rows.map(rowKey);
  const visibleSelected = countVisible(selection, effectiveVisibleIds);
  const hidden = selection.count - visibleSelected;

  return (
    <>
      <div
        className={cn(
          "flex shrink-0 items-center gap-3 border-b border-border bg-accent-soft/40 px-6 py-2 text-[12px]",
        )}
        role="region"
        aria-label="bulk actions"
      >
        <span className="font-mono text-ink">
          {selection.count} selected
          {hidden > 0 && (
            <span className="ml-1 text-ink-muted">
              ({hidden} hidden by filter)
            </span>
          )}
        </span>

        <label className="flex cursor-pointer items-center gap-1.5 text-ink-muted">
          <input
            type="checkbox"
            checked={stripServerFields}
            onChange={(e) => setStripServerFields(e.target.checked)}
            disabled={downloading}
            className="h-3.5 w-3.5 cursor-pointer accent-accent disabled:cursor-not-allowed"
          />
          <span>strip server fields</span>
        </label>

        <div className="ml-auto flex items-center gap-2">
          {downloading && progress && (
            <span className="font-mono text-[11px] text-ink-muted">
              {progress.done}/{progress.total}
            </span>
          )}
          {downloading ? (
            <button
              type="button"
              onClick={onCancel}
              className="rounded border border-border-strong bg-surface px-2.5 py-1 text-[11.5px] text-ink hover:bg-surface-2"
            >
              cancel
            </button>
          ) : (
            <>
              <button
                type="button"
                onClick={onDownloadClick}
                disabled={selection.count === 0}
                className="rounded border border-accent/60 bg-accent px-3 py-1 text-[11.5px] font-medium text-bg hover:bg-accent/90 disabled:cursor-not-allowed disabled:opacity-50"
              >
                Download YAML
              </button>
              <button
                type="button"
                onClick={selection.clear}
                className="rounded border border-border-strong bg-surface px-2.5 py-1 text-[11.5px] text-ink-muted hover:bg-surface-2 hover:text-ink"
              >
                clear
              </button>
            </>
          )}
        </div>
      </div>

      {confirmSecrets && (
        <SecretsRevealConfirm
          secretCount={confirmSecrets.secretCount}
          totalCount={confirmSecrets.selectedRows.length}
          onConfirm={onConfirmSecrets}
          onCancel={() => setConfirmSecrets(null)}
        />
      )}
    </>
  );
}

function countVisible(
  selection: TableSelection,
  visibleIds: readonly string[],
): number {
  let n = 0;
  for (const id of visibleIds) if (selection.has(id)) n += 1;
  return n;
}

function SecretsRevealConfirm({
  secretCount,
  totalCount,
  onConfirm,
  onCancel,
}: {
  secretCount: number;
  totalCount: number;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  return (
    <div
      className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-labelledby="bulk-secret-confirm-title"
      onClick={onCancel}
    >
      <div
        className="w-full max-w-md rounded-md border border-border-strong bg-bg p-5 text-[13px] shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2
          id="bulk-secret-confirm-title"
          className="mb-2 font-mono text-[14px] font-medium text-ink"
        >
          Reveal {secretCount} secret{secretCount === 1 ? "" : "s"}?
        </h2>
        <p className="mb-4 text-ink-muted">
          The YAML for {secretCount === totalCount ? "these" : `${secretCount} of these ${totalCount}`} resource
          {secretCount === 1 ? " contains" : "s contains"} base64-encoded secret data. Each
          revealed secret will be recorded in the audit log.
        </p>
        <div className="flex justify-end gap-2">
          <button
            type="button"
            onClick={onCancel}
            autoFocus
            className="rounded border border-border-strong bg-surface px-3 py-1.5 text-[12px] text-ink hover:bg-surface-2"
          >
            Cancel
          </button>
          <button
            type="button"
            onClick={onConfirm}
            className="rounded border border-red/60 bg-red px-3 py-1.5 text-[12px] font-medium text-bg hover:bg-red/90"
          >
            Reveal & download
          </button>
        </div>
      </div>
    </div>
  );
}
