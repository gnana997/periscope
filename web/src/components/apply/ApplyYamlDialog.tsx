// ApplyYamlDialog — operator-facing modal for pasting / uploading
// arbitrary YAML and running it through the existing apply pipeline.
//
// Composition entry point for the Apply YAML epic (#51). This component
// is the "what" — paste, parse, dry-run, apply, render results. The
// "where" (button placement, Cmd+K palette) lives in #54 and mounts
// this dialog with `open` / `onClose`.
//
// Scaffold commit — wires the modal shell + footer skeleton. Subsequent
// commits add the YAML input (Monaco + drag-drop), parser, dry-run
// orchestration, and results panel.

import { useId } from "react";
import { Modal } from "../ui/Modal";
import { ApplyYamlInput } from "./ApplyYamlInput";
import { DocPreviewList } from "./DocPreviewList";
import { useApplyYamlState } from "../../hooks/useApplyYamlState";

export interface ApplyYamlDialogProps {
  open: boolean;
  onClose: () => void;
  /** Cluster the apply will target. Plumbed through to api.applyResource. */
  cluster: string;
}

export function ApplyYamlDialog({ open, onClose, cluster }: ApplyYamlDialogProps) {
  const titleId = useId();
  const state = useApplyYamlState();

  const validCount = state.docs.filter((d) => d.valid).length;
  const invalidCount = state.docs.length - validCount;

  // The dialog itself controls the close path — child components emit
  // events (clear, cancel, apply-success) that route here so reset
  // happens in one place.
  const handleClose = () => {
    state.reset();
    onClose();
  };

  return (
    <Modal open={open} onClose={handleClose} labelledBy={titleId} size="lg">
      <div className="flex max-h-[85vh] flex-col">
        {/* ── Header ────────────────────────────────────────────── */}
        <header className="flex items-baseline justify-between gap-4 border-b border-border px-6 py-4">
          <div>
            <h2 id={titleId} className="font-mono text-[15px] tracking-tight text-ink">
              Apply YAML
            </h2>
            <p className="mt-1 font-mono text-[11px] text-ink-faint tabular">
              {cluster}
            </p>
          </div>
          <p className="font-mono text-[11px] text-ink-muted">
            Paste a manifest or drop a `.yaml` file. Multi-doc supported.
          </p>
        </header>

        {/* ── Body ──────────────────────────────────────────────── */}
        <div className="flex-1 overflow-auto px-6 py-5">
          <ApplyYamlInput value={state.yamlText} onChange={state.setYamlText} />
          <DocPreviewList
            docs={state.docs}
            results={state.results}
            onForce={(doc) => { void state.forceApplyOne(doc, cluster); }}
            forceDisabled={state.busy !== "idle"}
          />
        </div>

        {/* ── Footer ────────────────────────────────────────────── */}
        <footer className="flex items-center justify-between gap-2 border-t border-border px-6 py-4">
          <p className="font-mono text-[11px] text-ink-muted">
            {state.busy === "dry-run" && "running dry-run…"}
            {state.busy === "apply" && "applying…"}
            {state.busy === "idle" && validCount > 0 && (
              <>{validCount} valid {validCount === 1 ? "doc" : "docs"} ready{invalidCount > 0 && `, ${invalidCount} skipped`}</>
            )}
          </p>
          <div className="flex items-center gap-2">
            {state.busy !== "idle" ? (
              <button
                type="button"
                onClick={state.cancel}
                className="rounded-sm border border-border-strong px-4 py-1.5 font-mono text-sm lowercase text-ink transition-colors hover:bg-surface-2"
              >
                cancel run
              </button>
            ) : (
              <>
                <button
                  type="button"
                  onClick={handleClose}
                  className="rounded-sm border border-border-strong px-4 py-1.5 font-mono text-sm lowercase text-ink transition-colors hover:bg-surface-2"
                >
                  cancel
                </button>
                <button
                  type="button"
                  onClick={() => { void state.runDryRun(cluster); }}
                  disabled={validCount === 0}
                  className="rounded-sm border border-border-strong px-4 py-1.5 font-mono text-sm lowercase text-ink transition-colors hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  dry-run
                </button>
                <button
                  type="button"
                  onClick={() => { void state.runApply(cluster); }}
                  disabled={validCount === 0}
                  className="rounded-sm border border-accent bg-accent px-4 py-1.5 font-mono text-sm lowercase text-accent-ink transition-[filter] hover:brightness-105 disabled:cursor-not-allowed disabled:opacity-50"
                >
                  apply {validCount > 0 ? validCount : ""}
                </button>
              </>
            )}
          </div>
        </footer>
      </div>
    </Modal>
  );
}
