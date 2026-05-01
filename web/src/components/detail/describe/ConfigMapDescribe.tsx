import { useConfigMapDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { MetaPills, SectionTitle, StatStrip } from "./shared";

export function ConfigMapDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useConfigMapDetail(
    cluster,
    ns,
    name,
  );

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const dataEntries = data.data ? Object.entries(data.data) : [];
  const binaryCount = data.binaryDataKeys?.length ?? 0;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Keys", value: String(data.keyCount) },
          { label: "Binary", value: String(binaryCount), tone: binaryCount === 0 ? "muted" : "neutral" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        {dataEntries.length > 0 && (
          <>
            <SectionTitle>Data</SectionTitle>
            <dl className="space-y-3">
              {dataEntries.map(([k, v]) => (
                <div key={k}>
                  <dt className="mb-1 font-mono text-[12px] font-medium text-ink">
                    {k}
                  </dt>
                  <dd>
                    <pre className="overflow-x-auto rounded-md border border-border bg-surface-2/60 px-3 py-2 font-mono text-[11.5px] leading-relaxed text-ink">
                      {v}
                    </pre>
                  </dd>
                </div>
              ))}
            </dl>
          </>
        )}

        {data.binaryDataKeys && data.binaryDataKeys.length > 0 && (
          <>
            <SectionTitle>Binary data keys</SectionTitle>
            <ul className="space-y-1 font-mono text-[11.5px] text-ink-muted">
              {data.binaryDataKeys.map((k) => (
                <li key={k}>· {k}</li>
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
