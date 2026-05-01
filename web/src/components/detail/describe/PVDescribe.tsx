import { Link } from "react-router-dom";
import { usePVDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

function pvStatusTone(status: string): "neutral" | "green" | "yellow" | "red" | "muted" {
  switch (status) {
    case "Available": return "green";
    case "Bound": return "neutral";
    case "Released": return "yellow";
    case "Failed": return "red";
    default: return "muted";
  }
}

export function PVDescribe({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = usePVDetail(cluster, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const pvcLink =
    data.claimRef
      ? `/clusters/${encodeURIComponent(cluster)}/pvcs` +
        `?sel=${encodeURIComponent(data.claimRef.name)}` +
        `&selNs=${encodeURIComponent(data.claimRef.namespace)}&tab=describe`
      : null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Status", value: data.status, tone: pvStatusTone(data.status), family: "sans" },
          { label: "Capacity", value: data.capacity ?? "—", family: "mono" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        <dl className="space-y-2">
          {data.storageClass && <KV label="Storage class" mono>{data.storageClass}</KV>}
          <KV label="Access modes" mono>{data.accessModes.join(", ") || "—"}</KV>
          {data.reclaimPolicy && <KV label="Reclaim policy">{data.reclaimPolicy}</KV>}
          {data.volumeMode && <KV label="Volume mode">{data.volumeMode}</KV>}
          {data.source && <KV label="Source" mono>{data.source}</KV>}
          {data.claimRef && (
            <KV label="Bound claim" mono>
              {pvcLink ? (
                <Link to={pvcLink} className="text-accent hover:underline">
                  {data.claimRef.namespace}/{data.claimRef.name}
                </Link>
              ) : (
                `${data.claimRef.namespace}/${data.claimRef.name}`
              )}
            </KV>
          )}
        </dl>

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}
