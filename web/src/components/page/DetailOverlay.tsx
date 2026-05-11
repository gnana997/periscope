// DetailOverlay — right-edge drawer that floats on top of the page's
// list table instead of squeezing it. Drop-in replacement for
// SplitPane: same `{ left, right, storageKey, initial, min, max }`
// prop contract, plus three optional callbacks that pages opt into.
//
// Why a drawer and not a split: on a 1280px laptop with the default
// 640px split pane open, the table loses half its width and most
// data tables (Pods has 8+ columns) truncate to ellipses. The drawer
// sits at z-40 with no backdrop dim so the table renders at full
// container width underneath, stays readable, and the operator can
// peek at the row list while reading detail (issue #172).
//
// Affordances kept from SplitPane:
//   - Drag-resize on the LEFT edge (inverse of SplitPane's middle
//     divider)
//   - Double-click resets to `initial`
//   - localStorage-persisted width per storageKey
//   - ResizeObserver tracks container width so the drawer never
//     overruns it
//
// New affordances:
//   - Esc dismisses (routed through page-supplied `onDismiss`)
//   - j / ArrowDown cycles to next row (`onNext`)
//   - k / ArrowUp cycles to prev row (`onPrev`)
//   - Narrow viewport (< initial + 180) snaps to full-screen with
//     no drag handle
//
// The overlay does NOT know about dirty-state. Pages already wrap
// their close + row-switch callbacks with `useConfirmDiscard`; they
// pass those pre-wrapped lambdas in as `onDismiss` / `onPrev` /
// `onNext`. Keeping the gate at the page level means DetailOverlay
// stays a pure presentation primitive.

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { isAnyModalOpen } from "../ui/modalRegistry";
import {
  activeElementIsEditable,
  chooseHandler,
  clampWidth,
  isNarrowViewport,
} from "./detailOverlayHelpers";
import { cn } from "../../lib/cn";

interface DetailOverlayProps {
  left: ReactNode;
  right: ReactNode | null;
  /** localStorage key for the user's preferred width. Optional. */
  storageKey?: string;
  /** Initial drawer width in pixels. Default 720. */
  initial?: number;
  /** Minimum drawer width in pixels. Default 520. */
  min?: number;
  /** Maximum drawer width in pixels. Default 95% of container width
   *  at render time; explicit number caps the upper bound. */
  max?: number;
  /** Pre-wrapped (confirmDiscard-gated) dismiss callback. Fires on
   *  Esc. The ✕ button inside the right pane should call the same
   *  function the page wires into DetailPane's `onClose`. */
  onDismiss?: () => void;
  /** Pre-wrapped row-cycling callbacks. Undefined = boundary (no-op).
   *  Page passes undefined for the direction with no neighbor. */
  onPrev?: () => void;
  onNext?: () => void;
  /** Accepted for prop-compat with SplitPane callers; unused. The
   *  overlay floats over the left content rather than reserving
   *  table space, so a `minLeft` constraint is meaningless. */
  minLeft?: number;
}

// ── Component ──────────────────────────────────────────────────────

export function DetailOverlay({
  left,
  right,
  storageKey,
  initial = 720,
  min = 520,
  max = 1600,
  onDismiss,
  onPrev,
  onNext,
}: DetailOverlayProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [containerWidth, setContainerWidth] = useState<number>(0);

  // Read stored width on mount; fall back to initial when missing or
  // out of range.
  const readInitial = useCallback((): number => {
    if (storageKey && typeof window !== "undefined") {
      try {
        const stored = window.localStorage.getItem(storageKey);
        if (stored) {
          const v = parseFloat(stored);
          if (!Number.isNaN(v) && v >= min && v <= max) return v;
        }
      } catch {
        // localStorage may be disabled (private mode) — fall through.
      }
    }
    return initial;
  }, [storageKey, min, max, initial]);

  const [rightWidth, setRightWidth] = useState<number>(readInitial);
  const dragStateRef = useRef<{ startX: number; startWidth: number } | null>(
    null,
  );

  // Track container width so the drawer never overruns it (95% cap).
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    setContainerWidth(el.clientWidth);
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setContainerWidth(entry.contentRect.width);
      }
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Persist on width change (only after the user has dragged — debounce
  // by writing once per change rather than tracking mouseup explicitly).
  useEffect(() => {
    if (!storageKey || typeof window === "undefined") return;
    if (rightWidth === initial) return; // don't overwrite if default
    try {
      window.localStorage.setItem(storageKey, String(Math.round(rightWidth)));
    } catch {
      // ignore
    }
  }, [rightWidth, storageKey, initial]);

  // Drag handlers — mirror SplitPane's math, inverted (mouse moves
  // LEFT to grow the right pane).
  const startDrag = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      dragStateRef.current = { startX: e.clientX, startWidth: rightWidth };
      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
    },
    [rightWidth],
  );

  useEffect(() => {
    function onMove(e: MouseEvent) {
      const state = dragStateRef.current;
      if (!state) return;
      const delta = e.clientX - state.startX;
      const next = state.startWidth - delta;
      setRightWidth(clampWidth(next, min, max, containerWidth));
    }
    function onUp() {
      if (!dragStateRef.current) return;
      dragStateRef.current = null;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    }
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, [min, max, containerWidth]);

  const resetToDefault = useCallback(() => {
    setRightWidth(initial);
  }, [initial]);

  // Keyboard handler — capture phase, yields to inputs + open modals.
  useEffect(() => {
    if (right === null) return;
    function onKey(e: KeyboardEvent) {
      if (activeElementIsEditable()) return;
      if (isAnyModalOpen()) return;
      const handler = chooseHandler(e.key, { onDismiss, onPrev, onNext });
      if (handler) {
        e.preventDefault();
        e.stopPropagation();
        handler();
      }
    }
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [right, onDismiss, onPrev, onNext]);

  const narrow = isNarrowViewport(containerWidth, initial);
  const effectiveWidth = narrow
    ? containerWidth
    : clampWidth(rightWidth, min, max, containerWidth);

  return (
    <div
      ref={containerRef}
      className="relative flex min-h-0 min-w-0 flex-1"
    >
      <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        {left}
      </div>
      {right !== null && (
        <aside
          role="dialog"
          aria-modal="false"
          aria-label="Detail"
          className="absolute inset-y-0 right-0 z-40 flex flex-col border-l border-border-strong bg-bg shadow-[-12px_0_28px_-12px_rgba(0,0,0,0.35)]"
          style={{ width: effectiveWidth }}
        >
          {/* Drag handle — hidden when narrow (drawer is full-width). */}
          <div
            onMouseDown={narrow ? undefined : startDrag}
            onDoubleClick={narrow ? undefined : resetToDefault}
            role="separator"
            aria-orientation="vertical"
            title="Drag to resize · Double-click to reset"
            className={cn(
              "group absolute inset-y-0 -left-1.5 w-3",
              narrow ? "pointer-events-none" : "cursor-col-resize",
            )}
          >
            <div
              className={cn(
                "absolute inset-y-0 left-1.5 w-px bg-border-strong transition-colors",
                !narrow && "group-hover:bg-accent-soft group-hover:w-[5px]",
              )}
            />
            {!narrow && (
              <div className="absolute left-0 top-1/2 flex -translate-y-1/2 flex-col gap-1 opacity-0 transition-opacity group-hover:opacity-100">
                <span className="h-1 w-1 rounded-full bg-accent" />
                <span className="h-1 w-1 rounded-full bg-accent" />
                <span className="h-1 w-1 rounded-full bg-accent" />
              </div>
            )}
          </div>
          {right}
        </aside>
      )}
    </div>
  );
}

