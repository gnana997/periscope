import type { ReactNode } from "react";
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
      <div className="flex-1 overflow-auto" role="rowgroup">
        {rows.map((row) => {
          const key = rowKey(row);
          const tint = rowTint?.(row) ?? null;
          const isSelected = selectedKey === key;

          return (
            <button
              key={key}
              onClick={() => onRowClick?.(row)}
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
                onRowClick && "cursor-pointer",
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
        })}
      </div>
    </div>
  );
}
