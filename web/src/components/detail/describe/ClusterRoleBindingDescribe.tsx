import { useClusterRoleBindingDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";
import { SubjectList } from "./RoleBindingDescribe";

export function ClusterRoleBindingDescribe({
  cluster,
  name,
}: {
  cluster: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useClusterRoleBindingDetail(cluster, name);

  if (isLoading) return <DetailLoading />;
  if (isError)
    return <DetailError message={(error as Error)?.message ?? "unknown"} />;
  if (!data) return null;

  return (
    <div>
      <StatStrip
        stats={[
          { label: "Subjects", value: String(data.subjects.length), family: "mono" },
          { label: "Age", value: ageFrom(data.createdAt), tone: "muted" },
        ]}
      />
      <div className="px-5 py-4">
        <SectionTitle>Role Ref</SectionTitle>
        <dl className="space-y-2">
          <KV label="Kind" mono>{data.roleRef.kind}</KV>
          <KV label="Name" mono>{data.roleRef.name}</KV>
        </dl>

        <SectionTitle>Subjects</SectionTitle>
        <SubjectList subjects={data.subjects} />

        <SectionTitle>Labels</SectionTitle>
        <MetaPills map={data.labels} />

        <SectionTitle>Annotations</SectionTitle>
        <MetaPills map={data.annotations} />
      </div>
    </div>
  );
}
