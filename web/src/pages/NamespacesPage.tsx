import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { Namespace, NamespaceList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { SplitPane } from "../components/page/SplitPane";
import {
  DataTable,
  type Column,
  type RowTint,
} from "../components/table/DataTable";
import { PhaseTag } from "../components/table/StatusDot";
import {
  EmptyState,
  ErrorState,
  ForbiddenState,
  LoadingState,
} from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { NamespaceDescribe } from "../components/detail/describe/NamespaceDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { ResourceActions } from "../components/edit/ResourceActions";
import { EventsView } from "../components/detail/EventsView";
import { cn } from "../lib/cn";

export function NamespacesPage({ cluster }: { cluster: string }) {
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

  const query = useResource({ cluster, resource: "namespaces" });
  const all = useMemo<Namespace[]>(() => (query.data as NamespaceList | undefined)?.namespaces ?? [], [query.data]);
  const filtered = useMemo(
    () => (search ? all.filter((n) => nameMatches(n.name, search)) : all),
    [all, search],
  );

  const columns: Column<Namespace>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (n) => n.name },
    { key: "phase", header: "status", weight: 1.2, accessor: (n) => <PhaseTag phase={n.phase} /> },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (n) => ageFrom(n.createdAt) },
  ];

  const rowTint = (n: Namespace): RowTint =>
    n.phase === "Terminating" ? "yellow" : null;

  // Namespaces are cluster-scoped — pass empty ns string to YamlView/EventsView,
  // and the DescribeRouter calls useNamespaceDetail (no ns param).
  const editFlag = useEditorDirty(cluster, "namespaces", undefined, selectedName);

  const detail = selectedName ? (
    <DetailPane
      title={selectedName}
      subtitle="cluster-scoped"
      activeTab={activeTab}
      onTabChange={(id) => setParam("tab", id)}
      onClose={() => setMany({ sel: null, tab: null })}
      tabs={[
        { id: "describe", label: "describe", ready: true, content: <NamespaceDescribe cluster={cluster} name={selectedName} /> },
        { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="namespaces" ns="" name={selectedName} />, dirty: editFlag.dirty },
        { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="namespaces" ns="" name={selectedName} /> },
      ]}
      actions={
        <ResourceActions
          cluster={cluster}
          yamlKind="namespaces"
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
        title="Namespaces"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "namespace" : "namespaces"}`
            : undefined
        }
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
          {filtered.length}
          <span className="text-ink-faint"> / </span>
          {all.length}
        </div>
      </div>
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? (
            <LoadingState resource="namespaces" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="namespaces" /> : isForbidden(query.error) ? <ForbiddenState resource="namespaces" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="namespaces" namespace={null} />
          ) : (
            <DataTable<Namespace>
              columns={columns}
              rows={filtered}
              rowKey={(n) => n.name}
              rowTint={rowTint}
              onRowClick={(n) => setMany({ sel: n.name, tab: "describe" })}
              selectedKey={selectedName}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
