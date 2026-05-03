import { Link, useParams } from "react-router-dom";
import { useEndpointSliceDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import type {
  EndpointSliceEndpoint,
  EndpointSlicePort,
} from "../../../lib/types";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function EndpointSliceDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const params = useParams<{ cluster?: string }>();
  const clusterName = params.cluster ?? cluster;
  const { data, isLoading, isError, error } = useEndpointSliceDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  const serviceLink = data.serviceName ? (
    // ServicesPage uses ?ns=<namespace>&sel=<name> for selection. Linking
    // straight in opens the parent service's detail panel without a
    // round-trip through the list page.
    <Link
      to={`/clusters/${encodeURIComponent(clusterName)}/services?ns=${encodeURIComponent(data.namespace)}&sel=${encodeURIComponent(data.serviceName)}&selNs=${encodeURIComponent(data.namespace)}&tab=describe`}
      className="text-accent hover:underline"
    >
      {data.serviceName}
    </Link>
  ) : null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Address Type", value: data.addressType },
          { label: "Endpoints", value: `${data.readyCount}/${data.totalCount} ready` },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        <dl className="space-y-2">
          {serviceLink && <KV label="Service">{serviceLink}</KV>}
        </dl>

        {data.ports.length > 0 && (
          <>
            <SectionTitle>Ports</SectionTitle>
            <PortsTable ports={data.ports} />
          </>
        )}

        {data.endpoints && data.endpoints.length > 0 && (
          <>
            <SectionTitle>Endpoints</SectionTitle>
            <EndpointsTable endpoints={data.endpoints} />
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

function PortsTable({ ports }: { ports: EndpointSlicePort[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-[11.5px]">
        <thead>
          <tr className="border-b border-border text-left text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
            <th className="pb-1.5 pr-4">Name</th>
            <th className="pb-1.5 pr-4">Port</th>
            <th className="pb-1.5 pr-4">Protocol</th>
            <th className="pb-1.5">App Protocol</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border/50">
          {ports.map((p, i) => (
            <tr key={i} className="align-top">
              <td className="py-1.5 pr-4 font-mono text-ink-muted">{p.name || "—"}</td>
              <td className="py-1.5 pr-4 font-mono text-ink-muted">{p.port}</td>
              <td className="py-1.5 pr-4 font-mono text-ink-muted">{p.protocol || "—"}</td>
              <td className="py-1.5 font-mono text-ink-muted">{p.appProtocol || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function EndpointsTable({ endpoints }: { endpoints: EndpointSliceEndpoint[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-[11.5px]">
        <thead>
          <tr className="border-b border-border text-left text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
            <th className="pb-1.5 pr-4">Address</th>
            <th className="pb-1.5 pr-4">Conditions</th>
            <th className="pb-1.5 pr-4">Target</th>
            <th className="pb-1.5 pr-4">Node</th>
            <th className="pb-1.5">Zone</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border/50">
          {endpoints.map((e, i) => {
            const conds: string[] = [];
            if (e.ready) conds.push("ready");
            if (e.serving) conds.push("serving");
            if (e.terminating) conds.push("terminating");
            const target = e.targetRef
              ? `${e.targetRef.kind}/${e.targetRef.name}`
              : "—";
            return (
              <tr key={i} className="align-top">
                <td className="py-1.5 pr-4 font-mono text-ink-muted">
                  {e.addresses.join(", ")}
                  {e.hostname ? <span className="ml-1 text-ink-faint">({e.hostname})</span> : null}
                </td>
                <td className="py-1.5 pr-4 font-mono text-ink-muted">
                  {conds.length > 0 ? conds.join(" + ") : "—"}
                </td>
                <td className="py-1.5 pr-4 font-mono text-ink-muted">{target}</td>
                <td className="py-1.5 pr-4 font-mono text-ink-muted">{e.nodeName || "—"}</td>
                <td className="py-1.5 font-mono text-ink-muted">{e.zone || "—"}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
