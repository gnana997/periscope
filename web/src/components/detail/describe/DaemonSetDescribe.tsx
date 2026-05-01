import { useDaemonSetDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import {
  ConditionList,
  KV,
  MetaPills,
  SectionTitle,
  StatStrip,
  type StatTone,
} from "./shared";

export function DaemonSetDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useDaemonSetDetail(
    cluster,
    ns,
    name,
  );

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const readyTone: StatTone =
    data.desiredNumberScheduled > 0 && data.numberReady === 0
      ? "red"
      : data.numberReady < data.desiredNumberScheduled
        ? "yellow"
        : "neutral";

  const availableTone: StatTone =
    data.desiredNumberScheduled > 0 &&
    data.numberAvailable < data.desiredNumberScheduled
      ? "yellow"
      : "neutral";

  return (
    <div>
      <StatStrip
        stats={[
          {
            label: "Ready",
            value: `${data.numberReady} / ${data.desiredNumberScheduled}`,
            tone: readyTone,
          },
          {
            label: "Up-to-date",
            value: String(data.updatedNumberScheduled),
          },
          {
            label: "Available",
            value: String(data.numberAvailable),
            tone: availableTone,
          },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        {data.numberMisscheduled > 0 && (
          <div className="mb-3 rounded-md border border-yellow/40 bg-yellow-soft px-3 py-2 text-[12px] text-yellow">
            <span className="font-medium">
              {data.numberMisscheduled} misscheduled
            </span>
            <span className="ml-1 text-ink-muted">
              · pods running on nodes that don't match the daemonset's selector
            </span>
          </div>
        )}

        <dl className="space-y-2">
          <KV label="Update strategy">{data.updateStrategy}</KV>
        </dl>

        {data.selector && Object.keys(data.selector).length > 0 && (
          <>
            <SectionTitle>Selector</SectionTitle>
            <MetaPills map={data.selector} />
          </>
        )}

        {data.nodeSelector && Object.keys(data.nodeSelector).length > 0 && (
          <>
            <SectionTitle>Node selector</SectionTitle>
            <MetaPills map={data.nodeSelector} />
          </>
        )}

        {data.conditions && data.conditions.length > 0 && (
          <>
            <SectionTitle>Conditions</SectionTitle>
            <ConditionList items={data.conditions} />
          </>
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
