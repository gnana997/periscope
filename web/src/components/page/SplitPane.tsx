import { useEffect, useRef, useState, type ReactNode } from "react";

interface SplitPaneProps {
  left: ReactNode;
  right: ReactNode | null;
  /** Storage key — width persists across sessions if provided. */
  storageKey?: string;
  /** Initial detail-pane width in pixels. Default 640. The pane stays
   *  this wide on every viewport; the left pane (table) takes whatever
   *  space remains. This makes describe content render at a stable,
   *  predictable width so chip layouts, container cards, and gauges
   *  look the same regardless of monitor size. */
  initial?: number;
  /** Minimum detail-pane width in pixels. Drag-resize honors this.
   *  Default 520 — below this the chip grid collapses to one column,
   *  and the Logs tab's wrap mode + dynamic row measurement starts
   *  oscillating against scrollbar appearance (issue #65). */
  min?: number;
  /** Maximum detail-pane width in pixels. Default 1100 — wide enough
   *  for YAML and event tables while still leaving room for the table. */
  max?: number;
  /** Pixels reserved for the left pane (table) on narrow viewports.
   *  When the container is too small to honor both `min` AND
   *  `minLeft`, the pane is squeezed below `min` so the table never
   *  vanishes. Default 320 — tight but usable. */
  minLeft?: number;
}

/**
 * Horizontal split with a draggable divider and a fixed-width right
 * pane. The right pane collapses (and the divider hides) when `right`
 * is null — left pane fills.
 *
 * Pixel-based instead of fraction-based: the detail pane stays at the
 * same absolute width on every monitor, and the table grows/shrinks
 * with the viewport. The previous fraction approach made describe
 * content wider on a 4K monitor than on a laptop, which produced
 * inconsistent chip wrap behavior and a bunch of right-side dead space
 * for pods with short labels. Anchoring the pane to a fixed width
 * removes that variance — all describe content lays out the same.
 *
 * Affordances:
 *   - 1px line at rest, hover thickens to ~5px tinted accent zone
 *   - Drag-grip dots appear on hover
 *   - Double-click resets to `initial`
 *   - Width persists to localStorage if `storageKey` is given
 *   - Honors `minLeft` so the table never disappears on narrow windows
 */
export function SplitPane({
  left,
  right,
  storageKey,
  initial = 640,
  min = 520,
  max = 1100,
  minLeft = 320,
}: SplitPaneProps) {
  const readInitial = (): number => {
    if (storageKey) {
      const stored = localStorage.getItem(storageKey);
      if (stored) {
        const v = parseFloat(stored);
        if (!Number.isNaN(v) && v >= min && v <= max) {
          return v;
        }
      }
    }
    return initial;
  };

  const [rightWidth, setRightWidth] = useState<number>(readInitial);
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerWidth, setContainerWidth] = useState<number>(0);
  const dragStateRef = useRef<{
    startX: number;
    startWidth: number;
  } | null>(null);

  // Track the parent's width so we can clamp the pane on narrow
  // viewports. Without this, opening the page on a 1280px laptop with a
  // stored width of 800px would leave only ~480px for the table — too
  // tight to read.
  useEffect(() => {
    if (!containerRef.current) return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width ?? 0;
      setContainerWidth(w);
    });
    ro.observe(containerRef.current);
    return () => ro.disconnect();
  }, []);

  useEffect(() => {
    if (storageKey) {
      localStorage.setItem(storageKey, String(Math.round(rightWidth)));
    }
  }, [rightWidth, storageKey]);

  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      const ds = dragStateRef.current;
      if (!ds) return;
      // Drag the divider LEFT to grow the detail pane (subtract dx).
      const next = ds.startWidth - (e.clientX - ds.startX);
      const cw = containerRef.current?.getBoundingClientRect().width ?? 0;
      // Upper bound is the smaller of the configured max and what the
      // current viewport leaves after honoring minLeft.
      const upper = Math.min(max, Math.max(min, cw - minLeft));
      setRightWidth(Math.max(min, Math.min(upper, next)));
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
  }, [min, max, minLeft]);

  const startDrag = (e: React.MouseEvent) => {
    if (!containerRef.current) return;
    dragStateRef.current = {
      startX: e.clientX,
      startWidth: rightWidth,
    };
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
  };

  const resetToDefault = () => setRightWidth(initial);

  // Effective width = the stored value clamped by what the current
  // viewport allows. We don't write this back to localStorage — the
  // user's preference is preserved for when they widen the window
  // again.
  const effectiveWidth = (() => {
    if (containerWidth <= 0) return rightWidth;
    const upper = Math.max(min, containerWidth - minLeft);
    // Hard floor at 1px so we never apply width: 0 (which can cause
    // layout glitches in some browsers). Pane never goes below `min`
    // unless the viewport itself can't accommodate that.
    return Math.max(1, Math.min(rightWidth, upper));
  })();

  return (
    <div ref={containerRef} className="flex min-h-0 min-w-0 flex-1">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        {left}
      </div>
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
            style={{ width: `${effectiveWidth}px` }}
          >
            {right}
          </div>
        </>
      )}
    </div>
  );
}
