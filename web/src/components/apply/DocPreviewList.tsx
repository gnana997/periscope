// DocPreviewList — per-doc preview rows for the apply dialog.
//
// Renders one row per parsed doc with kind / apiVersion / namespace /
// name and either a "ready" badge (parsed clean) or a "bad input"
// badge with the parse error message. Status of dry-run / apply
// results layers on top via the optional DocResult prop.
//
// Conflict rows expose a per-row Force button that retries with
// force=true (takeover semantics). Successful dry-runs expose an
// expandable diff so the operator can verify what would change before
// committing.

import { useState } from "react";
import type { ParsedDoc } from "../../lib/applyYamlParser";
import type { DocResult } from "../../hooks/useApplyYamlState";
import { cn } from "../../lib/cn";

interface DocPreviewListProps {
  docs: ParsedDoc[];
  results: ReadonlyMap<string, DocResult>;
  /**
   * Called when the operator clicks the per-row "Force" button on a
   * conflict result. The dialog wires this to forceApplyOne(doc, cluster).
   */
  onForce?: (doc: ParsedDoc) => void;
  /** Disabled state for force buttons (e.g. while a batch is running). */
  forceDisabled?: boolean;
}

const STATE_GLYPH: Record<DocResult["state"], string> = {
  idle: "○",
  pending: "◐",
  success: "●",
  failure: "✕",
  conflict: "⚠",
};

const STATE_COLOR: Record<DocResult["state"], string> = {
  idle: "text-ink-faint",
  pending: "text-ink-muted",
  success: "text-green",
  failure: "text-red",
  conflict: "text-yellow",
};

export function DocPreviewList({
  docs,
  results,
  onForce,
  forceDisabled,
}: DocPreviewListProps) {
  if (docs.length === 0) return null;

  return (
    <ul className="mt-4 divide-y divide-border border-y border-border">
      {docs.map((doc) => (
        <DocPreviewRow
          key={doc.id}
          doc={doc}
          result={results.get(doc.id)}
          onForce={onForce}
          forceDisabled={forceDisabled}
        />
      ))}
    </ul>
  );
}

interface DocPreviewRowProps {
  doc: ParsedDoc;
  result: DocResult | undefined;
  onForce?: (doc: ParsedDoc) => void;
  forceDisabled?: boolean;
}

function DocPreviewRow({ doc, result, onForce, forceDisabled }: DocPreviewRowProps) {
  const [diffOpen, setDiffOpen] = useState(false);
  const state: DocResult["state"] = result?.state ?? "idle";
  const hasDiff = state === "success" && Boolean(result?.diff);

  return (
    <li
      className={cn(
        "flex flex-col py-2",
        !doc.valid && "bg-red-soft/30",
      )}
    >
      <div className="flex items-baseline gap-3">
        <span
          aria-hidden
          className={cn("font-mono text-[14px] w-4 shrink-0", STATE_COLOR[state])}
        >
          {STATE_GLYPH[state]}
        </span>

        {doc.valid ? (
          <>
            <span className="font-mono text-[12.5px] text-ink tabular">
              {doc.kind}
            </span>
            <span className="font-mono text-[11px] text-ink-faint tabular">
              {doc.apiVersion}
            </span>
            <span className="text-[12.5px] text-ink-muted">
              {doc.namespace ? `${doc.namespace}/` : ""}
              <span className="text-ink">{doc.name}</span>
              {!doc.namespace && (
                <span className="ml-1 font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
                  cluster-scoped
                </span>
              )}
            </span>
          </>
        ) : (
          <>
            <span className="font-mono text-[12.5px] text-red">bad input</span>
            <span className="text-[12.5px] text-ink-muted line-clamp-1">
              {doc.parseError}
            </span>
          </>
        )}

        <div className="ml-auto flex shrink-0 items-baseline gap-2">
          {result?.errorMessage && (
            <span
              className={cn(
                "max-w-[26ch] truncate font-mono text-[11px]",
                STATE_COLOR[state],
              )}
              title={result.errorMessage}
            >
              {result.errorMessage}
            </span>
          )}
          {state === "conflict" && onForce && (
            <button
              type="button"
              onClick={() => onForce(doc)}
              disabled={forceDisabled}
              title="Retry this doc with force=true (takeover ownership of conflicting fields)"
              className="rounded-sm border border-yellow-soft bg-yellow-soft px-2 py-0.5 font-mono text-[10.5px] uppercase tracking-[0.08em] text-yellow transition-[filter] hover:brightness-105 disabled:cursor-not-allowed disabled:opacity-50"
            >
              force
            </button>
          )}
          {hasDiff && (
            <button
              type="button"
              onClick={() => setDiffOpen((o) => !o)}
              className="font-mono text-[10.5px] lowercase tracking-[0.04em] text-ink-muted underline-offset-2 hover:text-accent hover:underline"
            >
              {diffOpen ? "hide diff" : "show diff"}
            </button>
          )}
        </div>
      </div>

      {hasDiff && diffOpen && result?.diff && (
        <pre className="ml-7 mt-2 max-h-72 overflow-auto rounded-sm border border-border bg-bg p-3 font-mono text-[11px] leading-[1.4] text-ink-muted">
          {result.diff}
        </pre>
      )}
    </li>
  );
}
