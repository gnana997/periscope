// useTableSelection — per-tab row selection state for bulk operations.
//
// Selection is keyed by an opaque string ID (whatever the page already
// uses for `rowKey`). Because we store IDs (not row references), state
// survives transient row turnover from the watch stream and from
// client-side filtering: a row that drops out of the visible list and
// returns later stays selected.
//
// Cap: refuses inserts past `cap` and surfaces a toast. The cap exists
// so "select all on a 5,000-pod list" can't construct a multi-MB
// browser download that freezes the tab.

import { useCallback, useMemo, useRef, useState } from "react";
import { showToast } from "../lib/toastBus";

export interface UseTableSelectionArgs {
  /** Hard cap on selected rows. Defaults to 100. */
  cap?: number;
  /** Human-readable kind label for the cap toast (e.g. "pods"). */
  kindLabel?: string;
}

export interface TableSelection {
  /** Set of currently-selected row IDs. */
  ids: ReadonlySet<string>;
  /** True when `id` is currently selected. */
  has: (id: string) => boolean;
  /** Number of selected rows. */
  count: number;
  /** Toggle a single row. Refuses if the cap would be exceeded. */
  toggle: (id: string) => void;
  /**
   * Range-select between the last-toggled row and `id`, restricted to
   * `visibleIds` (the currently rendered rows in order). Used by
   * shift-click. Refuses inserts past the cap; partial range is fine.
   */
  toggleRange: (id: string, visibleIds: readonly string[]) => void;
  /**
   * Select every visible ID up to the cap. If `visibleIds.length` is
   * over `cap` we select the first `cap` and toast.
   */
  selectAll: (visibleIds: readonly string[]) => void;
  /** Drop everything. */
  clear: () => void;
  /** Cap value in effect. */
  cap: number;
}

const DEFAULT_CAP = 100;

export function useTableSelection({
  cap = DEFAULT_CAP,
  kindLabel,
}: UseTableSelectionArgs = {}): TableSelection {
  const [ids, setIds] = useState<ReadonlySet<string>>(() => new Set());
  // Anchor for shift-click range selection.
  const anchorRef = useRef<string | null>(null);

  const capWarning = useCallback(() => {
    showToast(
      `max ${cap} ${kindLabel ?? "rows"} selected for download — refine your filter or download in batches`,
      "warn",
      4500,
    );
  }, [cap, kindLabel]);

  const toggle = useCallback(
    (id: string) => {
      setIds((prev) => {
        if (prev.has(id)) {
          const next = new Set(prev);
          next.delete(id);
          anchorRef.current = id;
          return next;
        }
        if (prev.size >= cap) {
          capWarning();
          return prev;
        }
        const next = new Set(prev);
        next.add(id);
        anchorRef.current = id;
        return next;
      });
    },
    [cap, capWarning],
  );

  const toggleRange = useCallback(
    (id: string, visibleIds: readonly string[]) => {
      const anchor = anchorRef.current;
      if (anchor === null || anchor === id) {
        toggle(id);
        return;
      }
      const a = visibleIds.indexOf(anchor);
      const b = visibleIds.indexOf(id);
      if (a === -1 || b === -1) {
        toggle(id);
        return;
      }
      const [lo, hi] = a < b ? [a, b] : [b, a];
      const range = visibleIds.slice(lo, hi + 1);
      // Range-select adds (never deselects) — Excel/GMail convention.
      // Stops at cap and toasts once if any were dropped.
      setIds((prev) => {
        const next = new Set(prev);
        let dropped = 0;
        for (const rid of range) {
          if (next.has(rid)) continue;
          if (next.size >= cap) {
            dropped += 1;
            continue;
          }
          next.add(rid);
        }
        if (dropped > 0) capWarning();
        anchorRef.current = id;
        return next;
      });
    },
    [cap, capWarning, toggle],
  );

  const selectAll = useCallback(
    (visibleIds: readonly string[]) => {
      setIds((prev) => {
        const next = new Set(prev);
        let dropped = 0;
        for (const rid of visibleIds) {
          if (next.has(rid)) continue;
          if (next.size >= cap) {
            dropped += 1;
            continue;
          }
          next.add(rid);
        }
        if (dropped > 0) capWarning();
        return next;
      });
    },
    [cap, capWarning],
  );

  const clear = useCallback(() => {
    setIds(new Set());
    anchorRef.current = null;
  }, []);

  return useMemo<TableSelection>(
    () => ({
      ids,
      has: (id) => ids.has(id),
      count: ids.size,
      toggle,
      toggleRange,
      selectAll,
      clear,
      cap,
    }),
    [ids, toggle, toggleRange, selectAll, clear, cap],
  );
}
