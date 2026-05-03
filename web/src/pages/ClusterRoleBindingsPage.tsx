import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { ClusterRoleBinding, ClusterRoleBindingList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { SplitPane } from "../components/page/SplitPane";
import { DataTable, type Column } from "../components/table/DataTable";
import { EmptyState, ErrorState, ForbiddenState, LoadingState } from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { ClusterRoleBindingDescribe } from "../components/detail/describe/ClusterRoleBindingDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { ResourceActions } from "../components/edit/ResourceActions";
import { cn } from "../lib/cn";

export function ClusterRoleBindingsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const selectedName = params.get("sel");
  const activeTab = params.get("tab") ?? "describe";

  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value === null || value === "") next.delete(key);
    else next.set(key, value);
    setParams(next, { replace: true });
  };

  const setMany = (updates: Record<string, string | null>) => {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(updates)) {
      if (value === null || value === "") next.delete(key);
      else next.set(key, value);
    }
    setParams(next, { replace: true });
  };

  const query = useResource({ cluster, resource: "clusterrolebindings" });
  const all = useMemo<ClusterRoleBinding[]>(() => (query.data as ClusterRoleBindingList | undefined)?.clusterRoleBindings ?? [], [query.data]);
  const filtered = useMemo(
    () => (search ? all.filter((r) => nameMatches(r.name, search)) : all),
    [all, search],
  );

  const columns: Column<ClusterRoleBinding>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (r) => r.name },
    { key: "roleRef", header: "role", weight: 2.5, cellClassName: "font-mono text-ink-muted", accessor: (r) => r.roleRef },
    { key: "subjects", header: "subjects", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => String(r.subjectCount) },
    { key: "age", header: "age", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => ageFrom(r.createdAt) },
  ];

  const editFlag = useEditorDirty(cluster, "clusterrolebindings", undefined, selectedName);

  const detail = selectedName ? (
    <DetailPane
      title={selectedName}
      subtitle="cluster-scoped"
      activeTab={activeTab}
      onTabChange={(id) => setParam("tab", id)}
      onClose={() => setMany({ sel: null, tab: null })}
      tabs={[
        { id: "describe", label: "describe", ready: true, content: <ClusterRoleBindingDescribe cluster={cluster} name={selectedName} /> },
        { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="clusterrolebindings" ns="" name={selectedName} />, dirty: editFlag.dirty },
      ]}
      actions={
        <ResourceActions
          cluster={cluster}
          source={{ kind: "builtin", yamlKind: "clusterrolebindings" }}
          namespace={null}
          name={selectedName}
          onDeleted={() => setParam("sel", null)}
        />
      }
    />
  ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="ClusterRoleBindings"
        subtitle={query.isSuccess ? `${all.length} ${all.length === 1 ? "clusterrolebinding" : "clusterrolebindings"}` : undefined}
      />
      <div className="flex items-center gap-2 border-b border-border bg-bg px-6 py-2.5">
        <div className="flex min-w-[240px] flex-1 items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-[12.5px] focus-within:border-border-strong">
          <svg width="13" height="13" viewBox="0 0 13 13" className="text-ink-faint" aria-hidden>
            <circle cx="5.5" cy="5.5" r="3.6" stroke="currentColor" strokeWidth="1.3" fill="none" />
            <path d="M8.3 8.3l2.4 2.4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
          <input
            value={search}
            onChange={(e) => setParam("q", e.target.value)}
            placeholder="filter by name"
            className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink outline-none placeholder:text-ink-faint"
          />
        </div>
        <div className={cn("ml-auto font-mono text-[11px] text-ink-muted tabular")}>
          {filtered.length}<span className="text-ink-faint"> / </span>{all.length}
        </div>
      </div>
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? <LoadingState resource="clusterrolebindings" /> :
          query.isError ? isForbidden(query.error) ? <ForbiddenState resource="clusterrolebindings" /> : isForbidden(query.error) ? <ForbiddenState resource="clusterrolebindings" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} /> :
          filtered.length === 0 ? <EmptyState resource="clusterrolebindings" namespace={null} /> :
          <DataTable<ClusterRoleBinding>
            columns={columns}
            rows={filtered}
            rowKey={(r) => r.name}
            onRowClick={(r) => setMany({ sel: r.name, tab: "describe" })}
            selectedKey={selectedName}
          />
        }
        right={detail}
      />
    </div>
  );
}
