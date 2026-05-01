import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { Service, ServiceList } from "../lib/types";
import { ageFrom, formatPorts, nameMatches } from "../lib/format";
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
import { ServiceDescribe } from "../components/detail/describe/ServiceDescribe";
import { YamlView } from "../components/detail/YamlView";
import { EventsView } from "../components/detail/EventsView";
import { NamespacePicker } from "../components/shell/NamespacePicker";

export function ServicesPage({ cluster }: { cluster: string }) {
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
    resource: "services",
    namespace: namespace ?? undefined,
  });
  const all =
    ((query.data as ServiceList | undefined)?.services ?? []) as Service[];
  const filtered = useMemo(
    () => (search ? all.filter((s) => nameMatches(s.name, search)) : all),
    [all, search],
  );

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<Service>[] = [
    { key: "name", header: "name", weight: 2.4, cellClassName: "font-mono text-ink", accessor: (s) => s.name },
    { key: "namespace", header: "namespace", weight: 1.3, cellClassName: "font-mono text-ink-muted", accessor: (s) => s.namespace },
    { key: "type", header: "type", weight: 1, cellClassName: "text-ink", accessor: (s) => s.type },
    { key: "clusterip", header: "cluster-ip", weight: 1.1, cellClassName: "font-mono text-ink-muted", accessor: (s) => s.clusterIP || "—" },
    { key: "external", header: "external", weight: 1.4, cellClassName: "font-mono text-ink-muted", accessor: (s) => s.externalIP || "—" },
    { key: "ports", header: "ports", weight: 2, cellClassName: "font-mono text-ink-muted", accessor: (s) => formatPorts(s.ports) },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (s) => ageFrom(s.createdAt) },
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
          { id: "describe", label: "describe", ready: true, content: <ServiceDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="services" ns={selectedNs} name={selectedName} /> },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="services" ns={selectedNs} name={selectedName} /> },
        ]}
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Services"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "service" : "services"}${
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
            <LoadingState resource="services" />
          ) : query.isError ? (
            <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="services" namespace={namespace} />
          ) : (
            <DataTable<Service>
              columns={columns}
              rows={filtered}
              rowKey={(s) => `${s.namespace}/${s.name}`}
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
