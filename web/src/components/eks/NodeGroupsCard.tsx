import { useNavigate } from "react-router-dom";
import { useNodegroups } from "../../hooks/useNodegroups";
import { isBackendNotEKS } from "../../lib/api";
import { cn } from "../../lib/cn";

// NodeGroupsCard — at-a-glance status pill on the cluster overview
// page, sized to sit next to UpgradeReadinessCard.
//
// Empty / error / loading states mirror the insights card so the
// two surfaces feel consistent. Drift counts ("N behind") are only
// shown when the backend actually computed drift (PR-3); in PR-2
// the row simply reads "N node groups · M custom".

export function NodeGroupsCard({ cluster }: { cluster: string }) {
  const navigate = useNavigate();
  const { data, isLoading, isError, error } = useNodegroups(cluster);

  if (isError && isBackendNotEKS(error)) return null;

  return (
    <button
      type="button"
      onClick={() =>
        navigate(`/clusters/${encodeURIComponent(cluster)}/nodegroups`)
      }
      className="w-full rounded-md border border-border bg-surface px-4 py-3 text-left transition-colors hover:bg-surface-2"
    >
      <div className="flex items-center justify-between">
        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          Node groups
        </div>
        {data?.counts && (
          <div className="font-mono text-[11px] text-ink-faint">
            {data.counts.total} total
          </div>
        )}
      </div>
      <div className="mt-2 min-h-[28px]">
        {isLoading ? (
          <div className="text-[12px] italic text-ink-faint">Loading…</div>
        ) : isError ? (
          <div className="text-[12px] text-red">Failed to load node groups.</div>
        ) : data ? (
          <CountsRow
            healthy={data.counts.healthy}
            behind={data.counts.behind}
            custom={data.counts.custom}
            driftAvailable={data.nodegroups.some((n) => n.driftComputed)}
          />
        ) : null}
      </div>
    </button>
  );
}

function CountsRow({
  healthy,
  behind,
  custom,
  driftAvailable,
}: {
  healthy: number;
  behind: number;
  custom: number;
  driftAvailable: boolean;
}) {
  return (
    <div className="flex items-center gap-4 text-[13px]">
      <Bucket glyph="●" tone="green" count={healthy} label="healthy" />
      {driftAvailable ? (
        <Bucket
          glyph={behind > 0 ? "▲" : "●"}
          tone={behind > 0 ? "yellow" : "green"}
          count={behind}
          label={behind === 1 ? "behind" : "behind"}
        />
      ) : null}
      {custom > 0 && (
        <Bucket glyph="—" tone="faint" count={custom} label="custom" />
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
