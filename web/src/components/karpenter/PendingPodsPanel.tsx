// PendingPodsPanel — bottom panel of the Karpenter dashboard. Lists
// pods stuck Pending with the per-NodePool incompatibility breakdown
// the backend extracted from FailedScheduling apiserver Events.
//
// This is the differentiator vs `kubectl get pods --field-selector
// status.phase=Pending` — operators don't have to grep
// karpenter-controller logs to find out *why* the pod isn't being
// scheduled. The reasons render inline next to the pod, one row per
// rejecting NodePool.
//
// When the backend couldn't find a matching event (older Karpenter,
// event already deduped past the 5-min window, or the events list
// call failed), the row falls back to the raw `reason` field with
// a "no breakdown available" hint — still useful, just not as deep.

import { useState } from "react";
import type { PendingPodView } from "../../lib/types";
import { cn } from "../../lib/cn";

interface Props {
  onSelectPool: (name: string) => void;
  /* removed: cluster (no longer needed since pool click is handled via onSelectPool) */
  pods: PendingPodView[];
  truncated: boolean;
}

export function PendingPodsPanel({ pods, truncated, onSelectPool }: Props) {
  if (pods.length === 0) {
    return (
      <div className="space-y-2">
        <h2 className="border-b border-border px-1 pb-2 font-mono text-[11.5px] uppercase tracking-[0.14em] text-ink-faint">
          Pending pods
        </h2>
        <p className="px-1 py-4 font-mono text-[12px] text-ink-faint">
          no pods waiting on Karpenter — every workload that needs
          capacity has it.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between border-b border-border px-1 pb-2">
        <h2 className="font-mono text-[11.5px] uppercase tracking-[0.14em] text-ink-faint">
          Pending pods waiting on Karpenter ({pods.length}
          {truncated ? "+" : ""})
        </h2>
        {truncated ? (
          <span className="font-mono text-[10.5px] uppercase tracking-[0.1em] text-yellow">
            showing first 50 — narrow by namespace via kubectl for full list
          </span>
        ) : null}
      </div>
      <div className="space-y-1">
        {pods.map((pod) => (
          <PendingPodRow
            key={`${pod.namespace}/${pod.name}`}
            onSelectPool={onSelectPool}
            pod={pod}
          />
        ))}
      </div>
    </div>
  );
}

function PendingPodRow({
  pod,
  onSelectPool,
}: {
  pod: PendingPodView;
  onSelectPool: (name: string) => void;
}) {
  const hasBreakdown = (pod.incompatibilityReasons ?? []).length > 0;
  // Auto-expand when there's a breakdown to show — the operator's
  // reason for opening this page is to see why pods aren't scheduling.
  const [open, setOpen] = useState(hasBreakdown);

  return (
    <details
      className="rounded-sm border border-border"
      open={open}
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary className="flex cursor-pointer select-none items-baseline gap-3 px-3 py-1.5 font-mono text-[11.5px] tracking-[0.04em] text-ink-muted transition-colors hover:text-ink">
        <span className="w-56 shrink-0 truncate text-ink">
          {pod.namespace}/{pod.name}
        </span>
        <span className="w-16 shrink-0 text-ink-faint">
          pending {pod.pendingFor}
        </span>
        <span className="flex-1 truncate text-[10.5px] text-ink-faint">
          {hasBreakdown
            ? `${pod.incompatibilityReasons!.length} NodePool${
                pod.incompatibilityReasons!.length === 1 ? "" : "s"
              } rejected`
            : pod.reason
              ? "kube-scheduler reason — expand for detail"
              : "no scheduling event found yet"}
        </span>
      </summary>
      <div className="space-y-2 px-3 pb-3 pt-1">
        {hasBreakdown ? (
          <ul className="space-y-1">
            {pod.incompatibilityReasons!.map((r, i) => (
              <li
                key={`${r.nodepool}-${i}`}
                className="flex items-baseline gap-2 font-mono text-[11px]"
              >
                <span className={cn("text-red shrink-0")}>×</span>
                <button
                  type="button"
                  onClick={() => onSelectPool(r.nodepool)}
                  className="w-32 shrink-0 truncate text-left text-ink hover:text-accent"
                >
                </button>
                <span className="flex-1 text-ink-muted">{r.reason}</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-[11px] text-ink-faint">
            {pod.reason ? (
              <>
                <span className="text-ink-muted">event: </span>
                {pod.reason}
              </>
            ) : (
              "no FailedScheduling event found within the apiserver event window."
            )}
          </p>
        )}
      </div>
    </details>
  );
}
