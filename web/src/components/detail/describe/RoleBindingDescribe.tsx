import { useRoleBindingDetail } from "../../../hooks/useResource";
import { ageFrom } from "../../../lib/format";
import type { RBACSubject } from "../../../lib/types";
import { DetailError, DetailLoading } from "../states";
import { KV, MetaPills, SectionTitle, StatStrip } from "./shared";

export function RoleBindingDescribe({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const { data, isLoading, isError, error } = useRoleBindingDetail(cluster, ns, name);

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

export function SubjectList({ subjects }: { subjects: RBACSubject[] }) {
  if (subjects.length === 0) {
    return <p className="text-[11.5px] text-ink-faint italic">No subjects.</p>;
  }
  return (
    <ul className="space-y-1.5">
      {subjects.map((s, i) => {
        const kindColor =
          s.kind === "User"
            ? "text-accent"
            : s.kind === "Group"
              ? "text-yellow"
              : "text-ink-muted";
        return (
          <li
            key={i}
            className="flex items-baseline gap-2 rounded-md border border-border bg-surface-2/40 px-3 py-1.5 text-[12.5px]"
          >
            <span className={`font-mono text-[11px] ${kindColor}`}>{s.kind}</span>
            <span className="font-mono text-ink">{s.name}</span>
            {s.namespace && (
              <span className="ml-auto font-mono text-[11px] text-ink-faint">{s.namespace}</span>
            )}
          </li>
        );
      })}
    </ul>
  );
}
