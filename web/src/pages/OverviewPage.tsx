import { useClusterSummary, useClusterEvents } from "../hooks/useResource";
import { ageFrom } from "../lib/format";
import { CircularGauge, CircularGaugeSkeleton } from "../components/ui/CircularGauge";
import { ThemeToggle } from "../components/shell/ThemeToggle";
import { cn } from "../lib/cn";

const INSTALL_CMD =
  "kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml";

export function OverviewPage({ cluster }: { cluster: string }) {
  const { data, isLoading, isError, error } = useClusterSummary(cluster);
  const events = useClusterEvents(cluster);

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
        <p className="text-[13px] text-red">{(error as Error)?.message ?? "Failed to load cluster overview"}</p>
      </div>
    );
  }

  if (!data) return null;

  const nodesReady = data.nodeReadyCount === data.nodeCount;

  return (
    <div className="flex h-full min-h-0 flex-col overflow-y-auto">
      {/* Cluster identity header */}
      <div className="flex items-start justify-between border-b border-border px-6 py-5">
        <div>
          <h1 className="font-display text-[28px] leading-none tracking-[-0.01em] text-ink">
            {cluster}
          </h1>
          <div className="mt-2 flex items-center gap-3 font-mono text-[12px] text-ink-faint">
            <span>{data.kubernetesVersion}</span>
            <span>·</span>
            <span>{data.provider}</span>
          </div>
        </div>
        <ThemeToggle />
      </div>

      <div className="space-y-6 px-6 py-5">
        {/* Hero stats */}
        <div className="grid grid-cols-3 gap-3">
          <StatCard
            label="Nodes"
            value={`${data.nodeReadyCount} / ${data.nodeCount}`}
            tone={nodesReady ? "green" : "red"}
            sub="ready"
          />
          <StatCard label="Pods" value={String(data.podCount)} sub="running" />
          <StatCard label="Namespaces" value={String(data.namespaceCount)} />
        </div>

        {/* Resource usage */}
        <section>
          <SectionTitle>Resource Usage</SectionTitle>
          {data.metricsAvailable === false ? (
            <MetricsNudge />
          ) : !data.metricsAvailable && !data.cpuUsed ? (
            // Still loading metrics (metricsAvailable not yet determined)
            <div className="flex justify-around py-2">
              <CircularGaugeSkeleton label="CPU" />
              <CircularGaugeSkeleton label="Memory" />
            </div>
          ) : (
            <div className="flex justify-around py-2">
              <CircularGauge
                percent={data.cpuPercent ?? null}
                label="CPU"
                usageLabel={data.cpuUsed ?? "—"}
                totalLabel={data.cpuAllocatable}
              />
              <CircularGauge
                percent={data.memoryPercent ?? null}
                label="Memory"
                usageLabel={data.memoryUsed ?? "—"}
                totalLabel={data.memoryAllocatable}
              />
            </div>
          )}
          {/* Always show allocatable totals */}
          <div className="mt-3 flex justify-center gap-6 font-mono text-[11.5px] text-ink-faint">
            <span>CPU allocatable <span className="text-ink-muted">{data.cpuAllocatable}</span></span>
            <span>Memory allocatable <span className="text-ink-muted">{data.memoryAllocatable}</span></span>
          </div>
        </section>

        {/* Recent events */}
        <section>
          <SectionTitle>Recent Events</SectionTitle>
          {events.isLoading ? (
            <p className="text-[12px] text-ink-faint italic">Loading events…</p>
          ) : events.isError ? (
            <p className="text-[12px] text-red">Failed to load events.</p>
          ) : !events.data?.events?.length ? (
            <p className="text-[12px] text-ink-faint italic">No events.</p>
          ) : (
            <EventsTable events={events.data.events.slice(0, 20)} />
          )}
        </section>
      </div>
    </div>
  );
}

// ---- Sub-components ----

function SectionTitle({ children }: { children: React.ReactNode }) {
  return (
    <h2 className="mb-3 text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
      {children}
    </h2>
  );
}

function StatCard({
  label,
  value,
  tone,
  sub,
}: {
  label: string;
  value: string;
  tone?: "green" | "red";
  sub?: string;
}) {
  return (
    <div className="rounded-md border border-border bg-surface-2/40 px-4 py-3">
      <div className="mb-1 text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
        {label}
      </div>
      <div
        className={cn(
          "font-display text-[28px] leading-none tracking-[-0.01em]",
          tone === "green" ? "text-green" : tone === "red" ? "text-red" : "text-ink",
        )}
      >
        {value}
      </div>
      {sub && <div className="mt-1 text-[11px] text-ink-faint">{sub}</div>}
    </div>
  );
}

function MetricsNudge() {
  return (
    <div className="rounded-md border border-border bg-surface-2/40 px-4 py-3">
      <div className="flex items-start gap-3">
        <span className="mt-px shrink-0 text-[14px] text-ink-faint">ⓘ</span>
        <div className="min-w-0">
          <p className="text-[12.5px] text-ink-muted">
            <span className="font-medium text-ink">metrics-server</span> is not installed in this cluster.
            Install it to see CPU and memory usage.
          </p>
          <div className="mt-2 flex items-center gap-2 rounded-md border border-border bg-bg px-3 py-1.5">
            <code className="min-w-0 flex-1 truncate font-mono text-[11px] text-ink-muted">
              {INSTALL_CMD}
            </code>
            <button
              type="button"
              onClick={() => navigator.clipboard.writeText(INSTALL_CMD)}
              className="shrink-0 rounded px-1.5 py-0.5 text-[10px] text-ink-faint transition-colors hover:bg-surface-2 hover:text-ink-muted"
              title="Copy to clipboard"
            >
              copy
            </button>
          </div>
          <p className="mt-1.5 text-[11px] text-ink-faint">
            Note: kind clusters also need <code className="font-mono">--kubelet-insecure-tls</code> — see the metrics-server docs.
          </p>
        </div>
      </div>
    </div>
  );
}

function EventsTable({
  events,
}: {
  events: Array<{
    namespace: string;
    kind: string;
    name: string;
    type: string;
    reason: string;
    message: string;
    count: number;
    last: string;
  }>;
}) {
  return (
    <div className="overflow-x-auto rounded-md border border-border">
      <table className="w-full border-collapse text-[12px]">
        <thead>
          <tr className="border-b border-border bg-surface-2/40 text-left text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
            <th className="px-3 py-2">Type</th>
            <th className="px-3 py-2">Object</th>
            <th className="px-3 py-2">Reason</th>
            <th className="px-3 py-2">Message</th>
            <th className="px-3 py-2 text-right">Count</th>
            <th className="px-3 py-2 text-right">Age</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border/50">
          {events.map((e, i) => (
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
