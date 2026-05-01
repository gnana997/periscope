import { useRuntimeClassDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function RuntimeClassDescribe({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useRuntimeClassDetail(cluster, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Handler", value: data.handler, family: "mono" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        <dl className="space-y-2">
          {data.cpuOverhead && <KV label="CPU Overhead" mono>{data.cpuOverhead}</KV>}
          {data.memoryOverhead && <KV label="Mem Overhead" mono>{data.memoryOverhead}</KV>}
        </dl>

        {data.nodeSelector && Object.keys(data.nodeSelector).length > 0 && (
          <>
            <SectionTitle>Node Selector</SectionTitle>
            <MetaPills map={data.nodeSelector} />
          </>
        )}

        {data.tolerations && data.tolerations.length > 0 && (
          <>
            <SectionTitle>Tolerations</SectionTitle>
            <ul className="space-y-1">
              {data.tolerations.map((t, i) => (
                <li key={i} className="font-mono text-[11.5px] text-ink-muted">{t}</li>
              ))}
            </ul>
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
