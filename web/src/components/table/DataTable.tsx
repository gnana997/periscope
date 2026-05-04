import { memo, useLayoutEffect, useMemo, useRef, type ReactNode, type MouseEvent } from "react";
import { cn } from "../../lib/cn";
import type { TableSelection } from "../../hooks/useTableSelection";

export type RowTint = "red" | "yellow" | null;

export interface Column<T> {
  key: string;
  header: string;
  accessor: (row: T) => ReactNode;
  align?: "left" | "right";
  /** Flex weight for the column. Defaults to 1. */
  weight?: number;
  /** Additional cell className (typography mostly). */
  cellClassName?: string;
}

export interface DataTableProps<T> {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  rowTint?: (row: T) => RowTint;
  onRowClick?: (row: T) => void;
  selectedKey?: string | null;
  /**
   * Opt-in bulk-selection state. When provided, a checkbox column is
   * prepended to the table; the header checkbox toggles select-all-on-
   * page. Click vs. shift-click on a row checkbox toggles a single row
   * vs. range-extends from the last anchor.
   */
  selection?: TableSelection;
}

const ROW_HEIGHT = 32;
const CHECKBOX_COL_WIDTH = 36;

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  rowTint,
  onRowClick,
  selectedKey,
  selection,
}: DataTableProps<T>) {
  // Sync onRowClick into a ref so the row-level handler can read the
  // latest closure without participating in memo equality.
  const onRowClickRef = useRef(onRowClick);
  useLayoutEffect(() => {
    onRowClickRef.current = onRowClick;
  });
  // Stable wrapper. Identity never changes for the lifetime of the
  // DataTable instance, so a row's memo never breaks because the
  // page passes a fresh `onRowClick={(p) => ...}` on every render.
  const handleRowClick = useMemo(
    () => (row: T) => onRowClickRef.current?.(row),
    [],
  );

  // Visible IDs in render order — feeds shift-click range and the
  // header select-all. Stable identity until rows or rowKey changes.
  const visibleIds = useMemo(() => rows.map(rowKey), [rows, rowKey]);

  // Selected count restricted to currently-visible rows. The selection
  // model itself can hold IDs that the active filter has hidden; the
  // header checkbox shows the on-page state, not the global state.
  const visibleSelectedCount = useMemo(() => {
    if (!selection) return 0;
    let n = 0;
    for (const id of visibleIds) if (selection.has(id)) n += 1;
    return n;
  }, [selection, visibleIds]);

  const headerCheckboxState: "off" | "indeterminate" | "all" = !selection
    ? "off"
    : visibleSelectedCount === 0
      ? "off"
      : visibleSelectedCount === visibleIds.length
        ? "all"
        : "indeterminate";

  const onHeaderCheckboxClick = () => {
    if (!selection) return;
    if (headerCheckboxState === "all") {
      // Deselect only the visible rows; keep hidden-by-filter rows.
      for (const id of visibleIds) {
        if (selection.has(id)) selection.toggle(id);
      }
    } else {
      selection.selectAll(visibleIds);
    }
  };

  const onRowCheckboxClick = (id: string, e: MouseEvent) => {
    if (!selection) return;
    if (e.shiftKey) {
      selection.toggleRange(id, visibleIds);
    } else {
      selection.toggle(id);
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div
        className="sticky top-0 z-10 flex h-7 shrink-0 items-center border-b border-border bg-bg/80 px-6 backdrop-blur-sm"
        role="row"
      >
        {selection && (
          <div
            className="flex shrink-0 items-center justify-center pr-2"
            style={{ width: CHECKBOX_COL_WIDTH }}
            role="columnheader"
          >
            <HeaderCheckbox
              state={headerCheckboxState}
              onClick={onHeaderCheckboxClick}
              total={visibleIds.length}
            />
          </div>
        )}
        {columns.map((col) => (
          <div
            key={col.key}
            className={cn(
              "pr-4 text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint",
              col.align === "right" && "text-right",
            )}
            style={{ flex: col.weight ?? 1 }}
            role="columnheader"
          >
            {col.header}
          </div>
        ))}
      </div>
      {/* scrollbar-gutter: stable so the right edge of every row tint
          (selected accent, failing red-soft, pending yellow-soft) lands
          at the same x-coordinate regardless of whether the rowgroup
          itself needs to scroll. Without it, a tall list paints a 10px
          scrollbar that shrinks each row's effective width, while a
          short list doesn't — and the visible right edge of the tinted
          rows shifts between table states. */}
      <div className="flex-1 overflow-auto [scrollbar-gutter:stable]" role="rowgroup">
        {rows.map((row) => {
          const key = rowKey(row);
          const tint = rowTint?.(row) ?? null;
          const isSelected = selectedKey === key;
          const isChecked = selection ? selection.has(key) : false;
          return (
            <DataTableRow
              key={key}
              row={row}
              rowId={key}
              columns={columns}
              isSelected={isSelected}
              tint={tint}
              onClick={onRowClick ? handleRowClick : undefined}
              showCheckbox={!!selection}
              isChecked={isChecked}
              onCheckboxClick={selection ? onRowCheckboxClick : undefined}
            />
          );
        })}
      </div>
    </div>
  );
}

interface DataTableRowProps<T> {
  row: T;
  rowId: string;
  columns: Column<T>[];
  isSelected: boolean;
  tint: RowTint;
  /**
   * Stable wrapper that delegates to the parent's onRowClick via ref.
   * Identity is fixed for the lifetime of the parent (modulo
   * onRowClick going from defined to undefined or vice-versa), so it
   * never participates in row-equality misses.
   */
  onClick?: (row: T) => void;
  showCheckbox: boolean;
  isChecked: boolean;
  onCheckboxClick?: (id: string, e: MouseEvent) => void;
}

/**
 * DataTableRowImpl renders one row. Extracted from DataTable to make
 * memoization possible — the watch-stream cache mutators
 * (addRowToList / patchRowInList) preserve reference identity for
 * unchanged rows, so reference equality on `row` is exactly what we
 * want for the equality check: a single delta re-renders one row, not
 * the whole list.
 *
 * The checkbox cell is rendered as a sibling <span> rather than a
 * nested <button>: nested buttons are invalid HTML and break
 * keyboard / a11y semantics. We catch the click on the wrapper and
 * stop propagation so the row-level navigation doesn't fire.
 */
function DataTableRowImpl<T>({
  row,
  rowId,
  columns,
  isSelected,
  tint,
  onClick,
  showCheckbox,
  isChecked,
  onCheckboxClick,
}: DataTableRowProps<T>) {
  return (
    <div
      className={cn(
        "group flex w-full items-center text-left text-[12.5px] transition-colors",
        "border-l-2",
        tint === "red" && "bg-red-soft/60",
        tint === "yellow" && "bg-yellow-soft/60",
        isSelected
          ? "border-l-accent bg-accent-soft"
          : "border-l-transparent",
        !isSelected && !tint && "hover:bg-surface-2/50",
        !isSelected && tint === "red" && "hover:bg-red-soft",
        !isSelected && tint === "yellow" && "hover:bg-yellow-soft",
      )}
      style={{ height: ROW_HEIGHT, minHeight: ROW_HEIGHT }}
      role="row"
    >
      {showCheckbox && (
        <span
          className="flex shrink-0 items-center justify-center pl-6 pr-2"
          style={{ width: CHECKBOX_COL_WIDTH + 24 /* 24 = pl-6 */ }}
          onClick={(e) => {
            e.stopPropagation();
            onCheckboxClick?.(rowId, e);
          }}
        >
          <RowCheckbox checked={isChecked} rowId={rowId} />
        </span>
      )}
      <button
        onClick={onClick ? () => onClick(row) : undefined}
        className={cn(
          "flex h-full min-w-0 flex-1 items-center text-left",
          !showCheckbox && "pl-6",
          "pr-6",
          onClick && "cursor-pointer",
        )}
        type="button"
      >
        {columns.map((col) => (
          <div
            key={col.key}
            className={cn(
              "min-w-0 truncate pr-4",
              col.align === "right" && "text-right",
              col.cellClassName,
            )}
            style={{ flex: col.weight ?? 1 }}
            role="cell"
          >
            {col.accessor(row)}
          </div>
        ))}
      </button>
    </div>
  );
}

// Memo equality: only the observable props that affect render output.
// `onClick` and `onCheckboxClick` are intentionally excluded —
// DataTable wraps them in stable ref-backed handlers whose identity
// never changes for the row's lifetime, so the row never re-renders
// due to a callback identity churn at the page level.
const DataTableRow = memo(
  DataTableRowImpl,
  (prev, next) =>
    prev.row === next.row &&
    prev.rowId === next.rowId &&
    prev.isSelected === next.isSelected &&
    prev.tint === next.tint &&
    prev.columns === next.columns &&
    prev.showCheckbox === next.showCheckbox &&
    prev.isChecked === next.isChecked,
) as typeof DataTableRowImpl;

function HeaderCheckbox({
  state,
  onClick,
  total,
}: {
  state: "off" | "indeterminate" | "all";
  onClick: () => void;
  total: number;
}) {
  const ref = useRef<HTMLInputElement | null>(null);
  useLayoutEffect(() => {
    if (ref.current) ref.current.indeterminate = state === "indeterminate";
  }, [state]);
  return (
    <input
      ref={ref}
      type="checkbox"
      checked={state === "all"}
      onChange={onClick}
      disabled={total === 0}
      aria-label={
        state === "all"
          ? "deselect all rows on this page"
          : "select all rows on this page"
      }
      className="h-3.5 w-3.5 cursor-pointer accent-accent disabled:cursor-not-allowed disabled:opacity-40"
    />
  );
}

function RowCheckbox({ checked, rowId }: { checked: boolean; rowId: string }) {
  return (
    <input
      type="checkbox"
      checked={checked}
      // The wrapper <span> handles the click (so it can read shiftKey
      // and stop propagation). onChange must still be present or React
      // will warn about a controlled input without a handler.
      onChange={() => {}}
      aria-label={`select row ${rowId}`}
      className="h-3.5 w-3.5 cursor-pointer accent-accent"
    />
  );
}
