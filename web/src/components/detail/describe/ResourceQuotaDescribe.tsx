import { useResourceQuotaDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { SectionTitle, StatStrip } from "./shared";

export function ResourceQuotaDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useResourceQuotaDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const entries = Object.entries(data.items ?? {});

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Resources", value: String(entries.length) },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        {entries.length > 0 && (
          <>
            <SectionTitle>Quota</SectionTitle>
            <div className="overflow-x-auto">
              <table className="w-full border-collapse text-[11.5px]">
                <thead>
                  <tr className="border-b border-border text-left text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
                    <th className="pb-1.5 pr-4">Resource</th>
                    <th className="pb-1.5 pr-4 text-right">Used</th>
                    <th className="pb-1.5 text-right">Hard</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border/50">
                  {entries.map(([resource, entry]) => (
                    <tr key={resource}>
                      <td className="py-1.5 pr-4 font-mono text-ink">{resource}</td>
                      <td className="py-1.5 pr-4 text-right font-mono text-ink-muted">{entry.used}</td>
                      <td className="py-1.5 text-right font-mono text-ink-faint">{entry.hard}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
