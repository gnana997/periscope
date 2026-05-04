import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { DaemonSet, DaemonSetList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import {
  type Column,
  type RowTint,
} from "../components/table/DataTable";
import { SelectableDataTable } from "../components/table/SelectableDataTable";
import { api } from "../lib/api";
import {
  EmptyState,
  ErrorState,
  ForbiddenState,
  LoadingState,
} from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { DaemonSetDescribe } from "../components/detail/describe/DaemonSetDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { useConfirmDiscard } from "../hooks/useConfirmDiscard";
import { ResourceActions } from "../components/edit/ResourceActions";
import { EventsView } from "../components/detail/EventsView";
import { WorkloadLogsTab } from "../components/logs/WorkloadLogsTab";
import { NamespacePicker } from "../components/shell/NamespacePicker";
import { cn } from "../lib/cn";

export function DaemonSetsPage({ cluster }: { cluster: string }) {
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
    resource: "daemonsets",
    namespace: namespace ?? undefined,
  });
  const all = useMemo<DaemonSet[]>(() => (query.data as DaemonSetList | undefined)?.daemonSets ?? [], [query.data]);
  const filtered = useMemo(
    () => (search ? all.filter((d) => nameMatches(d.name, search)) : all),
    [all, search],
  );

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<DaemonSet>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (d) => d.name },
    { key: "namespace", header: "namespace", weight: 1.4, cellClassName: "font-mono text-ink-muted", accessor: (d) => d.namespace },
    {
      key: "ready",
      header: "ready",
      weight: 0.8,
      align: "right",
      cellClassName: "font-mono",
      accessor: (d) => (
        <span className={cn(d.numberReady < d.desiredNumberScheduled ? "text-yellow" : "text-ink")}>
          {d.numberReady}/{d.desiredNumberScheduled}
        </span>
      ),
    },
    { key: "updated", header: "up-to-date", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (d) => d.updatedNumberScheduled },
    { key: "available", header: "available", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (d) => d.numberAvailable },
    {
      key: "miss",
      header: "miss",
      weight: 0.5,
      align: "right",
      cellClassName: "font-mono",
      accessor: (d) => (
        <span className={d.numberMisscheduled > 0 ? "text-yellow" : "text-ink-muted"}>
          {d.numberMisscheduled}
        </span>
      ),
    },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (d) => ageFrom(d.createdAt) },
  ];

  const rowTint = (d: DaemonSet): RowTint => {
    if (d.desiredNumberScheduled > 0 && d.numberReady === 0) return "red";
    if (d.numberReady < d.desiredNumberScheduled) return "yellow";
    if (d.numberMisscheduled > 0) return "yellow";
    return null;
  };

  const editFlag = useEditorDirty(cluster, "daemonsets", selectedNs ?? undefined, selectedName);
  const confirmDiscard = useConfirmDiscard(editFlag.dirty);

  const detail =
    selectedNs && selectedName ? (
      <DetailPane
        title={selectedName}
        subtitle={selectedNs}
        activeTab={activeTab}
        onTabChange={(id) => confirmDiscard(() => setParam("tab", id))}
        onClose={() => confirmDiscard(() => setMany({ sel: null, selNs: null, tab: null }))}
        tabs={[
          { id: "describe", label: "describe", ready: true, content: <DaemonSetDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} source={{ kind: "builtin", yamlKind: "daemonsets" }} ns={selectedNs} name={selectedName} />, dirty: editFlag.dirty },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="daemonsets" ns={selectedNs} name={selectedName} /> },
          { id: "logs", label: "logs", ready: true, content: <WorkloadLogsTab kind="daemonset" cluster={cluster} ns={selectedNs} name={selectedName} /> },
        ]}
        actions={
          <ResourceActions
            cluster={cluster}
            source={{ kind: "builtin", yamlKind: "daemonsets" }}
            namespace={selectedNs}
            name={selectedName}
            onDeleted={() => setParam("sel", null)}
          />
        }
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="DaemonSets"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "daemonset" : "daemonsets"}${
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
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? (
            <LoadingState resource="daemonsets" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="daemonsets" /> : isForbidden(query.error) ? <ForbiddenState resource="daemonsets" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="daemonsets" namespace={namespace} />
          ) : (
            <SelectableDataTable<DaemonSet>
              columns={columns}
              rows={filtered}
              rowKey={(d) => `${d.namespace}/${d.name}`}
              rowTint={rowTint}
              onRowClick={(d) => confirmDiscard(() => setMany({ sel: d.name, selNs: d.namespace, tab: "describe" }))}
              selectedKey={selectedKey}
              bulk={{
                cluster,
                kindLabel: "daemonsets",
                fetchYaml: (d, signal) => api.yaml(cluster, "daemonsets", d.namespace, d.name, signal),
              }}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
