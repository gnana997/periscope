import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import { ageFrom, nameMatches } from "../lib/format";
import type { StorageClass, StorageClassList } from "../lib/types";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import { DataTable, type Column } from "../components/table/DataTable";
import { DetailPane } from "../components/detail/DetailPane";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { ResourceActions } from "../components/edit/ResourceActions";
import { EventsView } from "../components/detail/EventsView";
import { StorageClassDescribe } from "../components/detail/describe/StorageClassDescribe";
import { EmptyState, ErrorState, ForbiddenState, LoadingState } from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";

export function StorageClassesPage({ cluster }: { cluster: string }) {
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

  const query = useResource({ cluster, resource: "storageclasses" });
  const all = useMemo<StorageClass[]>(() => (query.data as StorageClassList | undefined)?.storageClasses ?? [], [query.data]);

  const filtered = useMemo(
    () => (search ? all.filter((s) => nameMatches(s.name, search)) : all),
    [all, search],
  );

  const columns: Column<StorageClass>[] = [
    { key: "name", header: "name", weight: 2.5, cellClassName: "font-mono text-ink", accessor: (s) => s.name },
    { key: "provisioner", header: "provisioner", weight: 2, cellClassName: "font-mono text-ink-muted", accessor: (s) => s.provisioner },
    { key: "reclaim", header: "reclaim", weight: 0.8, cellClassName: "font-mono text-ink-muted", accessor: (s) => s.reclaimPolicy ?? "—" },
    { key: "binding", header: "binding mode", weight: 1.2, cellClassName: "font-mono text-ink-muted", accessor: (s) => s.volumeBindingMode ?? "—" },
    { key: "expansion", header: "expansion", weight: 0.6, cellClassName: "font-mono text-ink-muted", accessor: (s) => s.allowVolumeExpansion ? "✓" : "—" },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (s) => ageFrom(s.createdAt) },
  ];

  const editFlag = useEditorDirty(cluster, "storageclasses", undefined, selectedName);

  const detail = selectedName ? (
    <DetailPane
      title={selectedName}
      subtitle="cluster-scoped"
      activeTab={activeTab}
      onTabChange={(id) => setParam("tab", id)}
      onClose={() => setMany({ sel: null, tab: null })}
      tabs={[
        { id: "describe", label: "describe", ready: true, content: <StorageClassDescribe cluster={cluster} name={selectedName} /> },
        { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="storageclasses" ns="" name={selectedName} />, dirty: editFlag.dirty },
        { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="storageclasses" ns="" name={selectedName} /> },
      ]}
      actions={
        <ResourceActions
          cluster={cluster}
          source={{ kind: "builtin", yamlKind: "storageclasses" }}
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
        title="StorageClasses"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "storageclass" : "storageclasses"}`
            : undefined
        }
      />
      <FilterStrip
        search={search}
        onSearch={(v) => setParam("q", v)}
        resultCount={filtered.length}
        totalCount={all.length}
      />
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? (
            <LoadingState resource="storageclasses" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="storageclasses" /> : isForbidden(query.error) ? <ForbiddenState resource="storageclasses" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="storageclasses" namespace={null} />
          ) : (
            <DataTable<StorageClass>
              columns={columns}
              rows={filtered}
              rowKey={(s) => s.name}
              onRowClick={(s) => setMany({ sel: s.name, tab: "describe" })}
              selectedKey={selectedName}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
