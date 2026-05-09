import { useNavigate } from "react-router-dom";
import { useAddons } from "../../hooks/useAddons";
import { isBackendNotEKS } from "../../lib/api";
import { cn } from "../../lib/cn";

// AddOnsCard — at-a-glance status pill on the cluster overview page.
// Sized to sit next to UpgradeReadinessCard / NodeGroupsCard. The
// three glyphs (●/▲/✕) and counts mirror the issue mockup.
//
// Empty / error / loading states match the sibling cards so the
// overview feels consistent. Non-EKS clusters render nothing — same
// pattern as NodeGroupsCard / UpgradeReadinessCard.

export function AddOnsCard({ cluster }: { cluster: string }) {
  const navigate = useNavigate();
  const { data, isLoading, isError, error } = useAddons(cluster);

  if (isError && isBackendNotEKS(error)) return null;

  return (
    <button
      type="button"
      onClick={() =>
        navigate(`/clusters/${encodeURIComponent(cluster)}/addons`)
      }
      className="w-full rounded-md border border-border bg-surface px-4 py-3 text-left transition-colors hover:bg-surface-2"
    >
      <div className="flex items-center justify-between">
        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          Add-ons
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
          <div className="text-[12px] text-red">Failed to load add-ons.</div>
        ) : data ? (
          <CountsRow
            healthy={data.counts.healthy}
            updateAvailable={data.counts.updateAvailable}
            unhealthy={data.counts.unhealthy}
            blocksNextMinor={data.counts.blocksNextMinor}
          />
        ) : null}
      </div>
    </button>
  );
}

function CountsRow({
  healthy,
  updateAvailable,
  unhealthy,
  blocksNextMinor,
}: {
  healthy: number;
  updateAvailable: number;
  unhealthy: number;
  blocksNextMinor: number;
}) {
  return (
    <div className="flex items-center gap-4 text-[13px]">
      <Bucket glyph="●" tone="green" count={healthy} label="healthy" />
      {updateAvailable > 0 && (
        <Bucket
          glyph="▲"
          tone="yellow"
          count={updateAvailable}
          label="update"
        />
      )}
      {unhealthy > 0 && (
        <Bucket glyph="✕" tone="red" count={unhealthy} label="failing" />
      )}
      {blocksNextMinor > 0 && (
        <Bucket
          glyph="▲"
          tone="yellow"
          count={blocksNextMinor}
          label="blocks next k8s"
        />
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
