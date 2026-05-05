// DocPreviewList — per-doc preview rows for the apply dialog.
//
// Renders one row per parsed doc with kind / apiVersion / namespace /
// name and either a "ready" badge (parsed clean) or a "bad input"
// badge with the parse error message. Status of dry-run / apply
// results layers on top via the optional DocResult prop.

import type { ParsedDoc } from "../../lib/applyYamlParser";
import type { DocResult } from "../../hooks/useApplyYamlState";
import { cn } from "../../lib/cn";

interface DocPreviewListProps {
  docs: ParsedDoc[];
  results: ReadonlyMap<string, DocResult>;
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

export function DocPreviewList({ docs, results }: DocPreviewListProps) {
  if (docs.length === 0) return null;

  return (
    <ul className="mt-4 divide-y divide-border border-y border-border">
      {docs.map((doc) => {
        const result = results.get(doc.id);
        const state: DocResult["state"] = result?.state ?? "idle";
        return (
          <li
            key={doc.id}
            className={cn(
              "flex items-baseline gap-3 py-2",
              !doc.valid && "bg-red-soft/30",
            )}
          >
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
                <span className="font-mono text-[12.5px] text-red">
                  bad input
                </span>
                <span className="text-[12.5px] text-ink-muted line-clamp-1">
                  {doc.parseError}
                </span>
              </>
            )}

            {result?.errorMessage && (
              <span
                className={cn(
                  "ml-auto truncate font-mono text-[11px]",
                  STATE_COLOR[state],
                )}
                title={result.errorMessage}
              >
                {result.errorMessage}
              </span>
            )}
          </li>
        );
      })}
    </ul>
  );
}
