import { useLimitRangeDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import type { LimitRangeItem } from "../../../lib/types";
import { DetailError, DetailLoading } from "../states";
import { SectionTitle, StatStrip } from "./shared";

export function LimitRangeDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useLimitRangeDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Limits", value: String(data.limitCount) },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        {data.limits && data.limits.length > 0 && (
          <>
            <SectionTitle>Limit Items</SectionTitle>
            <div className="space-y-4">
              {data.limits.map((item, i) => (
                <LimitItemCard key={i} item={item} />
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function LimitItemCard({ item }: { item: LimitRangeItem }) {
  const sections: Array<{ label: string; map?: Record<string, string> }> = [
    { label: "Default", map: item.default },
    { label: "Default Request", map: item.defaultRequest },
    { label: "Max", map: item.max },
    { label: "Min", map: item.min },
    { label: "Max Limit/Request Ratio", map: item.maxLimitRequestRatio },
  ];

  return (
    <div className="rounded-md border border-border bg-surface-2/40 px-3 py-2.5">
      <div className="mb-2 font-mono text-[12px] font-medium text-ink">{item.type}</div>
      <table className="w-full border-collapse text-[11.5px]">
        <tbody>
          {sections.map(({ label, map }) => {
            if (!map || Object.keys(map).length === 0) return null;
            return (
              <tr key={label} className="align-top">
                <td className="pr-3 text-[10.5px] text-ink-faint">{label}</td>
                <td className="font-mono text-ink-muted">
                  {Object.entries(map).map(([k, v]) => `${k}: ${v}`).join(", ")}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
