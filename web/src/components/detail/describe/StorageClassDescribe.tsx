import { useStorageClassDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function StorageClassDescribe({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useStorageClassDetail(cluster, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Provisioner", value: data.provisioner, family: "mono" },
          { label: "Reclaim", value: data.reclaimPolicy ?? "—", family: "sans" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        <dl className="space-y-2">
          {data.volumeBindingMode && (
            <KV label="Binding mode">{data.volumeBindingMode}</KV>
          )}
          <KV label="Allow expansion">
            {data.allowVolumeExpansion ? "yes" : "no"}
          </KV>
        </dl>

        {data.parameters && Object.keys(data.parameters).length > 0 && (
          <>
            <SectionTitle>Parameters</SectionTitle>
            <MetaPills map={data.parameters} />
          </>
        )}

        {data.mountOptions && data.mountOptions.length > 0 && (
          <>
            <SectionTitle>Mount options</SectionTitle>
            <ul className="space-y-1">
              {data.mountOptions.map((opt) => (
                <li
                  key={opt}
                  className="rounded-md border border-border bg-surface-2/40 px-3 py-1.5 font-mono text-[12px] text-ink"
                >
                  {opt}
                </li>
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
