import { useEffect, useRef, useState, type ReactNode } from "react";

interface SplitPaneProps {
  left: ReactNode;
  right: ReactNode | null;
  /** Storage key — width persists across sessions if provided. */
  storageKey?: string;
  /** Initial right-pane width as a fraction (0-1). Default 0.45. */
  initial?: number;
  min?: number;
  max?: number;
}

/**
 * Horizontal split with a draggable divider. The right pane collapses
 * (and the divider hides) when `right` is null — left pane fills.
 *
 * Affordances:
 *   - 1px line at rest, hover thickens to ~5px tinted accent zone
 *   - Drag-grip dots appear on hover
 *   - Double-click resets to `initial`
 *   - Width persists to localStorage if `storageKey` is given
 */
export function SplitPane({
  left,
  right,
  storageKey,
  initial = 0.45,
  min = 0.25,
  max = 0.7,
}: SplitPaneProps) {
  const readInitial = (): number => {
    if (storageKey) {
      const stored = localStorage.getItem(storageKey);
      if (stored) {
        const v = parseFloat(stored);
        if (!Number.isNaN(v)) return Math.min(max, Math.max(min, v));
      }
    }
    return initial;
  };

  const [rightFraction, setRightFraction] = useState<number>(readInitial);
  const containerRef = useRef<HTMLDivElement>(null);
  const dragStateRef = useRef<{
    startX: number;
    startFraction: number;
    width: number;
  } | null>(null);

  useEffect(() => {
    if (storageKey) {
      localStorage.setItem(storageKey, rightFraction.toFixed(4));
    }
  }, [rightFraction, storageKey]);

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      const ds = dragStateRef.current;
      if (!ds) return;
      const dx = e.clientX - ds.startX;
      const next = Math.max(
        min,
        Math.min(max, ds.startFraction - dx / ds.width),
      );
      setRightFraction(next);
    };
    const onUp = () => {
      dragStateRef.current = null;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
    return () => {
      document.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseup", onUp);
    };
  }, [min, max]);

  const startDrag = (e: React.MouseEvent) => {
    if (!containerRef.current) return;
    const rect = containerRef.current.getBoundingClientRect();
    dragStateRef.current = {
      startX: e.clientX,
      startFraction: rightFraction,
      width: rect.width,
    };
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
  };

  const resetToDefault = () => setRightFraction(initial);

  return (
    <div ref={containerRef} className="flex min-h-0 flex-1">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">{left}</div>
      {right && (
        <>
          <div
            onMouseDown={startDrag}
            onDoubleClick={resetToDefault}
            className="group relative w-px shrink-0 cursor-col-resize bg-border"
            role="separator"
            aria-orientation="vertical"
            title="Drag to resize · Double-click to reset"
          >
            {/* Wider invisible hit-zone with hover-only tinting */}
            <div className="absolute inset-y-0 -left-1.5 -right-1.5 transition-colors group-hover:bg-accent-soft" />
            {/* Grip dots — only visible on hover */}
            <div className="pointer-events-none absolute left-1/2 top-1/2 flex -translate-x-1/2 -translate-y-1/2 flex-col items-center gap-[3px] opacity-0 transition-opacity group-hover:opacity-100">
              <span className="block size-[3px] rounded-full bg-accent" />
              <span className="block size-[3px] rounded-full bg-accent" />
              <span className="block size-[3px] rounded-full bg-accent" />
            </div>
          </div>
          <div
            className="flex min-w-0 shrink-0 flex-col bg-surface"
            style={{ width: `${rightFraction * 100}%` }}
          >
            {right}
          </div>
        </>
      )}
    </div>
  );
}
