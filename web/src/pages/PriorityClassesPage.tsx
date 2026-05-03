import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { PriorityClass, PriorityClassList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { cn } from "../lib/cn";
import { PageHeader } from "../components/page/PageHeader";
import { SplitPane } from "../components/page/SplitPane";
import { DataTable, type Column } from "../components/table/DataTable";
import { EmptyState, ErrorState, ForbiddenState, LoadingState } from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { PriorityClassDescribe } from "../components/detail/describe/PriorityClassDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { ResourceActions } from "../components/edit/ResourceActions";

export function PriorityClassesPage({ cluster }: { cluster: string }) {
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

  const query = useResource({ cluster, resource: "priorityclasses" });
  const all = useMemo<PriorityClass[]>(() => (query.data as PriorityClassList | undefined)?.priorityClasses ?? [], [query.data]);
  const filtered = useMemo(
    () => (search ? all.filter((r) => nameMatches(r.name, search)) : all),
    [all, search],
  );

  const columns: Column<PriorityClass>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (r) => r.name },
    { key: "value", header: "value", weight: 1, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => String(r.value) },
    {
      key: "globalDefault",
      header: "global default",
      weight: 1,
      align: "right",
      accessor: (r) => (
        <span className={cn("font-mono text-[11px]", r.globalDefault ? "text-green" : "text-ink-faint")}>
          {r.globalDefault ? "✓" : "—"}
        </span>
      ),
    },
    { key: "preemptionPolicy", header: "preemption", weight: 1.5, cellClassName: "font-mono text-ink-muted", accessor: (r) => r.preemptionPolicy },
    { key: "age", header: "age", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => ageFrom(r.createdAt) },
  ];

  const editFlag = useEditorDirty(cluster, "priorityclasses", undefined, selectedName);

  const detail = selectedName ? (
    <DetailPane
      title={selectedName}
      subtitle="cluster-scoped"
      activeTab={activeTab}
      onTabChange={(id) => setParam("tab", id)}
      onClose={() => setMany({ sel: null, tab: null })}
      tabs={[
        { id: "describe", label: "describe", ready: true, content: <PriorityClassDescribe cluster={cluster} name={selectedName} /> },
        { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="priorityclasses" ns="" name={selectedName} />, dirty: editFlag.dirty },
      ]}
      actions={
        <ResourceActions
          cluster={cluster}
          source={{ kind: "builtin", yamlKind: "priorityclasses" }}
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
        title="PriorityClasses"
        subtitle={query.isSuccess ? `${all.length} ${all.length === 1 ? "priorityclass" : "priorityclasses"}` : undefined}
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
          query.isLoading ? <LoadingState resource="priorityclasses" /> :
          query.isError ? isForbidden(query.error) ? <ForbiddenState resource="priorityclasses" /> : isForbidden(query.error) ? <ForbiddenState resource="priorityclasses" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} /> :
          filtered.length === 0 ? <EmptyState resource="priorityclasses" namespace={null} /> :
          <DataTable<PriorityClass>
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
