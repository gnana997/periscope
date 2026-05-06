import { useNavigate } from "react-router-dom";
import { useUpgradeInsights } from "../../hooks/useUpgradeInsights";
import { isBackendNotEKS } from "../../lib/api";
import { cn } from "../../lib/cn";

// UpgradeReadinessCard — at-a-glance status pill on the cluster
// overview page. Three-bucket count + target K8s version, click
// through to the dedicated tab for the full insight list.
//
// Empty states:
//   - non-EKS cluster (422 / E_BACKEND_NOT_EKS) → renders nothing.
//     The full surface lives on the dedicated page; the overview
//     card is purely a forwarder, and showing "this cluster is not
//     EKS" on the overview adds noise for every kubeconfig user.
//   - other errors → small inline error message; does not block
//     the rest of the overview.

export function UpgradeReadinessCard({ cluster }: { cluster: string }) {
  const navigate = useNavigate();
  const { data, isLoading, isError, error } = useUpgradeInsights(cluster);

  if (isError && isBackendNotEKS(error)) return null;

  const goToTab = () =>
    navigate(`/clusters/${encodeURIComponent(cluster)}/upgrade-readiness`);

  return (
    <button
      type="button"
      onClick={goToTab}
      className="w-full rounded-md border border-border bg-surface px-4 py-3 text-left transition-colors hover:bg-surface-2"
    >
      <div className="flex items-center justify-between">
        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          Upgrade readiness
        </div>
        {data?.targetKubernetesVersion && (
          <div className="font-mono text-[11px] text-ink-faint">
            target {data.targetKubernetesVersion}
          </div>
        )}
      </div>
      <div className="mt-2 min-h-[28px]">
        {isLoading ? (
          <div className="text-[12px] italic text-ink-faint">Loading…</div>
        ) : isError ? (
          <div className="text-[12px] text-red">Failed to load insights.</div>
        ) : data ? (
          <CountsRow
            passing={data.counts.passing}
            warning={data.counts.warning}
            error={data.counts.error}
            unknown={data.counts.unknown}
          />
        ) : null}
      </div>
    </button>
  );
}

function CountsRow({
  passing,
  warning,
  error,
  unknown,
}: {
  passing: number;
  warning: number;
  error: number;
  unknown: number;
}) {
  return (
    <div className="flex items-center gap-4 text-[13px]">
      <Bucket glyph="●" tone="green" count={passing} label="passing" />
      <Bucket glyph="▲" tone="yellow" count={warning} label="warning" />
      <Bucket glyph="✕" tone="red" count={error} label="error" />
      {unknown > 0 && (
        <Bucket glyph="?" tone="faint" count={unknown} label="unknown" />
      )}
    </div>
  );
}

function Bucket({
  glyph,
  tone,
  count,
  label,
}: {
  glyph: string;
  tone: "green" | "yellow" | "red" | "faint";
  count: number;
  label: string;
}) {
  return (
    <div className="flex items-center gap-1.5">
      <span
        className={cn(
          "font-mono text-[14px] leading-none",
          tone === "green" && "text-green",
          tone === "yellow" && "text-yellow",
          tone === "red" && "text-red",
          tone === "faint" && "text-ink-faint",
        )}
        aria-hidden
      >
        {glyph}
      </span>
      <span className="font-mono tabular-nums">{count}</span>
      <span className="text-[11px] text-ink-faint">{label}</span>
    </div>
  );
}
