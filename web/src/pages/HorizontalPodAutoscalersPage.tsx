import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { HPA, HPAList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { cn } from "../lib/cn";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import { DataTable, type Column } from "../components/table/DataTable";
import { EmptyState, ErrorState, ForbiddenState, LoadingState, isForbidden } from "../components/table/states";
import { DetailPane } from "../components/detail/DetailPane";
import { HPADescribe } from "../components/detail/describe/HPADescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { EventsView } from "../components/detail/EventsView";
import { NamespacePicker } from "../components/shell/NamespacePicker";

export function HorizontalPodAutoscalersPage({ cluster }: { cluster: string }) {
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

  const query = useResource({ cluster, resource: "horizontalpodautoscalers", namespace: namespace ?? undefined });
  const all = ((query.data as HPAList | undefined)?.hpas ?? []) as HPA[];
  const filtered = useMemo(
    () => (search ? all.filter((r) => nameMatches(r.name, search)) : all),
    [all, search],
  );

  const columns: Column<HPA>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (r) => r.name },
    { key: "namespace", header: "namespace", weight: 2, cellClassName: "font-mono text-ink-muted", accessor: (r) => r.namespace },
    { key: "target", header: "target", weight: 2, cellClassName: "font-mono text-ink-muted", accessor: (r) => r.target },
    { key: "minReplicas", header: "min", weight: 0.6, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => String(r.minReplicas) },
    { key: "maxReplicas", header: "max", weight: 0.6, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => String(r.maxReplicas) },
    { key: "current", header: "current", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => String(r.currentReplicas) },
    { key: "desired", header: "desired", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => String(r.desiredReplicas) },
    {
      key: "ready", header: "ready", weight: 0.7, align: "right",
      accessor: (r) => (
        <span className={cn("font-mono text-[11px]", r.ready ? "text-green" : "text-red")}>
          {r.ready ? "✓" : "✗"}
        </span>
      ),
    },
    { key: "age", header: "age", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => ageFrom(r.createdAt) },
  ];

  const selectedKey = selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const editFlag = useEditorDirty(cluster, "horizontalpodautoscalers", selectedNs ?? undefined, selectedName);

  const detail =
    selectedNs && selectedName ? (
      <DetailPane
        title={selectedName}
        subtitle={selectedNs}
        activeTab={activeTab}
        onTabChange={(id) => setParam("tab", id)}
        onClose={() => setMany({ sel: null, selNs: null, tab: null })}
        tabs={[
          { id: "describe", label: "describe", ready: true, content: <HPADescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="horizontalpodautoscalers" ns={selectedNs} name={selectedName} />, dirty: editFlag.dirty },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="horizontalpodautoscalers" ns={selectedNs} name={selectedName} /> },
        ]}
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="HorizontalPodAutoscalers"
        subtitle={query.isSuccess ? `${all.length} hpa${all.length === 1 ? "" : "s"}${namespace ? ` in ${namespace}` : ""}` : undefined}
        trailing={<NamespacePicker />}
      />
      <FilterStrip search={search} onSearch={(v) => setParam("q", v)} resultCount={filtered.length} totalCount={all.length} />
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? <LoadingState resource="horizontalpodautoscalers" /> :
          query.isError ? isForbidden(query.error) ? <ForbiddenState resource="horizontalpodautoscalers" /> : isForbidden(query.error) ? <ForbiddenState resource="horizontalpodautoscalers" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} /> :
          filtered.length === 0 ? <EmptyState resource="horizontalpodautoscalers" namespace={namespace} /> :
          <DataTable<HPA>
            columns={columns}
            rows={filtered}
            rowKey={(r) => `${r.namespace}/${r.name}`}
            onRowClick={(r) => setMany({ sel: r.name, selNs: r.namespace, tab: "describe" })}
            selectedKey={selectedKey}
          />
        }
        right={detail}
      />
    </div>
  );
}
