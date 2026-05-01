import { useClusterRoleDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";
import { PolicyRuleTable } from "./RoleDescribe";

export function ClusterRoleDescribe({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useClusterRoleDetail(cluster, name);

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
        {data.aggregationLabels && data.aggregationLabels.length > 0 && (
          <>
            <SectionTitle>Aggregation</SectionTitle>
            <dl className="space-y-2">
              {data.aggregationLabels.map((sel, i) => (
                <KV key={i} label={`Selector ${i + 1}`} mono>{sel}</KV>
              ))}
            </dl>
          </>
        )}

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
