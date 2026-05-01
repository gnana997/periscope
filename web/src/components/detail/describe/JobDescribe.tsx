import { useJobDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import type { JobStatus } from "../../../lib/types";
import { DetailError, DetailLoading } from "../states";
import {
  ConditionList,
  KV,
  MetaPills,
  PodRow,
  SectionTitle,
  StatStrip,
  type StatTone,
} from "./shared";

function statusTone(s: JobStatus): StatTone {
  switch (s) {
    case "Complete":
      return "green";
    case "Failed":
      return "red";
    case "Running":
      return "yellow";
    case "Suspended":
      return "muted";
    default:
      return "neutral";
  }
}


export function JobDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useJobDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const completionsTone: StatTone =
    data.status === "Failed"
      ? "red"
      : data.status === "Running"
        ? "yellow"
        : data.status === "Complete"
          ? "green"
          : "neutral";

  return (
    <div>
      <StatStrip
        stats={[
          {
            label: "Status",
            value: data.status,
            tone: statusTone(data.status),
            family: "sans",
          },
          {
            label: "Completions",
            value: data.completions,
            tone: completionsTone,
            family: "mono",
          },
          { label: "Parallelism", value: String(data.parallelism) },
          { label: "Duration", value: data.duration ?? "—", family: "mono" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        <dl className="space-y-2">
          <KV label="Active">{data.active}</KV>
          <KV label="Succeeded">{data.succeeded}</KV>
          <KV label="Failed">
            <span className={data.failed > 0 ? "text-red" : "text-ink"}>
              {data.failed}
            </span>
          </KV>
          <KV label="Backoff Limit">{data.backoffLimit}</KV>
          {data.suspend && (
            <KV label="Suspend">
              <span className="text-yellow">true</span>
            </KV>
          )}
          {data.startTime && (
            <KV label="Start Time" mono>
              {new Date(data.startTime).toLocaleString()}
            </KV>
          )}
          {data.completionTime && (
            <KV label="Completed" mono>
              {new Date(data.completionTime).toLocaleString()}
            </KV>
          )}
        </dl>

        {data.selector && Object.keys(data.selector).length > 0 && (
          <>
            <SectionTitle>Selector</SectionTitle>
            <MetaPills map={data.selector} />
          </>
        )}

        {data.conditions && data.conditions.length > 0 && (
          <>
            <SectionTitle>Conditions</SectionTitle>
            <ConditionList items={data.conditions} />
          </>
        )}

        <SectionTitle>Pods</SectionTitle>
        {data.pods.length === 0 ? (
          <span className="text-[11.5px] text-ink-faint">none</span>
        ) : (
          <ul className="space-y-1.5">
            {data.pods.map((p) => (
              <PodRow key={p.name} cluster={cluster} ns={ns} pod={p} />
            ))}
          </ul>
        )}

        <SectionTitle>Containers (template)</SectionTitle>
        <ul className="space-y-2">
          {data.containers.map((c) => (
            <li
              key={c.name}
              className="rounded-md border border-border bg-surface-2/40 px-3 py-2"
            >
              <div className="font-mono text-[12.5px] font-medium text-ink">
                {c.name}
              </div>
              <div
                className="mt-1 truncate font-mono text-[11.5px] text-ink-muted"
                title={c.image}
              >
                {c.image}
              </div>
            </li>
          ))}
        </ul>

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}

