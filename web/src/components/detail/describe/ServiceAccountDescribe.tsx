import { useServiceAccountDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { MetaPills, SectionTitle, StatStrip } from "./shared";

export function ServiceAccountDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useServiceAccountDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Secrets", value: String(data.secrets), family: "mono" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        {data.secretNames && data.secretNames.length > 0 && (
          <>
            <SectionTitle>Secrets</SectionTitle>
            <ul className="space-y-1">
              {data.secretNames.map((s) => (
                <li key={s} className="font-mono text-[12px] text-ink-muted">{s}</li>
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
