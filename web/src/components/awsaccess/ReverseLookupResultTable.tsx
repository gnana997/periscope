// ReverseLookupResultTable renders the pod-row result list with
// react-virtual so a SA bound to thousands of pods doesn't freeze
// the browser. Each row links to that pod's detail with the
// aws-access tab pre-opened.

import { useNavigate } from "react-router-dom";
import { useRef } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";

import { cn } from "../../lib/cn";
import type { ReverseLookupPodRow } from "../../lib/identity";
import { SensitivePermChip } from "./SensitivePermChip";

export interface ReverseLookupResultTableProps {
  cluster: string;
  rows: ReverseLookupPodRow[];
  truncated: boolean;
  totalPods: number;
}

export function ReverseLookupResultTable({
  cluster,
  rows,
  truncated,
  totalPods,
}: ReverseLookupResultTableProps) {
  const parentRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  const rowVirt = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 36,
    overscan: 8,
  });

  if (rows.length === 0) {
    return (
      <p className="mt-4 text-[13px] text-ink-faint">
        No pods match this query. (Empty results are a valid answer — nothing in this cluster can perform that action on that resource.)
      </p>
    );
  }

  return (
    <div className="mt-4">
      <div className="mb-1 flex flex-wrap items-baseline gap-3 text-[12.5px]">
        <span className="text-ink">
          {rows.length} row{rows.length === 1 ? "" : "s"}
          {truncated ? <> · showing first {rows.length} of {totalPods} total pods</> : null}
        </span>
      </div>
      <div
        ref={parentRef}
        className="max-h-[60vh] overflow-y-auto rounded-md border border-border"
      >
        <div
          style={{ height: `${rowVirt.getTotalSize()}px`, position: "relative" }}
        >
          {rowVirt.getVirtualItems().map((vItem) => {
            const row = rows[vItem.index];
            return (
              <div
                key={vItem.key}
                ref={rowVirt.measureElement}
                data-index={vItem.index}
                style={{
                  position: "absolute",
                  top: 0,
                  left: 0,
                  width: "100%",
                  transform: `translateY(${vItem.start}px)`,
                }}
                className="grid grid-cols-[1.5fr,1fr,2fr,1fr] items-center gap-2 border-b border-border px-3 py-2 text-[12.5px]"
              >
                <button
                  type="button"
                  onClick={() =>
                    navigate(
                      `/clusters/${encodeURIComponent(cluster)}/pods?ns=${encodeURIComponent(
                        row.pod.namespace,
                      )}&sel=${encodeURIComponent(row.pod.name)}&selNs=${encodeURIComponent(
                        row.pod.namespace,
                      )}&tab=aws-access`,
                    )
                  }
                  className="truncate text-left font-mono text-ink hover:underline"
                >
                  {row.pod.namespace}/{row.pod.name}
                </button>
                <span className="truncate font-mono text-ink-muted">{row.saName}</span>
                <span className="truncate font-mono text-ink-muted">{row.roleArn}</span>
                <div className="flex flex-wrap items-center gap-1.5">
                  {row.source ? (
                    <span
                      className={cn(
                        "rounded-sm border px-1.5 py-0.5 font-mono text-[11px]",
                        row.source === "IRSA" && "border-blue-500/30 bg-blue-500/10 text-blue-500",
                        row.source === "PodIdentity" && "border-emerald-500/30 bg-emerald-500/10 text-emerald-500",
                        row.source === "Both" && "border-purple-500/30 bg-purple-500/10 text-purple-500",
                      )}
                    >
                      {row.source}
                    </span>
                  ) : null}
                  {row.permission.sensitive && row.permission.sensitiveReason ? (
                    <SensitivePermChip category={row.permission.sensitiveReason} />
                  ) : null}
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
