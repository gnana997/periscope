import { useEffect, useMemo, useRef, useState } from "react";
import { cn } from "../../lib/cn";
import { podColor } from "./podColor";
import type { PodAttribution } from "../../hooks/useLogStream";

const VISIBLE_LIMIT = 30;

export type WorkloadKind = "deployment" | "statefulset" | "daemonset" | "job";

// Sort + display rules differ per kind:
//   - Deployment / StatefulSet / Job: pod-identity-first (sort by pod name,
//     show pod name on the pill, surface node in the tooltip).
//   - DaemonSet: node-identity-first (sort by node, show node on the pill,
//     surface pod name in the tooltip). Pod color hash stays keyed on pod
//     name so each replica still has a stable distinct color.
function pillLabels(
  p: PodAttribution,
  kind: WorkloadKind,
): { primary: string; secondary: string } {
  if (kind === "daemonset") {
    return {
      primary: p.node || "(unscheduled)",
      secondary: `pod: ${p.name}`,
    };
  }
  return {
    primary: p.name,
    secondary: p.node ? `node: ${p.node}` : "node: (unscheduled)",
  };
}

// PodFilterStrip renders the live pod legend below the toolbar. Each pod
// becomes a clickable pill — clicking toggles inclusion in the filter set.
// When nothing is selected, all pods are visible (filter = []).
//
// Pod sets larger than VISIBLE_LIMIT collapse into a "+ N more ▾"
// dropdown so the strip doesn't take over the viewport on big DaemonSets.
export function PodFilterStrip({
  kind,
  pods,
  selected,
  onToggle,
  onClear,
}: {
  kind: WorkloadKind;
  pods: PodAttribution[];
  selected: string[];
  onToggle: (podName: string) => void;
  onClear: () => void;
}) {
  const sortedPods = useMemo(() => {
    const arr = pods.slice();
    if (kind === "daemonset") {
      arr.sort((a, b) => a.node.localeCompare(b.node) || a.name.localeCompare(b.name));
    } else {
      arr.sort((a, b) => a.name.localeCompare(b.name));
    }
    return arr;
  }, [pods, kind]);

  const visible = sortedPods.slice(0, VISIBLE_LIMIT);
  const overflow = sortedPods.slice(VISIBLE_LIMIT);

  const [dropdownOpen, setDropdownOpen] = useState(false);
  const dropdownAnchorRef = useRef<HTMLDivElement | null>(null);

  // Click-outside dismissal for the overflow dropdown.
  useEffect(() => {
    if (!dropdownOpen) return;
    const onMouseDown = (e: MouseEvent) => {
      if (
        dropdownAnchorRef.current &&
        !dropdownAnchorRef.current.contains(e.target as Node)
      ) {
        setDropdownOpen(false);
      }
    };
    document.addEventListener("mousedown", onMouseDown);
    return () => document.removeEventListener("mousedown", onMouseDown);
  }, [dropdownOpen]);

  if (pods.length === 0) return null;

  return (
    <div className="flex flex-wrap items-center gap-1.5 border-b border-border bg-bg/95 px-5 py-2 backdrop-blur-sm">
      <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {kind === "daemonset" ? "nodes" : "pods"} · {pods.length}
      </span>

      {visible.map((pod) => (
        <Pill
          key={pod.name}
          pod={pod}
          kind={kind}
          selected={selected.includes(pod.name)}
          dimmed={selected.length > 0 && !selected.includes(pod.name)}
          onClick={() => onToggle(pod.name)}
        />
      ))}

      {overflow.length > 0 && (
        <div className="relative" ref={dropdownAnchorRef}>
          <button
            type="button"
            onClick={() => setDropdownOpen((v) => !v)}
            className={cn(
              "inline-flex items-center gap-1 rounded-md border border-border bg-surface-2/40 px-2 py-1 font-mono text-[10.5px] transition-colors",
              "text-ink-muted hover:border-border-strong hover:text-ink",
              dropdownOpen && "border-border-strong text-ink",
            )}
          >
            + {overflow.length} more
            <span className="text-[8px]">{dropdownOpen ? "▾" : "▸"}</span>
          </button>
          {dropdownOpen && (
            <div className="absolute left-0 top-full z-20 mt-1 max-h-80 w-80 overflow-y-auto rounded-md border border-border bg-surface shadow-lg">
              {overflow.map((pod) => {
                const { primary, secondary } = pillLabels(pod, kind);
                const isSelected = selected.includes(pod.name);
                return (
                  <button
                    key={pod.name}
                    type="button"
                    onClick={() => onToggle(pod.name)}
                    className={cn(
                      "flex w-full items-center gap-2 border-b border-border/40 px-2.5 py-1.5 text-left font-mono text-[11px] transition-colors last:border-b-0",
                      isSelected
                        ? "bg-accent-soft/60 text-ink"
                        : "text-ink-muted hover:bg-surface-2/60 hover:text-ink",
                    )}
                  >
                    <span
                      className="size-2 shrink-0 rounded-full"
                      style={{ backgroundColor: podColor(pod.name) }}
                    />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate">{primary}</span>
                      <span className="block truncate text-[10px] text-ink-faint">
                        {secondary}
                      </span>
                    </span>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      )}

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

function Pill({
  pod,
  kind,
  selected,
  dimmed,
  onClick,
}: {
  pod: PodAttribution;
  kind: WorkloadKind;
  selected: boolean;
  dimmed: boolean;
  onClick: () => void;
}) {
  const { primary, secondary } = pillLabels(pod, kind);
  return (
    <button
      type="button"
      onClick={onClick}
      title={secondary}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2 py-1 font-mono text-[10.5px] transition-colors",
        selected
          ? "border-accent bg-accent-soft text-ink"
          : dimmed
            ? "border-border bg-surface-2/40 text-ink-faint opacity-60 hover:opacity-100"
            : "border-border bg-surface-2/40 text-ink-muted hover:border-border-strong hover:text-ink",
      )}
    >
      <span
        className="size-2 shrink-0 rounded-full"
        style={{ backgroundColor: podColor(pod.name) }}
      />
      <span className="truncate max-w-[28ch]">{primary}</span>
    </button>
  );
}
