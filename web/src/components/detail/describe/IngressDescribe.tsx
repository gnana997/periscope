import { Link } from "react-router-dom";
import { useIngressDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function IngressDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useIngressDetail(
    cluster,
    ns,
    name,
  );

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const totalPaths = data.rules.reduce((sum, r) => sum + r.paths.length, 0);

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Class", value: data.class || "—", family: "sans" },
          { label: "Hosts", value: String(data.hosts.length) },
          { label: "Paths", value: String(totalPaths) },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />

      <div className="px-5 py-4">
        {data.address && (
          <dl className="space-y-2">
            <KV label="Address" mono>
              {data.address}
            </KV>
          </dl>
        )}

        <SectionTitle>Rules</SectionTitle>
        {data.rules.length === 0 ? (
          <span className="text-[11.5px] text-ink-faint">—</span>
        ) : (
          <ul className="space-y-3">
            {data.rules.map((r, i) => (
              <li
                key={i}
                className="rounded-md border border-border bg-surface-2/40 px-3 py-2"
              >
                <div className="mb-1.5 font-mono text-[12.5px] font-medium text-ink">
                  {r.host || (
                    <span className="italic text-ink-muted">(catch-all)</span>
                  )}
                </div>
                {r.paths.length === 0 ? (
                  <div className="text-[11.5px] text-ink-faint">no paths</div>
                ) : (
                  <ul className="space-y-1">
                    {r.paths.map((p, j) => (
                      <li
                        key={j}
                        className="flex items-center gap-2 font-mono text-[12px]"
                      >
                        <span className="text-ink">{p.path}</span>
                        <span
                          className="rounded border border-border px-1 py-px text-[9.5px] uppercase tracking-[0.04em] text-ink-muted"
                          title={p.pathType}
                        >
                          {p.pathType}
                        </span>
                        <span className="text-ink-faint">→</span>
                        <Link
                          to={
                            `/clusters/${encodeURIComponent(cluster)}/services` +
                            `?sel=${encodeURIComponent(p.backend.serviceName)}` +
                            `&selNs=${encodeURIComponent(ns)}&tab=describe`
                          }
                          className="text-ink hover:text-accent hover:underline"
                        >
                          {p.backend.serviceName}
                        </Link>
                        {p.backend.servicePort && (
                          <span className="text-ink">
                            <span className="text-ink-faint">:</span>
                            {p.backend.servicePort}
                          </span>
                        )}
                      </li>
                    ))}
                  </ul>
                )}
              </li>
            ))}
          </ul>
        )}

        {data.tls && data.tls.length > 0 && (
          <>
            <SectionTitle>TLS</SectionTitle>
            <ul className="space-y-1.5">
              {data.tls.map((t, i) => (
                <li
                  key={i}
                  className="rounded-md border border-border bg-surface-2/40 px-3 py-1.5 font-mono text-[12px]"
                >
                  <span className="text-ink">
                    {t.hosts.length > 0 ? t.hosts.join(", ") : (
                      <span className="italic text-ink-muted">(no hosts)</span>
                    )}
                  </span>
                  {t.secretName && (
                    <>
                      <span className="text-ink-faint"> → </span>
                      <span className="text-ink-muted">{t.secretName}</span>
                    </>
                  )}
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
