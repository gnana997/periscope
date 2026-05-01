import { useDeploymentDetail } from "../../../hooks/useResource";
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

export function DeploymentDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useDeploymentDetail(
    cluster,
    ns,
    name,
  );

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const replicasTone: StatTone =
    data.replicas > 0 && data.readyReplicas === 0
      ? "red"
      : data.readyReplicas < data.replicas
        ? "yellow"
        : "neutral";

  const availableTone: StatTone =
    data.replicas > 0 && data.availableReplicas === 0
      ? "red"
      : data.availableReplicas < data.replicas
        ? "yellow"
        : "neutral";

  return (
    <div>
      <StatStrip
        stats={[
          {
            label: "Replicas",
            value: `${data.readyReplicas} / ${data.replicas}`,
            tone: replicasTone,
          },
          { label: "Updated", value: String(data.updatedReplicas) },
          {
            label: "Available",
            value: String(data.availableReplicas),
            tone: availableTone,
          },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        <dl className="space-y-2">
          <KV label="Strategy">{data.strategy}</KV>
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
