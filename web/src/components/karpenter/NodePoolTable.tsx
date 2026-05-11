// NodePoolTable — top panel of the Karpenter dashboard. One row per
// NodePool with the operator-relevant slots: weight (priority),
// disruption knobs, current-vs-limit utilization, node count, and
// the cost summary (when metrics are available).
//
// Cluster-total $/hr renders above the table so an operator landing
// on the page can see the autoscaler's current spend at a glance —
// the single most-asked-for "how much is Karpenter costing me?"
// answer.

import type { NodePoolView } from "../../lib/types";
import { cn } from "../../lib/cn";

interface Props {
  pools: NodePoolView[];
  metricsAvailable: boolean;
  onSelect: (name: string) => void;
  selectedName: string | null;
}

export function NodePoolTable({ pools, metricsAvailable, onSelect, selectedName }: Props) {
  if (pools.length === 0) {
    return (
      <p className="px-1 py-4 font-mono text-[12px] text-ink-faint">
        no NodePools defined. add one via your GitOps pipeline (Karpenter
        is read-only here).
      </p>
    );
  }

  const totalHourly = pools.reduce(
    (sum, p) => sum + (p.cost?.currentHourly ?? 0),
    0,
  );
  const totalOnDemand = pools.reduce(
    (sum, p) => sum + (p.cost?.onDemandHourly ?? 0),
    0,
  );
  const totalSavings =
    totalOnDemand > 0 && totalOnDemand >= totalHourly
      ? Math.round((1 - totalHourly / totalOnDemand) * 100)
      : 0;

  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between border-b border-border px-1 pb-2">
        <h2 className="font-mono text-[11.5px] uppercase tracking-[0.14em] text-ink-faint">
          NodePools ({pools.length})
        </h2>
        {metricsAvailable && totalHourly > 0 ? (
          <span className="font-mono text-[11px] text-ink-muted">
            cluster total ·{" "}
            <span className="text-ink">${totalHourly.toFixed(2)}/hr</span>{" "}
            ({totalSavings}% spot savings)
          </span>
        ) : null}
        {!metricsAvailable ? (
          <span className="font-mono text-[10.5px] uppercase tracking-[0.1em] text-yellow">
            metrics unreachable — cost columns hidden
          </span>
        ) : null}
      </div>

      <div className="overflow-x-auto rounded-sm border border-border">
        <table className="w-full min-w-[760px] table-fixed text-left font-mono text-[11.5px]">
          <thead className="bg-surface text-[10.5px] uppercase tracking-[0.08em] text-ink-faint">
            <tr>
              <th className="w-44 px-3 py-1.5">name</th>
              <th className="w-16 px-3 py-1.5">weight</th>
              <th className="w-48 px-3 py-1.5">disruption</th>
              <th className="px-3 py-1.5">limits / usage</th>
              <th className="w-16 px-3 py-1.5 text-right">nodes</th>
              {metricsAvailable ? (
                <>
                  <th className="w-24 px-3 py-1.5 text-right">$/hr</th>
                  <th className="w-16 px-3 py-1.5 text-right">spot</th>
                </>
              ) : null}
            </tr>
          </thead>
          <tbody>
            {pools.map((p) => (
              <NodePoolRow
                key={p.name}

                pool={p}
                metricsAvailable={metricsAvailable}
                onSelect={onSelect}
                isSelected={p.name === selectedName}
              />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
function NodePoolRow({
  pool,
  metricsAvailable,
  onSelect,
  isSelected,
}: {
  pool: NodePoolView;
  metricsAvailable: boolean;
  onSelect: (name: string) => void;
  isSelected: boolean;
}) {
  const limits = pool.limits ?? {};
  const usage = pool.usage ?? {};
  const limitKeys = Object.keys(limits);

  return (
    <tr
      className={cn(
        "border-t border-border first:border-t-0 cursor-pointer hover:bg-surface-2",
        isSelected && "bg-accent-soft/40",
      )}
      onClick={() => onSelect(pool.name)}
    >
      <td className="px-3 py-1.5">
        <span className="text-ink">{pool.name}</span>
      </td>
      <td className="px-3 py-1.5 text-ink-muted tabular">
        {pool.weight ?? "—"}
      </td>
      <td className="px-3 py-1.5 text-ink-muted">
        <DisruptionSummary p={pool} />
      </td>
      <td className="px-3 py-1.5 text-ink-muted">
        {limitKeys.length === 0 ? (
          <span className="text-ink-faint">no limits</span>
        ) : (
          <ul className="space-y-0.5">
            {limitKeys.map((k) => (
              <li key={k} className="text-[11px]">
                <span className="text-ink-faint">{k}:</span>{" "}
                <span className="text-ink">{usage[k] ? formatResourceValue(k, usage[k]) : "—"}</span>
                <span className="text-ink-faint">{" / "}{formatResourceValue(k, limits[k])}</span>
              </li>
            ))}
          </ul>
        )}
      </td>
      <td className="px-3 py-1.5 text-right tabular text-ink">
        {pool.nodeCount}
      </td>
      {metricsAvailable ? (
        <>
          <td className="px-3 py-1.5 text-right tabular text-ink">
            {pool.cost ? `$${pool.cost.currentHourly.toFixed(2)}` : "—"}
          </td>
          <td
            className={cn(
              "px-3 py-1.5 text-right tabular",
              pool.cost && pool.cost.spotSavingsPct > 0
                ? "text-green"
                : "text-ink-muted",
            )}
          >
            {pool.cost ? `${pool.cost.spotSavingsPct}%` : "—"}
          </td>
        </>
      ) : null}
    </tr>
  );
}

function DisruptionSummary({ p }: { p: NodePoolView }) {
  const d = p.disruption;
  // Compact 2-line display: line 1 = consolidation policy + when,
  // line 2 = expire / budgets summary. Keeps the column readable
  // at the form's ~640px width without wrapping mid-word.
  const policy = (() => {
    if (!d.consolidationPolicy) return "—";
    if (d.consolidationPolicy === "WhenEmptyOrUnderutilized") return "empty/underutilized";
    if (d.consolidationPolicy === "WhenEmpty") return "empty only";
    return d.consolidationPolicy;
  })();
  const after = d.consolidateAfter ? `after ${d.consolidateAfter}` : "";
  const expire = d.expireAfter ? `expire ${d.expireAfter}` : "";
  const budgets = d.budgets && d.budgets.length > 0
    ? `${d.budgets.length} budget${d.budgets.length === 1 ? "" : "s"}`
    : "";
  return (
    <div className="space-y-0.5 text-[10.5px]">
      <div>{[policy, after].filter(Boolean).join(" · ")}</div>
      {(expire || budgets) && (
        <div className="text-ink-faint">
          {[expire, budgets].filter(Boolean).join(" · ")}
        </div>
      )}
    </div>
  );
}

// formatResourceValue renders a Karpenter NodePool resource value
// (string from the metrics scrape; bytes for memory, raw count for
// cpu / pods / nodes). Memory rendering matches kubectl's Mi/Gi style.
function formatResourceValue(resourceType: string, raw: string): string {
  if (!raw) return "—";
  const n = Number(raw);
  if (!Number.isFinite(n)) return raw;
  if (resourceType === "memory" || resourceType.startsWith("hugepages_") || resourceType === "ephemeral_storage") {
    if (n >= 1024 ** 4) return `${(n / 1024 ** 4).toFixed(1)}Ti`;
    if (n >= 1024 ** 3) return `${(n / 1024 ** 3).toFixed(1)}Gi`;
    if (n >= 1024 ** 2) return `${(n / 1024 ** 2).toFixed(1)}Mi`;
    if (n >= 1024) return `${(n / 1024).toFixed(1)}Ki`;
    return `${n}`;
  }
  // CPU + pod + node counts: render as integer when whole, else 1 decimal.
  return Number.isInteger(n) ? `${n}` : n.toFixed(1);
}
