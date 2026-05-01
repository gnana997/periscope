import { cn } from "../../lib/cn";
import { podColor } from "./podColor";

// PodFilterStrip renders the live pod legend below the toolbar. Each pod
// becomes a clickable pill — clicking toggles inclusion in the filter set.
// When nothing is selected, all pods are visible (filter = []).
export function PodFilterStrip({
  pods,
  selected,
  onToggle,
  onClear,
}: {
  pods: string[];
  selected: string[];
  onToggle: (pod: string) => void;
  onClear: () => void;
}) {
  if (pods.length === 0) return null;

  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-border bg-bg/95 px-5 py-2 backdrop-blur-sm">
      <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        pods · {pods.length}
      </span>
      {pods.map((pod) => {
        const isSelected = selected.includes(pod);
        const dimmed = selected.length > 0 && !isSelected;
        return (
          <button
            key={pod}
            type="button"
            onClick={() => onToggle(pod)}
            title={isSelected ? "Click to remove from filter" : "Click to filter to this pod"}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 font-mono text-[10.5px] transition-colors",
              isSelected
                ? "border-accent bg-accent-soft text-ink"
                : dimmed
                  ? "border-border bg-surface-2/40 text-ink-faint opacity-60 hover:opacity-100"
                  : "border-border bg-surface-2/40 text-ink-muted hover:border-border-strong hover:text-ink",
            )}
          >
            <span
              className="size-2 shrink-0 rounded-full"
              style={{ backgroundColor: podColor(pod) }}
            />
            <span className="truncate max-w-[28ch]">{pod}</span>
          </button>
        );
      })}
      {selected.length > 0 && (
        <button
          type="button"
          onClick={onClear}
          className="ml-1 rounded-md border border-border bg-surface-2/40 px-2 py-1 font-mono text-[10.5px] text-ink-muted hover:border-border-strong hover:text-ink"
        >
          clear filter
        </button>
      )}
    </div>
  );
}
