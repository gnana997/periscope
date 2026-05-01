import { usePriorityClassDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function PriorityClassDescribe({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = usePriorityClassDetail(cluster, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Value", value: String(data.value), family: "mono" },
          { label: "Global Default", value: data.globalDefault ? "Yes" : "No", tone: data.globalDefault ? "green" : "muted" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        <dl className="space-y-2">
          <KV label="Preemption" mono>{data.preemptionPolicy}</KV>
          {data.description && <KV label="Description">{data.description}</KV>}
        </dl>

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}
