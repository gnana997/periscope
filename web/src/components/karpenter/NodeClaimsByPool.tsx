// NodeClaimsByPool — middle panel of the Karpenter dashboard. Groups
// NodeClaims by their owning NodePool (`karpenter.sh/nodepool` label)
// and renders one collapsible block per pool. Each row inside shows
// instance type, capacity type (spot/on-demand), zone, age, and the
// status conditions operators care about (Drifted in particular).
//
// Click-through goes to the existing CRD detail view —
// /clusters/{c}/customresources/karpenter.sh/v1/nodeclaims/{name} —
// so this panel is purely a join + summary, no new detail page.

import { Link } from "react-router-dom";
import { useState } from "react";
import type { NodeClaimView } from "../../lib/types";
import { cn } from "../../lib/cn";

interface Props {
  cluster: string;
  claims: NodeClaimView[];
}

export function NodeClaimsByPool({ cluster, claims }: Props) {
  if (claims.length === 0) {
    return (
      <p className="px-1 py-4 font-mono text-[12px] text-ink-faint">
        no NodeClaims — no nodes provisioned by Karpenter yet.
      </p>
    );
  }

  // Bucket by pool. Stable sort by name within each bucket so the
  // render is deterministic across refetches.
  const byPool = new Map<string, NodeClaimView[]>();
  for (const c of claims) {
    const pool = c.nodepool || "(no pool label)";
    if (!byPool.has(pool)) byPool.set(pool, []);
    byPool.get(pool)!.push(c);
  }
  for (const arr of byPool.values()) {
    arr.sort((a, b) => a.name.localeCompare(b.name));
  }
  const pools = Array.from(byPool.keys()).sort();

  return (
    <div className="space-y-2">
      <div className="flex items-baseline justify-between border-b border-border px-1 pb-2">
        <h2 className="font-mono text-[11.5px] uppercase tracking-[0.14em] text-ink-faint">
          NodeClaims by pool ({claims.length} total)
        </h2>
        <span className="font-mono text-[10.5px] text-ink-faint">
          click any row → CRD detail
        </span>
      </div>
      <div className="space-y-2">
        {pools.map((p) => (
          <PoolGroup key={p} cluster={cluster} pool={p} claims={byPool.get(p)!} />
        ))}
      </div>
    </div>
  );
}

function PoolGroup({
  cluster,
  pool,
  claims,
}: {
  cluster: string;
  pool: string;
  claims: NodeClaimView[];
}) {
  // Default open when the pool has any drifted claim — operators
  // landing on the page should see the drift signal without a click.
  const hasDrift = claims.some((c) =>
    (c.conditions ?? []).some(
      (cd) => cd.type === "Drifted" && cd.status === "True",
    ),
  );
  const [open, setOpen] = useState(hasDrift);

  return (
    <details
      className="rounded-sm border border-border"
      open={open}
      onToggle={(e) => setOpen(e.currentTarget.open)}
    >
      <summary className="cursor-pointer select-none px-3 py-1.5 font-mono text-[11.5px] tracking-[0.06em] text-ink-muted transition-colors hover:text-ink">
        <span className="text-ink-faint">{pool}</span>
        <span className="ml-2 text-ink-faint">
          ({claims.length} {claims.length === 1 ? "node" : "nodes"})
        </span>
        {hasDrift ? (
          <span className="ml-2 rounded-sm border border-yellow/40 bg-yellow/5 px-1.5 py-0 font-mono text-[10px] text-yellow">
            drifted
          </span>
        ) : null}
      </summary>
      <div className="space-y-1 px-3 pb-3 pt-1">
        {claims.map((c) => (
          <ClaimRow key={c.name} cluster={cluster} claim={c} />
        ))}
      </div>
    </details>
  );
}

function ClaimRow({
  cluster,
  claim,
}: {
  cluster: string;
  claim: NodeClaimView;
}) {
  const drifted = (claim.conditions ?? []).some(
    (c) => c.type === "Drifted" && c.status === "True",
  );
  const initialized = (claim.conditions ?? []).find(
    (c) => c.type === "Initialized",
  );
  return (
    <Link
      to={`/clusters/${encodeURIComponent(
        cluster,
      )}/customresources/karpenter.sh/v1/nodeclaims/${encodeURIComponent(
        claim.name,
      )}`}
      className="flex items-baseline gap-3 rounded-sm px-2 py-1 font-mono text-[11px] text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink"
    >
      <span className="w-44 shrink-0 truncate text-ink">{claim.name}</span>
      <span className="w-24 shrink-0 text-ink-faint">
        {claim.instanceType ?? "—"}
      </span>
      <span
        className={cn(
          "w-16 shrink-0 text-[10.5px] uppercase tracking-[0.08em]",
          claim.capacityType === "spot" ? "text-green" : "text-ink-faint",
        )}
      >
        {claim.capacityType ?? ""}
      </span>
      <span className="w-24 shrink-0 text-ink-faint">{claim.zone ?? "—"}</span>
      <span className="flex-1 text-ink-faint">
        {initialized && initialized.status === "True"
          ? "initialized"
          : initialized
            ? `init: ${initialized.reason ?? "false"}`
            : "—"}
      </span>
      {drifted ? (
        <span className="rounded-sm border border-yellow/40 bg-yellow/5 px-1.5 py-0 text-[10px] text-yellow">
          drifted
        </span>
      ) : null}
    </Link>
  );
}
