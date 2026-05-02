// ProblemsStrip — thin strip below the editor showing the first
// validation/parse error inline. Click anywhere on the strip to jump
// to the error in the editor. Hidden when there are no errors.
//
// Surfaces what was previously buried in the action bar's `errors N`
// counter — most operators wouldn't think to hover the counter, so
// the actual schema message stayed invisible. This makes it the first
// thing they see.

import { cn } from "../../../lib/cn";

interface ProblemsStripProps {
  errorCount: number;
  firstError: { message: string; line: number } | null;
  onJump: () => void;
}

export function ProblemsStrip({ errorCount, firstError, onJump }: ProblemsStripProps) {
  if (errorCount === 0 || !firstError) return null;

  const moreCount = errorCount - 1;
  return (
    <button
      type="button"
      onClick={onJump}
      className={cn(
        "flex shrink-0 items-center gap-3 border-t border-red/40 bg-red-soft px-4 py-1.5",
        "text-left transition-colors hover:bg-red-soft/80",
      )}
      title="Click to jump to first error"
    >
      <span aria-hidden className="text-red">
        <svg width="13" height="13" viewBox="0 0 13 13" fill="none">
          <circle cx="6.5" cy="6.5" r="5" stroke="currentColor" strokeWidth="1.4" />
          <path d="M6.5 4v3.5" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
          <circle cx="6.5" cy="9.5" r="0.7" fill="currentColor" />
        </svg>
      </span>
      <span className="font-mono text-[10.5px] text-red tabular shrink-0">
        {errorCount} error{errorCount === 1 ? "" : "s"}
      </span>
      <span className="font-mono text-[10.5px] text-ink-faint shrink-0">·</span>
      <span className="font-mono text-[11.5px] text-ink truncate min-w-0 flex-1">
        line {firstError.line}: {firstError.message}
      </span>
      {moreCount > 0 && (
        <span className="font-mono text-[10.5px] text-ink-faint shrink-0">
          + {moreCount} more
        </span>
      )}
      <span className="font-mono text-[10.5px] text-ink-muted shrink-0">jump ▸</span>
    </button>
  );
}
