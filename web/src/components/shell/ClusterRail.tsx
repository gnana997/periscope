import { useNavigate, useParams } from "react-router-dom";
import { useClusters } from "../../hooks/useClusters";
import { cn } from "../../lib/cn";
import { UserMenu } from "./UserMenu";
import type { Cluster } from "../../lib/types";

// ClusterRail is the leftmost vertical bar — Slack/Discord style. Each
// cluster is a clickable pill; the active cluster is highlighted. The
// user avatar sits at the bottom (replaces the old in-sidebar UserStrip).
export function ClusterRail() {
  const { cluster: currentCluster, resource } = useParams();
  const { data, isLoading } = useClusters();
  const navigate = useNavigate();

  const clusters = data?.clusters ?? [];

  return (
    <aside className="flex h-full w-16 shrink-0 flex-col items-center border-r border-border bg-surface-2/40 py-3">
      <div className="flex flex-1 flex-col items-center gap-2 overflow-y-auto px-1">
        {isLoading
          ? Array.from({ length: 2 }).map((_, i) => (
              <div
                key={i}
                className="size-9 shrink-0 rounded-lg border border-border bg-surface-2/40"
              />
            ))
          : clusters.map((c) => (
              <ClusterPill
                key={c.name}
                cluster={c}
                active={c.name === currentCluster}
                onClick={() =>
                  navigate(`/clusters/${c.name}/${resource ?? "pods"}`)
                }
              />
            ))}
      </div>
      <div className="shrink-0 pt-2">
        <UserMenu />
      </div>
    </aside>
  );
}

function ClusterPill({
  cluster,
  active,
  onClick,
}: {
  cluster: Cluster;
  active: boolean;
  onClick: () => void;
}) {
  const isEks = cluster.backend === "eks" || cluster.backend === undefined;
  const subtitle =
    cluster.backend === "kubeconfig"
      ? `kubeconfig · ${cluster.kubeconfigContext ?? "default"}`
      : cluster.region
        ? `eks · ${cluster.region}`
        : "eks";

  return (
    <button
      type="button"
      onClick={onClick}
      title={`${cluster.name}\n${subtitle}`}
      aria-label={`Switch to cluster ${cluster.name}`}
      className={cn(
        "flex size-9 shrink-0 items-center justify-center rounded-lg border font-mono text-[10px] font-semibold transition-colors",
        active
          ? "border-accent bg-accent-soft text-accent"
          : isEks
            ? "border-border bg-surface text-ink-muted hover:border-accent/60 hover:text-accent"
            : "border-border bg-surface text-ink-muted hover:border-border-strong hover:text-ink",
      )}
    >
      {clusterInitials(cluster.name)}
    </button>
  );
}


// Pulls 2–3 letters out of a cluster name for the pill label. Hyphen/dot/
// underscore-segmented names use the leading char of each segment (so
// "prod-us-east-1" → "PUE"); single-token names use the first three
// characters ("staging" → "STA"). Always uppercase.
function clusterInitials(name: string): string {
  const segments = name.split(/[-_.]/).filter(Boolean);
  if (segments.length >= 2) {
    return segments
      .slice(0, 3)
      .map((s) => s[0]?.toUpperCase() ?? "")
      .join("");
  }
  return name.slice(0, 3).toUpperCase();
}

