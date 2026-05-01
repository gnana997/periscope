import { useRoleDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import type { PolicyRule } from "../../../lib/types";
import { DetailError, DetailLoading } from "../states";
import { MetaPills, SectionTitle, StatStrip } from "./shared";

export function RoleDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useRoleDetail(cluster, ns, name);

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Rules", value: String(data.ruleCount), family: "mono" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        <SectionTitle>Rules</SectionTitle>
        <PolicyRuleTable rules={data.rules} />

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}

export function PolicyRuleTable({ rules }: { rules: PolicyRule[] }) {
  if (rules.length === 0) {
    return <p className="text-[11.5px] text-ink-faint italic">No rules defined.</p>;
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-[11.5px]">
        <thead>
          <tr className="border-b border-border text-left text-[10px] font-medium uppercase tracking-[0.08em] text-ink-faint">
            <th className="pb-1.5 pr-4">Verbs</th>
            <th className="pb-1.5 pr-4">Resources</th>
            <th className="pb-1.5 pr-4">API Groups</th>
            <th className="pb-1.5">Names / URLs</th>
          </tr>
        </thead>
        <tbody className="divide-y divide-border/50">
          {rules.map((r, i) => (
            <tr key={i} className="align-top">
              <td className="py-1.5 pr-4 font-mono text-ink">
                {(r.verbs ?? []).join(", ") || "*"}
              </td>
              <td className="py-1.5 pr-4 font-mono text-ink-muted">
                {(r.resources ?? r.nonResourceURLs ?? []).join(", ") || "—"}
              </td>
              <td className="py-1.5 pr-4 font-mono text-ink-faint">
                {(r.apiGroups ?? []).map((g) => g === "" ? '""' : g).join(", ") || "—"}
              </td>
              <td className="py-1.5 font-mono text-ink-faint">
                {(r.resourceNames ?? []).join(", ") || "—"}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
