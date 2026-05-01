import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { StatefulSet, StatefulSetList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import {
  DataTable,
  type Column,
  type RowTint,
} from "../components/table/DataTable";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "../components/table/states";
import { DetailPane } from "../components/detail/DetailPane";
import { StatefulSetDescribe } from "../components/detail/describe/StatefulSetDescribe";
import { YamlView } from "../components/detail/YamlView";
import { EventsView } from "../components/detail/EventsView";
import { WorkloadLogsTab } from "../components/logs/WorkloadLogsTab";
import { NamespacePicker } from "../components/shell/NamespacePicker";
import { cn } from "../lib/cn";

export function StatefulSetsPage({ cluster }: { cluster: string }) {
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
    resource: "statefulsets",
    namespace: namespace ?? undefined,
  });
  const all =
    ((query.data as StatefulSetList | undefined)?.statefulSets ?? []) as StatefulSet[];
  const filtered = useMemo(
    () => (search ? all.filter((s) => nameMatches(s.name, search)) : all),
    [all, search],
  );

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<StatefulSet>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (s) => s.name },
    { key: "namespace", header: "namespace", weight: 1.4, cellClassName: "font-mono text-ink-muted", accessor: (s) => s.namespace },
    {
      key: "ready",
      header: "ready",
      weight: 0.7,
      align: "right",
      cellClassName: "font-mono",
      accessor: (s) => (
        <span className={cn(s.readyReplicas < s.replicas ? "text-yellow" : "text-ink")}>
          {s.readyReplicas}/{s.replicas}
        </span>
      ),
    },
    { key: "updated", header: "updated", weight: 0.7, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (s) => s.updatedReplicas },
    { key: "current", header: "current", weight: 0.7, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (s) => s.currentReplicas },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (s) => ageFrom(s.createdAt) },
  ];

  const rowTint = (s: StatefulSet): RowTint => {
    if (s.replicas > 0 && s.readyReplicas === 0) return "red";
    if (s.readyReplicas < s.replicas) return "yellow";
    return null;
  };

  const detail =
    selectedNs && selectedName ? (
      <DetailPane
        title={selectedName}
        subtitle={selectedNs}
        activeTab={activeTab}
        onTabChange={(id) => setParam("tab", id)}
        onClose={() => setMany({ sel: null, selNs: null, tab: null })}
        tabs={[
          { id: "describe", label: "describe", ready: true, content: <StatefulSetDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="statefulsets" ns={selectedNs} name={selectedName} /> },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="statefulsets" ns={selectedNs} name={selectedName} /> },
          { id: "logs", label: "logs", ready: true, content: <WorkloadLogsTab kind="statefulset" cluster={cluster} ns={selectedNs} name={selectedName} /> },
        ]}
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="StatefulSets"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "statefulset" : "statefulsets"}${
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
            <LoadingState resource="statefulsets" />
          ) : query.isError ? (
            <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="statefulsets" namespace={namespace} />
          ) : (
            <DataTable<StatefulSet>
              columns={columns}
              rows={filtered}
              rowKey={(s) => `${s.namespace}/${s.name}`}
              rowTint={rowTint}
              onRowClick={(s) => setMany({ sel: s.name, selNs: s.namespace, tab: "describe" })}
              selectedKey={selectedKey}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
