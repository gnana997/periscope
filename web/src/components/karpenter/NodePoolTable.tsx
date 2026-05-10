// NodePoolTable — top panel of the Karpenter dashboard. One row per
// NodePool with the operator-relevant slots: weight (priority),
// disruption knobs, current-vs-limit utilization, node count, and
// the cost summary (when metrics are available).
//
// Cluster-total $/hr renders above the table so an operator landing
// on the page can see the autoscaler's current spend at a glance —
// the single most-asked-for "how much is Karpenter costing me?"
// answer.

import { Link } from "react-router-dom";
import type { NodePoolView } from "../../lib/types";
import { cn } from "../../lib/cn";

interface Props {
  cluster: string;
  pools: NodePoolView[];
  metricsAvailable: boolean;
}

export function NodePoolTable({ cluster, pools, metricsAvailable }: Props) {
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

      <div className="overflow-hidden rounded-sm border border-border">
        <table className="w-full table-fixed text-left font-mono text-[11.5px]">
          <thead className="bg-surface text-[10.5px] uppercase tracking-[0.08em] text-ink-faint">
            <tr>
              <th className="w-1/4 px-3 py-1.5">name</th>
              <th className="w-16 px-3 py-1.5">weight</th>
              <th className="w-32 px-3 py-1.5">disruption</th>
              <th className="px-3 py-1.5">limits / usage</th>
              <th className="w-16 px-3 py-1.5 text-right">nodes</th>
              {metricsAvailable ? (
                <>
                  <th className="w-20 px-3 py-1.5 text-right">$/hr</th>
                  <th className="w-16 px-3 py-1.5 text-right">spot</th>
                </>
              ) : null}
            </tr>
          </thead>
          <tbody>
            {pools.map((p) => (
              <NodePoolRow
                key={p.name}
                cluster={cluster}
                pool={p}
                metricsAvailable={metricsAvailable}
              />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function NodePoolRow({
  cluster,
  pool,
  metricsAvailable,
}: {
  cluster: string;
  pool: NodePoolView;
  metricsAvailable: boolean;
}) {
  const limits = pool.limits ?? {};
  const usage = pool.usage ?? {};
  const limitKeys = Object.keys(limits);

  return (
    <tr className="border-t border-border first:border-t-0 hover:bg-surface-2">
      <td className="px-3 py-1.5">
        <Link
          to={`/clusters/${encodeURIComponent(
            cluster,
          )}/customresources/karpenter.sh/v1/nodepools/${encodeURIComponent(
            pool.name,
          )}`}
          className="text-ink hover:text-accent"
        >
          {pool.name}
        </Link>
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
                <span className="text-ink">{usage[k] ?? "—"}</span>
                <span className="text-ink-faint">{" / "}{limits[k]}</span>
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
  const parts: string[] = [];
  if (d.consolidationPolicy) parts.push(d.consolidationPolicy);
  if (d.consolidateAfter) parts.push(`after ${d.consolidateAfter}`);
  if (d.expireAfter) parts.push(`expire ${d.expireAfter}`);
  if (d.budgets && d.budgets.length > 0) {
    parts.push(`${d.budgets.length} budget${d.budgets.length === 1 ? "" : "s"}`);
  }
  if (parts.length === 0) return <span className="text-ink-faint">—</span>;
  return <>{parts.join(" · ")}</>;
}
