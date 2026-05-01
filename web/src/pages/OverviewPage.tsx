import { useNavigate } from "react-router-dom";
import { useClusterSummary, useClusterEvents } from "../hooks/useResource";
import { CircularGauge, CircularGaugeSkeleton } from "../components/ui/CircularGauge";
import { ThemeToggle } from "../components/shell/ThemeToggle";
import { ageFrom } from "../lib/format";
import { cn } from "../lib/cn";
import type {
  ClusterEvent,
  ClusterSummary,
  FailingPod,
  PodPhaseCounts,
  TopPod,
  WorkloadCount,
  WorkloadCounts,
} from "../lib/types";

const INSTALL_CMD =
  "kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml";

/**
 * Overview is the operator's landing page for a cluster. It surfaces
 * the seven sections defined for the v1.x redesign:
 *
 *   E. Cluster identity banner       — first thing the eye lands on
 *   - CPU + memory gauges            — capacity at a glance
 *   B. Pod phase distribution        — single biggest health signal
 *   A. Workload breakdown            — "what's running"
 *   C. Needs attention               — failing pods, click → pod detail
 *   D. Top consumers (CPU + mem)     — top-5 each, click → pod detail
 *   F. Recent events                 — Warnings prioritized
 *   G. Storage snapshot              — PV / PVC counts + total size
 *
 * Every card surfaces an actionable click-through where it makes sense
 * (workload counts → filtered list view, failing pod → pod detail,
 * etc.) so the page is a navigation hub, not just a readout.
 */
export function OverviewPage({ cluster }: { cluster: string }) {
  const { data, isLoading, isError, error } = useClusterSummary(cluster);
  const events = useClusterEvents(cluster);
  const navigate = useNavigate();

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <span className="block size-4 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent" />
      </div>
    );
  }
  if (isError) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-[13px] text-red">
          {(error as Error)?.message ?? "Failed to load cluster overview"}
        </p>
      </div>
    );
  }
  if (!data) return null;

  const goTo = (path: string, query?: Record<string, string>) => {
    const qs = query ? "?" + new URLSearchParams(query).toString() : "";
    navigate(`/clusters/${encodeURIComponent(cluster)}/${path}${qs}`);
  };

  return (
    <div className="flex h-full min-h-0 flex-col overflow-y-auto">
      {/* E. Cluster identity banner ---------------------------------- */}
      <ClusterIdentityBanner cluster={cluster} data={data} />

      <div className="space-y-6 px-6 py-5">
        {/* Capacity row: CPU gauge / Memory gauge / Pod phase chart -- */}
        <section className="grid grid-cols-1 gap-4 lg:grid-cols-3">
          <Card title="CPU">
            {data.metricsAvailable === false ? (
              <MetricsNudgeInline />
            ) : data.metricsAvailable && data.cpuPercent != null ? (
              <CircularGauge
                percent={data.cpuPercent ?? null}
                label=""
                usageLabel={data.cpuUsed ?? "—"}
                totalLabel={data.cpuAllocatable}
              />
            ) : (
              <CircularGaugeSkeleton label="" />
            )}
          </Card>
          <Card title="Memory">
            {data.metricsAvailable === false ? (
              <MetricsNudgeInline />
            ) : data.metricsAvailable && data.memoryPercent != null ? (
              <CircularGauge
                percent={data.memoryPercent ?? null}
                label=""
                usageLabel={data.memoryUsed ?? "—"}
                totalLabel={data.memoryAllocatable}
              />
            ) : (
              <CircularGaugeSkeleton label="" />
            )}
          </Card>
          {/* B. Pod phase distribution */}
          <Card title="Pods">
            <PodPhaseChart
              phases={data.podPhases}
              total={data.podCount}
              onSlice={(phase) =>
                goTo("pods", phase ? { status: phase } : {})
              }
            />
          </Card>
        </section>

        {/* A. Workload breakdown -------------------------------------- */}
        <section>
          <SectionTitle>Workloads</SectionTitle>
          <WorkloadBreakdown
            workloads={data.workloads}
            onCard={(route) => goTo(route)}
          />
        </section>

        {/* C. Needs attention ---------------------------------------- */}
        {data.needsAttention.length > 0 && (
          <section>
            <SectionTitle tone="red">
              Needs attention
              <span className="ml-2 font-mono text-[10.5px] text-ink-faint">
                {data.needsAttention.length}{" "}
                {data.needsAttention.length === 1 ? "pod" : "pods"}
              </span>
            </SectionTitle>
            <NeedsAttentionList
              items={data.needsAttention}
              onPick={(p) =>
                goTo("pods", {
                  selNs: p.namespace,
                  sel: p.name,
                  tab: "describe",
                })
              }
            />
          </section>
        )}

        {/* D. Top consumers ------------------------------------------- */}
        {data.metricsAvailable !== false && (
          <section className="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <Card title="Top by CPU">
              <TopPodList
                items={data.topByCpu ?? []}
                onPick={(p) =>
                  goTo("pods", {
                    selNs: p.namespace,
                    sel: p.name,
                    tab: "describe",
                  })
                }
                empty="no pod metrics"
              />
            </Card>
            <Card title="Top by Memory">
              <TopPodList
                items={data.topByMemory ?? []}
                onPick={(p) =>
                  goTo("pods", {
                    selNs: p.namespace,
                    sel: p.name,
                    tab: "describe",
                  })
                }
                empty="no pod metrics"
              />
            </Card>
          </section>
        )}

        {/* G. Storage snapshot ---------------------------------------- */}
        <section>
          <SectionTitle>Storage</SectionTitle>
          <StorageSnapshot
            data={data}
            onPVs={() => goTo("pvs")}
            onPVCs={() => goTo("pvcs")}
          />
        </section>

        {/* F. Recent events ------------------------------------------- */}
        <section>
          <SectionTitle>Recent events</SectionTitle>
          {events.isLoading ? (
            <p className="text-[12px] italic text-ink-faint">
              Loading events…
            </p>
          ) : events.isError ? (
            <p className="text-[12px] text-red">Failed to load events.</p>
          ) : !events.data?.events?.length ? (
            <p className="text-[12px] italic text-ink-faint">No events.</p>
          ) : (
            <EventsTable events={events.data.events.slice(0, 20)} />
          )}
        </section>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// E. Cluster identity banner
// ---------------------------------------------------------------------

function ClusterIdentityBanner({
  cluster,
  data,
}: {
  cluster: string;
  data: ClusterSummary;
}) {
  const nodesReady = data.nodeReadyCount === data.nodeCount;
  return (
    <div className="sticky top-0 z-20 flex items-start justify-between border-b border-border bg-bg/80 px-6 py-5 backdrop-blur-md">
      <div className="min-w-0 flex-1">
        <h1 className="font-display text-[28px] leading-none tracking-[-0.01em] text-ink">
          {cluster}
        </h1>
        <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-[12px] text-ink-faint">
          <span>{data.kubernetesVersion}</span>
          <span>·</span>
          <span>{data.provider}</span>
          <span>·</span>
          <span>
            <span
              className={cn(
                "tabular-nums",
                nodesReady ? "text-green" : "text-red",
              )}
            >
              {data.nodeReadyCount}
            </span>
            <span className="text-ink-faint">/{data.nodeCount}</span> nodes
          </span>
          <span>·</span>
          <span>
            <span className="tabular-nums text-ink-muted">
              {data.cpuAllocatable}
            </span>{" "}
            CPU
          </span>
          <span>·</span>
          <span>
            <span className="tabular-nums text-ink-muted">
              {data.memoryAllocatable}
            </span>{" "}
            memory
          </span>
          <span>·</span>
          <span>
            <span className="tabular-nums text-ink-muted">
              {data.namespaceCount}
            </span>{" "}
            namespaces
          </span>
        </div>
      </div>
      <ThemeToggle />
    </div>
  );
}

// ---------------------------------------------------------------------
// B. Pod phase chart
// ---------------------------------------------------------------------

const PHASE_COLORS: Record<keyof PodPhaseCounts, string> = {
  running: "bg-green",
  pending: "bg-yellow",
  succeeded: "bg-ink-faint/60",
  failed: "bg-red",
  unknown: "bg-ink-faint/40",
  stuck: "bg-red",
};

function PodPhaseChart({
  phases,
  total,
  onSlice,
}: {
  phases: PodPhaseCounts;
  total: number;
  onSlice: (status: string | null) => void;
}) {
  const order: (keyof PodPhaseCounts)[] = [
    "running",
    "pending",
    "stuck",
    "failed",
    "succeeded",
    "unknown",
  ];
  const present = order.filter((k) => phases[k] > 0);
  if (total === 0) {
    return (
      <div className="flex h-[140px] items-center justify-center font-mono text-[11px] text-ink-faint">
        no pods
      </div>
    );
  }
  const phaseToFilter = (k: keyof PodPhaseCounts): string | null => {
    if (k === "running") return "Running";
    if (k === "pending") return "Pending";
    if (k === "failed" || k === "stuck") return "Failed";
    return null;
  };
  return (
    <div className="flex flex-col gap-3 py-2">
      <div className="flex items-baseline gap-2">
        <span className="font-display text-[36px] leading-none text-ink tabular-nums">
          {total}
        </span>
        <span className="font-mono text-[10.5px] uppercase tracking-[0.08em] text-ink-faint">
          total
        </span>
      </div>
      <div className="flex h-2 w-full overflow-hidden rounded-full bg-surface-2">
        {present.map((k) => (
          <button
            key={k}
            onClick={() => onSlice(phaseToFilter(k))}
            className={cn(
              "h-full transition-opacity hover:opacity-80",
              PHASE_COLORS[k],
            )}
            style={{ width: `${(phases[k] / total) * 100}%` }}
            title={`${phases[k]} ${k}`}
          />
        ))}
      </div>
      <div className="grid grid-cols-2 gap-x-3 gap-y-1 font-mono text-[10.5px]">
        {present.map((k) => (
          <button
            key={k}
            onClick={() => onSlice(phaseToFilter(k))}
            className="flex items-center gap-1.5 text-left text-ink-muted transition-colors hover:text-ink"
          >
            <span
              aria-hidden
              className={cn("block size-1.5 rounded-full", PHASE_COLORS[k])}
            />
            <span className="tabular-nums text-ink">{phases[k]}</span>
            <span>{k}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// A. Workload breakdown
// ---------------------------------------------------------------------

const WORKLOAD_LABELS: { key: keyof WorkloadCounts; label: string; route: string }[] = [
  { key: "deployments", label: "deployments", route: "deployments" },
  { key: "statefulSets", label: "statefulsets", route: "statefulsets" },
  { key: "daemonSets", label: "daemonsets", route: "daemonsets" },
  { key: "jobs", label: "jobs", route: "jobs" },
  { key: "cronJobs", label: "cronjobs", route: "cronjobs" },
];

function WorkloadBreakdown({
  workloads,
  onCard,
}: {
  workloads: WorkloadCounts;
  onCard: (route: string) => void;
}) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
      {WORKLOAD_LABELS.map(({ key, label, route }) => {
        const c: WorkloadCount = workloads[key];
        const allHealthy = c.total === 0 || c.healthy === c.total;
        return (
          <button
            key={key}
            onClick={() => onCard(route)}
            className="flex flex-col items-start rounded-md border border-border bg-surface px-3 py-2.5 text-left transition-colors hover:border-border-strong hover:bg-surface-2/40"
          >
            <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
              {label}
            </span>
            <div className="mt-1 flex items-baseline gap-1.5">
              <span className="font-display text-[24px] leading-none text-ink tabular-nums">
                {c.total}
              </span>
              {c.total > 0 && (
                <span
                  className={cn(
                    "font-mono text-[10.5px] tabular-nums",
                    allHealthy ? "text-green" : "text-yellow",
                  )}
                  title={`${c.healthy}/${c.total} healthy`}
                >
                  {c.healthy}/{c.total}
                </span>
              )}
            </div>
          </button>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------
// C. Needs attention list
// ---------------------------------------------------------------------

function NeedsAttentionList({
  items,
  onPick,
}: {
  items: FailingPod[];
  onPick: (p: FailingPod) => void;
}) {
  return (
    <ul className="overflow-hidden rounded-md border border-border">
      {items.map((p, i) => (
        <li
          key={`${p.namespace}/${p.name}`}
          className={cn(
            i > 0 && "border-t border-border",
            "bg-surface transition-colors hover:bg-red-soft/40",
          )}
        >
          <button
            onClick={() => onPick(p)}
            className="flex w-full items-center gap-3 px-3 py-2 text-left"
          >
            <span aria-hidden className="block size-1.5 shrink-0 rounded-full bg-red" />
            <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-ink">
              {p.name}
            </span>
            <span className="shrink-0 font-mono text-[10.5px] text-ink-faint">
              {p.namespace}
            </span>
            <span className="shrink-0 font-mono text-[10.5px] text-red">
              {p.reason}
            </span>
            {p.restartCount && p.restartCount > 0 ? (
              <span
                className="shrink-0 font-mono text-[10.5px] tabular-nums text-ink-muted"
                title="restart count"
              >
                ↻ {p.restartCount}
              </span>
            ) : null}
          </button>
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------
// D. Top consumers
// ---------------------------------------------------------------------

function TopPodList({
  items,
  onPick,
  empty,
}: {
  items: TopPod[];
  onPick: (p: TopPod) => void;
  empty: string;
}) {
  if (items.length === 0) {
    return (
      <p className="py-2 text-center font-mono text-[11px] text-ink-faint">
        {empty}
      </p>
    );
  }
  return (
    <ul className="space-y-1">
      {items.map((p) => (
        <li key={`${p.namespace}/${p.name}`}>
          <button
            onClick={() => onPick(p)}
            className="flex w-full items-center gap-2 rounded px-2 py-1 text-left transition-colors hover:bg-surface-2/60"
          >
            <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-ink">
              {p.name}
            </span>
            <span className="shrink-0 font-mono text-[10.5px] text-ink-faint">
              {p.namespace}
            </span>
            <span className="w-16 shrink-0 text-right font-mono text-[11px] tabular-nums text-ink-muted">
              {p.usage}
            </span>
            {p.percent != null && p.percent >= 0 && (
              <span
                className={cn(
                  "w-12 shrink-0 text-right font-mono text-[10.5px] tabular-nums",
                  p.percent > 90
                    ? "text-red"
                    : p.percent > 70
                      ? "text-yellow"
                      : "text-ink-faint",
                )}
                title={
                  p.ofLimit
                    ? "percent of pod limit"
                    : "percent of cluster allocatable"
                }
              >
                {p.percent.toFixed(0)}%
              </span>
            )}
          </button>
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------
// G. Storage snapshot
// ---------------------------------------------------------------------

function StorageSnapshot({
  data,
  onPVs,
  onPVCs,
}: {
  data: ClusterSummary;
  onPVs: () => void;
  onPVCs: () => void;
}) {
  const s = data.storage;
  return (
    <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
      <button
        onClick={onPVs}
        className="rounded-md border border-border bg-surface px-3 py-2.5 text-left transition-colors hover:border-border-strong hover:bg-surface-2/40"
      >
        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          PVs
        </div>
        <div className="mt-1 font-display text-[24px] leading-none text-ink tabular-nums">
          {s.pvCount}
        </div>
      </button>
      <button
        onClick={onPVCs}
        className="rounded-md border border-border bg-surface px-3 py-2.5 text-left transition-colors hover:border-border-strong hover:bg-surface-2/40"
      >
        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          PVCs bound
        </div>
        <div className="mt-1 font-display text-[24px] leading-none text-ink tabular-nums">
          {s.pvcBound}
        </div>
      </button>
      <button
        onClick={onPVCs}
        className={cn(
          "rounded-md border border-border bg-surface px-3 py-2.5 text-left transition-colors hover:border-border-strong hover:bg-surface-2/40",
          s.pvcPending > 0 && "border-yellow/40",
        )}
      >
        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          PVCs pending
        </div>
        <div
          className={cn(
            "mt-1 font-display text-[24px] leading-none tabular-nums",
            s.pvcPending > 0 ? "text-yellow" : "text-ink",
          )}
        >
          {s.pvcPending}
        </div>
      </button>
      <div className="rounded-md border border-border bg-surface px-3 py-2.5">
        <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
          provisioned
        </div>
        <div className="mt-1 font-display text-[24px] leading-none text-ink tabular-nums">
          {s.totalProvisioned ?? "—"}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Misc helpers
// ---------------------------------------------------------------------

function SectionTitle({
  children,
  tone,
}: {
  children: React.ReactNode;
  tone?: "red";
}) {
  return (
    <h2
      className={cn(
        "mb-3 flex items-center text-[10px] font-medium uppercase tracking-[0.08em]",
        tone === "red" ? "text-red" : "text-ink-faint",
      )}
    >
      {children}
    </h2>
  );
}

function Card({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="rounded-md border border-border bg-surface px-4 py-3">
      <div className="font-mono text-[10px] uppercase tracking-[0.08em] text-ink-faint">
        {title}
      </div>
      <div className="mt-1">{children}</div>
    </div>
  );
}

function MetricsNudgeInline() {
  return (
    <div className="py-3 font-mono text-[10.5px] text-ink-faint">
      metrics-server not found —{" "}
      <code
        className="select-all whitespace-nowrap rounded bg-surface-2 px-1 text-ink-muted"
        title={INSTALL_CMD}
      >
        kubectl apply -f …
      </code>
    </div>
  );
}

function EventsTable({ events }: { events: ClusterEvent[] }) {
  // Warnings first, then by recency. The slice is already trimmed to
  // 20 by the caller.
  const sorted = [...events].sort((a, b) => {
    if (a.type !== b.type) return a.type === "Warning" ? -1 : 1;
    return (b.last ?? "").localeCompare(a.last ?? "");
  });
  return (
    <div className="overflow-hidden rounded-md border border-border bg-surface">
      <table className="w-full text-left">
        <thead>
          <tr className="border-b border-border bg-surface-2/40 text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
            <th className="px-3 py-1.5">Type</th>
            <th className="px-3 py-1.5">Object</th>
            <th className="px-3 py-1.5">Reason</th>
            <th className="px-3 py-1.5">Message</th>
            <th className="px-3 py-1.5 text-right">Count</th>
            <th className="px-3 py-1.5 text-right">Last</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border/50">
          {sorted.map((e, i) => (
            <tr
              key={i}
              className={cn(
                "align-top",
                e.type === "Warning" && "bg-yellow-soft/30",
              )}
            >
              <td className="px-3 py-1.5">
                <span
                  className={cn(
                    "font-mono text-[11px]",
                    e.type === "Warning" ? "text-yellow" : "text-ink-faint",
                  )}
                >
                  {e.type === "Warning" ? "⚠" : "·"} {e.type}
                </span>
              </td>
              <td className="px-3 py-1.5">
                <span className="font-mono text-[11px] text-ink-muted">
                  {e.kind}/{e.name}
                </span>
                {e.namespace && (
                  <span className="ml-1 font-mono text-[10px] text-ink-faint">
                    ({e.namespace})
                  </span>
                )}
              </td>
              <td className="px-3 py-1.5 font-mono text-[11px] text-ink-muted">
                {e.reason}
              </td>
              <td className="max-w-[280px] px-3 py-1.5 text-[11.5px] text-ink-muted">
                <span className="line-clamp-2">{e.message}</span>
              </td>
              <td className="px-3 py-1.5 text-right font-mono text-[11px] text-ink-faint">
                {e.count}
              </td>
              <td className="px-3 py-1.5 text-right font-mono text-[11px] text-ink-faint">
                {ageFrom(e.last)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
