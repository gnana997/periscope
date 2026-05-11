// NodeClaimsByPool — middle panel of the Karpenter dashboard. Groups
// NodeClaims by their owning NodePool (`karpenter.sh/nodepool` label)
// and renders one collapsible block per pool. Each row inside shows
// instance type, capacity type (spot/on-demand), zone, age, and the
// status conditions operators care about (Drifted in particular).
//
// Click-through goes to the existing CRD detail view —
// /clusters/{c}/customresources/karpenter.sh/v1/nodeclaims/{name} —
// so this panel is purely a join + summary, no new detail page.

import { useState } from "react";
import type { NodeClaimView, CveSeverityCounts } from "../../lib/types";
import { SeverityChip } from "../security/SeverityChip";
import { combineCounts } from "../../lib/severity";
import { cn } from "../../lib/cn";

interface Props {

  /** Per-instance CVE counts keyed by EC2 instance id. Built by the
   *  KarpenterPage parent from useCveByInstance + threaded down so
   *  the chip is a synchronous lookup, not a per-row fetch. */
  cveByInstance?: Map<string, CveSeverityCounts>;

  claims: NodeClaimView[];
  onSelect: (name: string) => void;
  selectedName: string | null;
}

export function NodeClaimsByPool({ claims, onSelect, selectedName, cveByInstance }: Props) {
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
          <PoolGroup key={p} pool={p} claims={byPool.get(p)!} onSelect={onSelect} selectedName={selectedName} cveByInstance={cveByInstance} />
        ))}
      </div>
    </div>
  );
}

function PoolGroup({

  pool,
  claims,
  onSelect,
  selectedName,
  cveByInstance,
}: {

  pool: string;
  claims: NodeClaimView[];
  onSelect: (name: string) => void;
  selectedName: string | null;
  cveByInstance?: Map<string, CveSeverityCounts>;
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
        {cveByInstance ? (
          <span className="ml-2">
            <SeverityChip
              mode="compact"
              counts={aggregateClaimSeverity(claims, cveByInstance)}
              state="has-findings"
            />
          </span>
        ) : null}
      </summary>
      <div className="space-y-1 px-3 pb-3 pt-1">
        {claims.map((c) => (
          <ClaimRow key={c.name} claim={c} onSelect={onSelect} isSelected={c.name === selectedName} cveByInstance={cveByInstance} />
        ))}
      </div>
    </details>
  );
}

function ClaimRow({

  claim,
  onSelect,
  isSelected,
  cveByInstance,
}: {

  claim: NodeClaimView;
  onSelect: (name: string) => void;
  isSelected: boolean;
  cveByInstance?: Map<string, CveSeverityCounts>;
}) {
  const drifted = (claim.conditions ?? []).some(
    (c) => c.type === "Drifted" && c.status === "True",
  );
  const initialized = (claim.conditions ?? []).find(
    (c) => c.type === "Initialized",
  );
  return (
    <button
      type="button"
      onClick={() => onSelect(claim.name)}
      className={cn(
        "flex w-full items-baseline gap-3 rounded-sm px-2 py-1 text-left font-mono text-[11px] text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink",
        isSelected && "bg-accent-soft/40 text-ink",
      )}
    >
      <span className="w-44 shrink-0 truncate text-ink">{claim.name}</span>
      {claim.instanceType ? (
        <>
          <span className="w-24 shrink-0 text-ink-faint">
            {claim.instanceType}
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
        </>
      ) : (
        <span className="flex-1 truncate italic text-ink-faint">
          provisioning… {provisioningHint(claim)}
        </span>
      )}
      {drifted ? (
        <span className="rounded-sm border border-yellow/40 bg-yellow/5 px-1.5 py-0 text-[10px] text-yellow">
          drifted
        </span>
      ) : null}
      {(() => {
        const id = claim.providerID && claim.providerID.match(/\/(i-[a-f0-9]+)$/)?.[1];
        if (!id || !cveByInstance) return null;
        const s = cveByInstance.get(id);
        if (!s) return null;
        const isClean = s.critical + s.high + s.medium + s.low === 0;
        return (
          <SeverityChip
            mode="compact"
            counts={s}
            state={isClean ? "clean" : "has-findings"}
          />
        );
      })()}
    </button>
  );
}

// provisioningHint pulls the most informative text from a NodeClaim's
// status when it hasn't yet been bound to an EC2 instance. Surfaces
// the Launched/Registered/Ready condition reason or message so the
// operator sees "permission denied creating SLR" instead of an opaque
// "AwaitingReconciliation".
function provisioningHint(claim: NodeClaimView): string {
  const conds = claim.conditions ?? [];
  // Priority order: Launched (most informative when failing) > Registered > Ready.
  for (const t of ["Launched", "Registered", "Ready"]) {
    const c = conds.find((x) => x.type === t);
    if (c && c.status !== "True") {
      return c.message ?? c.reason ?? "";
    }
  }
  return "";
}

function aggregateClaimSeverity(
  claims: NodeClaimView[],
  cveByInstance: Map<string, CveSeverityCounts>,
): CveSeverityCounts {
  const slices: CveSeverityCounts[] = [];
  for (const c of claims) {
    const m = c.providerID?.match(/\/(i-[a-f0-9]+)$/);
    const id = m ? m[1] : "";
    if (!id) continue;
    const s = cveByInstance.get(id);
    if (s) slices.push(s);
  }
  return combineCounts(...slices);
}
