import { usePDBDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip, type StatTone } from "./shared";

export function PDBDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = usePDBDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const allowedTone: StatTone = data.disruptionsAllowed > 0 ? "green" : "yellow";

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Healthy", value: `${data.currentHealthy} / ${data.expectedPods}` },
          { label: "Desired", value: String(data.desiredHealthy) },
          { label: "Allowed", value: String(data.disruptionsAllowed), tone: allowedTone },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        <dl className="space-y-2">
          <KV label="Min Available" mono>{data.minAvailable}</KV>
          <KV label="Max Unavailable" mono>{data.maxUnavailable}</KV>
          <KV label="Selector" mono>{data.selector || "—"}</KV>
        </dl>

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}
