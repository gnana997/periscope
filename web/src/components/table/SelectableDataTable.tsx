// SelectableDataTable — DataTable + bulk-actions toolbar wired to a
// per-instance selection model.
//
// Pages swap their existing <DataTable> for this when they want a
// checkbox column and YAML bulk download. The component owns the
// selection state internally so pages don't need to thread anything
// through props beyond the bulk-fetch parameters.

import { useMemo } from "react";
import { DataTable, type DataTableProps } from "./DataTable";
import { BulkActionsToolbar } from "./BulkActionsToolbar";
import { useTableSelection } from "../../hooks/useTableSelection";

export interface BulkOptions<T> {
  cluster: string;
  /** Lowercase plural kind. Drives the filename and toast wording. */
  kindLabel: string;
  fetchYaml: (row: T, signal: AbortSignal) => Promise<string>;
  /** Optional — used to gate the secret-reveal confirm dialog. */
  isSecret?: (row: T) => boolean;
  /** Override the default 100-row cap. */
  cap?: number;
}

export interface SelectableDataTableProps<T>
  extends Omit<DataTableProps<T>, "selection"> {
  bulk: BulkOptions<T>;
}

export function SelectableDataTable<T>({
  bulk,
  ...tableProps
}: SelectableDataTableProps<T>) {
  const selection = useTableSelection({
    kindLabel: bulk.kindLabel,
    cap: bulk.cap,
  });

  // Memoize so DataTable's column/row memoization stays intact when
  // selection state mutates — the table doesn't re-render unnecessarily.
  const tableNode = useMemo(
    () => <DataTable {...tableProps} selection={selection} />,
    [tableProps, selection],
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <BulkActionsToolbar
        selection={selection}
        rows={tableProps.rows}
        rowKey={tableProps.rowKey}
        cluster={bulk.cluster}
        kindLabel={bulk.kindLabel}
        fetchYaml={bulk.fetchYaml}
        isSecret={bulk.isSecret}
      />
      {tableNode}
    </div>
  );
}
