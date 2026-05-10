// KarpenterPage — curated Karpenter dashboard (issue #118).
//
// Three panels stacked vertically:
//   1. NodePoolTable — pool list with weight / disruption / cost
//   2. NodeClaimsByPool — per-pool node grouping with Drift badges
//   3. PendingPodsPanel — pending pods with per-NodePool reject reasons
//
// All three render from one query result (useKarpenter). Sidebar
// entry uses the same query so loading the page is one network call,
// not two.

import { useParams } from "react-router-dom";
import { useKarpenter } from "../hooks/useKarpenter";
import { PageHeader } from "../components/page/PageHeader";
import { ErrorState, LoadingState } from "../components/table/states";
import { NodePoolTable } from "../components/karpenter/NodePoolTable";
import { NodeClaimsByPool } from "../components/karpenter/NodeClaimsByPool";
import { PendingPodsPanel } from "../components/karpenter/PendingPodsPanel";

export function KarpenterPage() {
  const { cluster } = useParams<{ cluster: string }>();
  const cl = cluster ?? "";
  const query = useKarpenter(cl);

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

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader title="Karpenter" subtitle={cl} />
      <div className="flex-1 space-y-6 overflow-y-auto px-6 py-4">
        <NodePoolTable
          cluster={cl}
          pools={data.nodepools ?? []}
          metricsAvailable={data.metricsAvailable}
        />
        <NodeClaimsByPool cluster={cl} claims={data.nodeclaims ?? []} />
        <PendingPodsPanel
          cluster={cl}
          pods={data.pendingPods ?? []}
          truncated={data.truncated ?? false}
        />
      </div>
    </div>
  );
}
