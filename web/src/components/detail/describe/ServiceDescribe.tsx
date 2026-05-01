import { useServiceDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function ServiceDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useServiceDetail(
    cluster,
    ns,
    name,
  );

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Type", value: data.type, family: "sans" },
          { label: "Ports", value: String(data.ports.length) },
          {
            label: "External",
            value: data.externalIP || "—",
            family: data.externalIP ? "mono" : "display",
            tone: data.externalIP ? "neutral" : "muted",
          },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        <dl className="space-y-2">
          {data.clusterIP && <KV label="Cluster IP" mono>{data.clusterIP}</KV>}
          {data.sessionAffinity && (
            <KV label="Affinity">{data.sessionAffinity}</KV>
          )}
        </dl>

        <SectionTitle>Ports</SectionTitle>
        {data.ports.length === 0 ? (
          <span className="text-[11.5px] text-ink-faint">—</span>
        ) : (
          <ul className="space-y-1">
            {data.ports.map((p, i) => (
              <li
                key={i}
                className="flex items-center gap-3 rounded-md border border-border bg-surface-2/40 px-3 py-1.5 font-mono text-[12px]"
              >
                {p.name && <span className="text-ink-muted">{p.name}</span>}
                <span className="text-ink">{p.port}</span>
                <span className="text-ink-faint">→</span>
                <span className="text-ink">{p.targetPort}</span>
                <span className="ml-auto text-ink-muted">{p.protocol}</span>
                {p.nodePort ? (
                  <span className="text-ink-muted">node:{p.nodePort}</span>
                ) : null}
              </li>
            ))}
          </ul>
        )}

        {data.selector && Object.keys(data.selector).length > 0 && (
          <>
            <SectionTitle>Selector</SectionTitle>
            <MetaPills map={data.selector} />
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
