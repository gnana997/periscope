import { memo, useLayoutEffect, useMemo, useRef, type ReactNode } from "react";
import { cn } from "../../lib/cn";

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
}

const ROW_HEIGHT = 32;

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  rowTint,
  onRowClick,
  selectedKey,
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

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div
        className="sticky top-0 z-10 flex h-7 shrink-0 items-center border-b border-border bg-bg/80 px-6 backdrop-blur-sm"
        role="row"
      >
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
          return (
            <DataTableRow
              key={key}
              row={row}
              columns={columns}
              isSelected={isSelected}
              tint={tint}
              onClick={onRowClick ? handleRowClick : undefined}
            />
          );
        })}
      </div>
    </div>
  );
}

interface DataTableRowProps<T> {
  row: T;
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
}

/**
 * DataTableRowImpl renders one row. Extracted from DataTable to make
 * memoization possible — the watch-stream cache mutators
 * (addRowToList / patchRowInList) preserve reference identity for
 * unchanged rows, so reference equality on `row` is exactly what we
 * want for the equality check: a single delta re-renders one row, not
 * the whole list.
 */
function DataTableRowImpl<T>({
  row,
  columns,
  isSelected,
  tint,
  onClick,
}: DataTableRowProps<T>) {
  return (
    <button
      onClick={onClick ? () => onClick(row) : undefined}
      className={cn(
        "group flex w-full items-center px-6 text-left text-[12.5px] transition-colors",
        "border-l-2",
        tint === "red" && "bg-red-soft/60",
        tint === "yellow" && "bg-yellow-soft/60",
        isSelected
          ? "border-l-accent bg-accent-soft"
          : "border-l-transparent",
        !isSelected && !tint && "hover:bg-surface-2/50",
        !isSelected && tint === "red" && "hover:bg-red-soft",
        !isSelected && tint === "yellow" && "hover:bg-yellow-soft",
        onClick && "cursor-pointer",
      )}
      style={{ height: ROW_HEIGHT, minHeight: ROW_HEIGHT }}
      type="button"
      role="row"
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
  );
}

// Memo equality: only the four observable props that affect render
// output. `onClick` is intentionally excluded — DataTable wraps it in
// a stable ref-backed handler whose identity never changes for the
// row's lifetime, so the row never re-renders due to a callback
// identity churn at the page level.
const DataTableRow = memo(
  DataTableRowImpl,
  (prev, next) =>
    prev.row === next.row &&
    prev.isSelected === next.isSelected &&
    prev.tint === next.tint &&
    prev.columns === next.columns,
) as typeof DataTableRowImpl;
