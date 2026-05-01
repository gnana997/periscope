import { useReplicaSetDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { ConditionList, KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function ReplicaSetDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useReplicaSetDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Ready", value: `${data.ready} / ${data.desired}` },
          { label: "Current", value: String(data.current) },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        <dl className="space-y-2">
          {data.owner && <KV label="Owner" mono>{data.owner}</KV>}
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

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}
