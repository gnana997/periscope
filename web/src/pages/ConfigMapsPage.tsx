import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { ConfigMap, ConfigMapList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import { DataTable, type Column } from "../components/table/DataTable";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "../components/table/states";
import { DetailPane } from "../components/detail/DetailPane";
import { ConfigMapDescribe } from "../components/detail/describe/ConfigMapDescribe";
import { YamlView } from "../components/detail/YamlView";
import { EventsView } from "../components/detail/EventsView";
import { NamespacePicker } from "../components/shell/NamespacePicker";

export function ConfigMapsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const namespace = params.get("ns");
  const search = params.get("q") ?? "";
  const selectedNs = params.get("selNs");
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

  const query = useResource({
    cluster,
    resource: "configmaps",
    namespace: namespace ?? undefined,
  });
  const all =
    ((query.data as ConfigMapList | undefined)?.configMaps ?? []) as ConfigMap[];
  const filtered = useMemo(
    () => (search ? all.filter((c) => nameMatches(c.name, search)) : all),
    [all, search],
  );

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<ConfigMap>[] = [
    { key: "name", header: "name", weight: 3.5, cellClassName: "font-mono text-ink", accessor: (c) => c.name },
    { key: "namespace", header: "namespace", weight: 1.4, cellClassName: "font-mono text-ink-muted", accessor: (c) => c.namespace },
    { key: "keys", header: "keys", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (c) => c.keyCount },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (c) => ageFrom(c.createdAt) },
  ];

  const detail =
    selectedNs && selectedName ? (
      <DetailPane
        title={selectedName}
        subtitle={selectedNs}
        activeTab={activeTab}
        onTabChange={(id) => setParam("tab", id)}
        onClose={() => setMany({ sel: null, selNs: null, tab: null })}
        tabs={[
          { id: "describe", label: "describe", ready: true, content: <ConfigMapDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="configmaps" ns={selectedNs} name={selectedName} /> },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="configmaps" ns={selectedNs} name={selectedName} /> },
        ]}
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="ConfigMaps"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "configmap" : "configmaps"}${
                namespace ? ` in ${namespace}` : ""
              }`
            : undefined
        }
        trailing={<NamespacePicker />}
      />
      <FilterStrip
        search={search}
        onSearch={(v) => setParam("q", v)}
        resultCount={filtered.length}
        totalCount={all.length}
      />
      <SplitPane
        storageKey="periscope.detailWidth"
        left={
          query.isLoading ? (
            <LoadingState resource="configmaps" />
          ) : query.isError ? (
            <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="configmaps" namespace={namespace} />
          ) : (
            <DataTable<ConfigMap>
              columns={columns}
              rows={filtered}
              rowKey={(c) => `${c.namespace}/${c.name}`}
              onRowClick={(c) => setMany({ sel: c.name, selNs: c.namespace, tab: "describe" })}
              selectedKey={selectedKey}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
