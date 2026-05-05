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
          <DocPreviewList docs={state.docs} results={state.results} />
        </div>

        {/* ── Footer ────────────────────────────────────────────── */}
        <footer className="flex items-center justify-end gap-2 border-t border-border px-6 py-4">
          <button
            type="button"
            onClick={handleClose}
            className="rounded-sm border border-border-strong px-4 py-1.5 font-mono text-sm lowercase text-ink transition-colors hover:bg-surface-2"
          >
            cancel
          </button>
          <button
            type="button"
            disabled
            className="rounded-sm border border-border-strong px-4 py-1.5 font-mono text-sm lowercase text-ink-faint disabled:cursor-not-allowed"
          >
            dry-run
          </button>
          <button
            type="button"
            disabled
            className="rounded-sm border border-accent bg-accent px-4 py-1.5 font-mono text-sm lowercase text-accent-ink disabled:cursor-not-allowed disabled:opacity-50"
          >
            apply
          </button>
        </footer>
      </div>
    </Modal>
  );
}
