import { useNodeDetail, useNodeMetrics } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import type { NodeCondition, NodeTaint } from "../../../lib/types";
import { CircularGauge, CircularGaugeSkeleton } from "../../ui/CircularGauge";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function NodeDescribe({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useNodeDetail(cluster, name);
  const metrics = useNodeMetrics(cluster, name);

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const statusTone =
    data.status === "Ready" ? "green" : data.status === "NotReady" ? "red" : "muted";

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Status", value: data.status, tone: statusTone, family: "sans" },
          { label: "Roles", value: data.roles.join(", "), family: "sans" },
          { label: "CPU", value: data.cpuCapacity, family: "mono" },
          { label: "Memory", value: data.memoryCapacity, family: "mono" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4 space-y-5">
        {/* Resource usage gauges */}
        <div>
          <SectionTitle>Resource Usage</SectionTitle>
          {metrics.isLoading ? (
            <div className="flex justify-around pt-1">
              <CircularGaugeSkeleton label="CPU" />
              <CircularGaugeSkeleton label="Memory" />
            </div>
          ) : metrics.data?.available === false ? (
            <p className="text-[11.5px] text-ink-faint italic">
              metrics-server not available in this cluster
            </p>
          ) : metrics.data ? (
            <div className="flex justify-around pt-1">
              <CircularGauge
                percent={metrics.data.cpuPercent ?? null}
                label="CPU"
                usageLabel={metrics.data.cpuUsage ?? "—"}
                totalLabel={data.cpuAllocatable}
              />
              <CircularGauge
                percent={metrics.data.memoryPercent ?? null}
                label="Memory"
                usageLabel={metrics.data.memoryUsage ?? "—"}
                totalLabel={data.memoryAllocatable}
              />
            </div>
          ) : null}
        </div>

        {/* Node info */}
        <div>
          <SectionTitle>Node Info</SectionTitle>
          <dl className="space-y-2">
            <KV label="Internal IP">{data.internalIP || "—"}</KV>
            <KV label="OS Image">{data.nodeInfo.osImage}</KV>
            <KV label="Kernel">{data.nodeInfo.kernelVersion}</KV>
            <KV label="Runtime">{data.nodeInfo.containerRuntime}</KV>
            <KV label="Kubelet">{data.nodeInfo.kubeletVersion}</KV>
            <KV label="kube-proxy">{data.nodeInfo.kubeProxyVersion}</KV>
            <KV label="CPU Allocatable">{data.cpuAllocatable}</KV>
            <KV label="Mem Allocatable">{data.memoryAllocatable}</KV>
          </dl>
        </div>

        {/* Conditions */}
        {data.conditions.length > 0 && (
          <div>
            <SectionTitle>Conditions</SectionTitle>
            <NodeConditionList conditions={data.conditions} />
          </div>
        )}

        {/* Taints */}
        {data.taints && data.taints.length > 0 && (
          <div>
            <SectionTitle>Taints</SectionTitle>
            <TaintList taints={data.taints} />
          </div>
        )}

        {/* Labels */}
        <div>
          <SectionTitle>Labels</SectionTitle>
          <MetaPills map={data.labels} />
        </div>

        {/* Annotations */}
        <div>
          <SectionTitle>Annotations</SectionTitle>
          <MetaPills map={data.annotations} />
        </div>
      </div>
    </div>
  );
}

// Conditions have inverted semantics for pressure types:
// Ready=True is healthy; MemoryPressure=True is a problem.
const PRESSURE_CONDITIONS = new Set([
  "MemoryPressure",
  "DiskPressure",
  "PIDPressure",
  "NetworkUnavailable",
]);

function NodeConditionList({ conditions }: { conditions: NodeCondition[] }) {
  // Show Ready first, then the rest sorted
  const sorted = [...conditions].sort((a, b) => {
    if (a.type === "Ready") return -1;
    if (b.type === "Ready") return 1;
    return a.type.localeCompare(b.type);
  });

  return (
    <ul className="space-y-1.5">
      {sorted.map((c) => {
        const isPressure = PRESSURE_CONDITIONS.has(c.type);
        // For pressure conditions: True is bad, False is good.
        // For Ready: True is good, False/Unknown is bad.
        const isHealthy = isPressure
          ? c.status === "False"
          : c.status === "True";
        const dotColor = isHealthy ? "bg-green" : c.status === "Unknown" ? "bg-ink-faint" : "bg-red";
        const textColor = isHealthy ? "text-green" : c.status === "Unknown" ? "text-ink-muted" : "text-red";

        return (
          <li key={c.type}>
            <div className="flex items-baseline gap-2 text-[12.5px]">
              <span className={`mt-[3px] block size-1.5 shrink-0 self-center rounded-full ${dotColor}`} />
              <span className="text-ink">{c.type}</span>
              {c.reason && (
                <span className="text-ink-muted">· {c.reason}</span>
              )}
              <span className={`ml-auto font-mono text-[12px] ${textColor}`}>
                {c.status}
              </span>
            </div>
            {c.message && (
              <div className="ml-3.5 mt-0.5 break-words text-[11.5px] leading-relaxed text-ink-muted">
                {c.message}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function TaintList({ taints }: { taints: NodeTaint[] }) {
  return (
    <ul className="space-y-1">
      {taints.map((t, i) => {
        const key = t.value ? `${t.key}=${t.value}` : t.key;
        const effectColor =
          t.effect === "NoExecute"
            ? "text-red"
            : t.effect === "NoSchedule"
              ? "text-yellow"
              : "text-ink-muted";
        return (
          <li key={i} className="flex items-baseline gap-2 text-[12.5px]">
            <span className="font-mono text-ink">{key}</span>
            <span className={`ml-auto font-mono text-[12px] ${effectColor}`}>
              {t.effect}
            </span>
          </li>
        );
      })}
    </ul>
  );
}
