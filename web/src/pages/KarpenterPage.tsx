// KarpenterPage — curated Karpenter dashboard (issue #118).
//
// Three panels stacked vertically:
//   1. NodePoolTable — pool list with weight / disruption / cost
//   2. NodeClaimsByPool — per-pool node grouping with Drift badges
//   3. PendingPodsPanel — pending pods with per-NodePool reject reasons
//
// Selection model: clicking a NodePool name or NodeClaim row sets a
// `?sel=NodePool/<name>` (or `NodeClaim/<name>`) URL param. When set,
// a slide-in DetailPane shows on the right with describe / yaml /
// events tabs for that resource. Operators stay on the Karpenter
// page — no jump to the generic CR catalog. Closing the pane drops
// the URL param.

import { useParams, useSearchParams } from "react-router-dom";
import { useKarpenter } from "../hooks/useKarpenter";
import { PageHeader } from "../components/page/PageHeader";
import { SplitPane } from "../components/page/SplitPane";
import { ErrorState, LoadingState } from "../components/table/states";
import { NodePoolTable } from "../components/karpenter/NodePoolTable";
import { NodeClaimsByPool } from "../components/karpenter/NodeClaimsByPool";
import { PendingPodsPanel } from "../components/karpenter/PendingPodsPanel";
import { KarpenterDetailPane } from "../components/karpenter/KarpenterDetailPane";

export function KarpenterPage() {
  const { cluster } = useParams<{ cluster: string }>();
  const cl = cluster ?? "";
  const query = useKarpenter(cl);
  const [params, setParams] = useSearchParams();

  // Parse `?sel=Kind/Name` into the pane's two args.
  const sel = params.get("sel") ?? "";
  const slash = sel.indexOf("/");
  const selKind = slash > 0 ? (sel.slice(0, slash) as "NodePool" | "NodeClaim") : null;
  const selName = slash > 0 ? sel.slice(slash + 1) : "";
  const validKind = selKind === "NodePool" || selKind === "NodeClaim";

  const select = (kind: "NodePool" | "NodeClaim", name: string) => {
    setParams((prev) => {
      const next = new URLSearchParams(prev);
      next.set("sel", `${kind}/${name}`);
      return next;
    });
  };
  const clear = () => {
    setParams((prev) => {
      const next = new URLSearchParams(prev);
      next.delete("sel");
      return next;
    });
  };

  if (query.isPending) return <LoadingState resource="karpenter" />;
  if (query.isError) {
    return (
      <ErrorState
        title="couldn't load Karpenter dashboard"
        message={(query.error as Error)?.message ?? "unknown"}
      />
    );
  }

  const data = query.data!;
  if (!data.available) {
    return (
      <div className="flex h-full flex-col">
        <PageHeader title="Karpenter" subtitle={cl} />
        <div className="flex flex-1 items-center justify-center px-6 py-10 text-center">
          <div className="max-w-md space-y-3">
            <p className="font-mono text-[12px] uppercase tracking-[0.14em] text-ink-faint">
              karpenter not detected
            </p>
            <p className="text-[13px] text-ink-muted">
              No <code className="font-mono text-ink">karpenter.sh/v1</code>{" "}
              CRDs found on this cluster. The dashboard auto-detects via
              the discovery API; install Karpenter (or upgrade from
              v1beta1 to v1) and reload.
            </p>
            <p className="text-[12px] text-ink-faint">
              <a
                href="https://karpenter.sh/docs/getting-started/"
                target="_blank"
                rel="noreferrer"
                className="hover:text-accent"
              >
                karpenter.sh getting started →
              </a>
            </p>
          </div>
        </div>
      </div>
    );
  }

  const detailOpen = Boolean(validKind && selName);

  // List panels — share rendering whether or not the detail pane is
  // open. SplitPane handles the narrowing itself; we just always feed
  // the same ReactNode into its `left` slot.
  const listPanels = (
    <div className="h-full space-y-6 overflow-y-auto px-6 py-4">
      <NodePoolTable
        pools={data.nodepools ?? []}
        metricsAvailable={data.metricsAvailable}
        onSelect={(name) => select("NodePool", name)}
        selectedName={selKind === "NodePool" ? selName : null}
      />
      <NodeClaimsByPool
        claims={data.nodeclaims ?? []}
        onSelect={(name) => select("NodeClaim", name)}
        selectedName={selKind === "NodeClaim" ? selName : null}
      />
      <PendingPodsPanel
        pods={data.pendingPods ?? []}
        truncated={data.truncated ?? false}
        onSelectPool={(name) => select("NodePool", name)}
      />
    </div>
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="Karpenter" subtitle={cl} />
      <div className="flex min-h-0 flex-1">
        <SplitPane
          storageKey="periscope.detailWidth.karpenter"
          left={listPanels}
          right={
            detailOpen ? (
              <KarpenterDetailPane
                cluster={cl}
                kind={selKind as "NodePool" | "NodeClaim"}
                name={selName}
                onClose={clear}
              />
            ) : null
          }
        />
      </div>
    </div>
  );
}
