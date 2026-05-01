import { useNetworkPolicyDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import type { NetworkPolicyRule } from "../../../lib/types";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function NetworkPolicyDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useNetworkPolicyDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError) return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Types", value: data.policyTypes.join(" + ") || "—" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        <dl className="space-y-2">
          <KV label="Pod Selector" mono>{data.podSelector || "<all pods>"}</KV>
        </dl>

        {data.ingressRules && data.ingressRules.length > 0 && (
          <>
            <SectionTitle>Ingress Rules</SectionTitle>
            <PolicyRulesTable rules={data.ingressRules} />
          </>
        )}

        {data.egressRules && data.egressRules.length > 0 && (
          <>
            <SectionTitle>Egress Rules</SectionTitle>
            <PolicyRulesTable rules={data.egressRules} />
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

function PolicyRulesTable({ rules }: { rules: NetworkPolicyRule[] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-[11.5px]">
        <thead>
          <tr className="border-b border-border text-left text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
            <th className="pb-1.5 pr-4">Ports</th>
            <th className="pb-1.5">Peers</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border/50">
          {rules.map((r, i) => (
            <tr key={i} className="align-top">
              <td className="py-1.5 pr-4 font-mono text-ink-muted">
                {r.ports.length > 0 ? r.ports.join(", ") : "<any>"}
              </td>
              <td className="py-1.5 font-mono text-ink-muted">
                {r.peers.length > 0 ? r.peers.join(", ") : "<any>"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
