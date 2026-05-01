import { Link } from "react-router-dom";
import { usePVCDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import {
  ConditionList,
  KV,
  MetaPills,
  SectionTitle,
  StatStrip,
} from "./shared";

function pvcStatusTone(status: string): "neutral" | "green" | "yellow" | "red" {
  switch (status) {
    case "Bound": return "green";
    case "Pending": return "yellow";
    case "Lost": return "red";
    default: return "neutral";
  }
}

export function PVCDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = usePVCDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const pvLink = data.volumeName
    ? `/clusters/${encodeURIComponent(cluster)}/pvs?sel=${encodeURIComponent(data.volumeName)}&tab=describe`
    : null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Status", value: data.status, tone: pvcStatusTone(data.status), family: "sans" },
          { label: "Capacity", value: data.capacity ?? "—", family: "mono" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        <dl className="space-y-2">
          {data.storageClass && <KV label="Storage class" mono>{data.storageClass}</KV>}
          <KV label="Access modes" mono>{data.accessModes.join(", ") || "—"}</KV>
          {data.volumeName && (
            <KV label="Bound PV" mono>
              {pvLink ? (
                <Link to={pvLink} className="text-accent hover:underline">
                  {data.volumeName}
                </Link>
              ) : (
                data.volumeName
              )}
            </KV>
          )}
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
