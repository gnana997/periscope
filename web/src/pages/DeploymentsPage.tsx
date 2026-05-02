import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { Deployment, DeploymentList } from "../lib/types";
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
  ForbiddenState,
  isForbidden,
  LoadingState,
} from "../components/table/states";
import { DetailPane } from "../components/detail/DetailPane";
import { DeploymentDescribe } from "../components/detail/describe/DeploymentDescribe";
import { YamlView } from "../components/detail/YamlView";
import { EventsView } from "../components/detail/EventsView";
import { WorkloadLogsTab } from "../components/logs/WorkloadLogsTab";
import { NamespacePicker } from "../components/shell/NamespacePicker";
import { cn } from "../lib/cn";

export function DeploymentsPage({ cluster }: { cluster: string }) {
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
    resource: "deployments",
    namespace: namespace ?? undefined,
  });
  const all =
    ((query.data as DeploymentList | undefined)?.deployments ?? []) as Deployment[];
  const filtered = useMemo(
    () => (search ? all.filter((d) => nameMatches(d.name, search)) : all),
    [all, search],
  );

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<Deployment>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (d) => d.name },
    { key: "namespace", header: "namespace", weight: 1.4, cellClassName: "font-mono text-ink-muted", accessor: (d) => d.namespace },
    {
      key: "ready",
      header: "ready",
      weight: 0.7,
      align: "right",
      cellClassName: "font-mono",
      accessor: (d) => (
        <span className={cn(d.readyReplicas < d.replicas ? "text-yellow" : "text-ink")}>
          {d.readyReplicas}/{d.replicas}
        </span>
      ),
    },
    { key: "updated", header: "up-to-date", weight: 0.7, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (d) => d.updatedReplicas },
    { key: "available", header: "available", weight: 0.7, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (d) => d.availableReplicas },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (d) => ageFrom(d.createdAt) },
  ];

  const rowTint = (d: Deployment): RowTint => {
    if (d.replicas > 0 && d.readyReplicas === 0) return "red";
    if (d.readyReplicas < d.replicas) return "yellow";
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
          { id: "describe", label: "describe", ready: true, content: <DeploymentDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="deployments" ns={selectedNs} name={selectedName} /> },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="deployments" ns={selectedNs} name={selectedName} /> },
          { id: "logs", label: "logs", ready: true, content: <WorkloadLogsTab kind="deployment" cluster={cluster} ns={selectedNs} name={selectedName} /> },
        ]}
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Deployments"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "deployment" : "deployments"}${
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
            <LoadingState resource="deployments" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="deployments" /> : isForbidden(query.error) ? <ForbiddenState resource="deployments" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="deployments" namespace={namespace} />
          ) : (
            <DataTable<Deployment>
              columns={columns}
              rows={filtered}
              rowKey={(d) => `${d.namespace}/${d.name}`}
              rowTint={rowTint}
              onRowClick={(d) => setMany({ sel: d.name, selNs: d.namespace, tab: "describe" })}
              selectedKey={selectedKey}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
