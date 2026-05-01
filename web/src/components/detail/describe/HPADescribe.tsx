import { useHPADetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { ConditionList, KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function HPADescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useHPADetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const replicaTone = data.currentReplicas === data.desiredReplicas && data.ready ? "green" : "yellow";

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Current", value: String(data.currentReplicas), tone: replicaTone },
          { label: "Desired", value: String(data.desiredReplicas) },
          { label: "Min", value: String(data.minReplicas), tone: "muted" },
          { label: "Max", value: String(data.maxReplicas), tone: "muted" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        <dl className="space-y-2">
          <KV label="Target" mono>{data.target}</KV>
          <KV label="Ready">{data.ready ? "Yes" : "No"}</KV>
        </dl>

        {data.conditions && data.conditions.length > 0 && (
          <>
            <SectionTitle>Conditions</SectionTitle>
            <ConditionList items={data.conditions} />
          </>
        )}

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}
