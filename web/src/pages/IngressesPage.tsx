import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { Ingress, IngressList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import { DataTable, type Column } from "../components/table/DataTable";
import {
  EmptyState,
  ErrorState,
  ForbiddenState,
  isForbidden,
  LoadingState,
} from "../components/table/states";
import { DetailPane } from "../components/detail/DetailPane";
import { IngressDescribe } from "../components/detail/describe/IngressDescribe";
import { YamlView } from "../components/detail/YamlView";
import { EventsView } from "../components/detail/EventsView";
import { NamespacePicker } from "../components/shell/NamespacePicker";

function joinHosts(hosts: string[]): string {
  if (hosts.length === 0) return "—";
  if (hosts.length <= 2) return hosts.join(", ");
  return `${hosts[0]}, +${hosts.length - 1}`;
}

export function IngressesPage({ cluster }: { cluster: string }) {
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
    resource: "ingresses",
    namespace: namespace ?? undefined,
  });
  const all =
    ((query.data as IngressList | undefined)?.ingresses ?? []) as Ingress[];
  const filtered = useMemo(
    () => (search ? all.filter((s) => nameMatches(s.name, search)) : all),
    [all, search],
  );

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<Ingress>[] = [
    { key: "name", header: "name", weight: 2.4, cellClassName: "font-mono text-ink", accessor: (i) => i.name },
    { key: "namespace", header: "namespace", weight: 1.3, cellClassName: "font-mono text-ink-muted", accessor: (i) => i.namespace },
    {
      key: "class",
      header: "class",
      weight: 1,
      cellClassName: "text-ink-muted",
      accessor: (i) => i.class || "—",
    },
    {
      key: "hosts",
      header: "hosts",
      weight: 2.4,
      cellClassName: "font-mono text-ink-muted",
      accessor: (i) => (
        <span title={i.hosts.join(", ")}>{joinHosts(i.hosts)}</span>
      ),
    },
    {
      key: "address",
      header: "address",
      weight: 1.4,
      cellClassName: "font-mono text-ink-muted",
      accessor: (i) => i.address || "—",
    },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (i) => ageFrom(i.createdAt) },
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
          { id: "describe", label: "describe", ready: true, content: <IngressDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="ingresses" ns={selectedNs} name={selectedName} /> },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="ingresses" ns={selectedNs} name={selectedName} /> },
        ]}
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Ingresses"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "ingress" : "ingresses"}${
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
            <LoadingState resource="ingresses" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="ingresses" /> : isForbidden(query.error) ? <ForbiddenState resource="ingresses" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="ingresses" namespace={namespace} />
          ) : (
            <DataTable<Ingress>
              columns={columns}
              rows={filtered}
              rowKey={(i) => `${i.namespace}/${i.name}`}
              onRowClick={(i) => setMany({ sel: i.name, selNs: i.namespace, tab: "describe" })}
              selectedKey={selectedKey}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
