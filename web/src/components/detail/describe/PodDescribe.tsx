import { usePodDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { cn } from "../../../lib/cn";
import { StatusDot } from "../../table/StatusDot";
import { DetailError, DetailLoading } from "../states";
import {
  ConditionList,
  KV,
  MetaPills,
  SectionTitle,
  StatStrip,
  phaseStatTone,
  readyStatTone,
  restartStatTone,
} from "./shared";
import type { ContainerStatus } from "../../../lib/types";

export function PodDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = usePodDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Phase", value: data.phase, tone: phaseStatTone(data.phase) },
          { label: "Ready", value: data.ready, tone: readyStatTone(data.ready) },
          { label: "Restarts", value: String(data.restarts), tone: restartStatTone(data.restarts) },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        <dl className="space-y-2">
          {data.qosClass && <KV label="QoS class">{data.qosClass}</KV>}
          {data.nodeName && <KV label="Node" mono>{data.nodeName}</KV>}
          {data.podIP && <KV label="Pod IP" mono>{data.podIP}</KV>}
          {data.hostIP && <KV label="Host IP" mono>{data.hostIP}</KV>}
        </dl>

        {data.conditions && data.conditions.length > 0 && (
          <>
            <SectionTitle>Conditions</SectionTitle>
            <ConditionList items={data.conditions} />
          </>
        )}

        {data.initContainers && data.initContainers.length > 0 && (
          <>
            <SectionTitle>Init containers</SectionTitle>
            <ContainerCardList items={data.initContainers} />
          </>
        )}

        <SectionTitle>Containers</SectionTitle>
        <ContainerCardList items={data.containers} />

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}

function ContainerCardList({ items }: { items: ContainerStatus[] }) {
  return (
    <ul className="space-y-2">
      {items.map((c) => {
        const stateTone =
          c.state === "Running"
            ? "green"
            : c.state === "Waiting"
              ? "yellow"
              : c.state === "Terminated"
                ? "red"
                : "muted";
        const stateColor =
          stateTone === "green"
            ? "text-green"
            : stateTone === "yellow"
              ? "text-yellow"
              : stateTone === "red"
                ? "text-red"
                : "text-ink-muted";
        return (
          <li
            key={c.name}
            className="rounded-md border border-border bg-surface-2/40 px-3 py-2"
          >
            <div className="flex items-center gap-2">
              <span className="font-mono text-[12.5px] font-medium text-ink">
                {c.name}
              </span>
              <span className="text-[11px] text-ink-faint">·</span>
              <span className="inline-flex items-center gap-1.5 text-[11.5px]">
                <StatusDot tone={stateTone} />
                <span className={stateColor}>{c.state}</span>
              </span>
              {c.reason && (
                <span className="text-[11.5px] text-ink-muted">· {c.reason}</span>
              )}
              <span
                className={cn(
                  "ml-auto font-mono text-[11.5px]",
                  c.restartCount > 5
                    ? "text-red"
                    : c.restartCount > 0
                      ? "text-yellow"
                      : "text-ink-faint",
                )}
              >
                ↻ {c.restartCount}
              </span>
            </div>
            <div
              className="mt-1 truncate font-mono text-[11.5px] text-ink-muted"
              title={c.image}
            >
              {c.image}
            </div>
            {c.message && (
              <div className="mt-1 break-words text-[11.5px] text-ink-muted">
                {c.message}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}
