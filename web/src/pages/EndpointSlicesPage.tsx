import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { EndpointSlice, EndpointSliceList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import { type Column } from "../components/table/DataTable";
import { SelectableDataTable } from "../components/table/SelectableDataTable";
import { api } from "../lib/api";
import { EmptyState, ErrorState, ForbiddenState, LoadingState } from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { EndpointSliceDescribe } from "../components/detail/describe/EndpointSliceDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { useConfirmDiscard } from "../hooks/useConfirmDiscard";
import { ResourceActions } from "../components/edit/ResourceActions";
import { EventsView } from "../components/detail/EventsView";
import { NamespacePicker } from "../components/shell/NamespacePicker";

export function EndpointSlicesPage({ cluster }: { cluster: string }) {
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

  const query = useResource({ cluster, resource: "endpointslices", namespace: namespace ?? undefined });
  const all = useMemo<EndpointSlice[]>(
    () => (query.data as EndpointSliceList | undefined)?.endpointSlices ?? [],
    [query.data],
  );
  const filtered = useMemo(
    () => (search ? all.filter((r) => nameMatches(r.name, search)) : all),
    [all, search],
  );

  const formatPorts = (es: EndpointSlice) => {
    if (es.ports.length === 0) return "—";
    return es.ports
      .map((p) => {
        const proto = p.protocol ?? "TCP";
        return p.name ? `${p.name}:${p.port}/${proto}` : `${p.port}/${proto}`;
      })
      .join(", ");
  };

  const columns: Column<EndpointSlice>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (r) => r.name },
    { key: "namespace", header: "namespace", weight: 2, cellClassName: "font-mono text-ink-muted", accessor: (r) => r.namespace },
    { key: "service", header: "service", weight: 2, cellClassName: "font-mono text-ink-muted", accessor: (r) => r.serviceName || "—" },
    { key: "addressType", header: "addr type", weight: 1, cellClassName: "font-mono text-ink-muted", accessor: (r) => r.addressType },
    { key: "ports", header: "ports", weight: 2, cellClassName: "font-mono text-ink-muted", accessor: formatPorts },
    {
      key: "endpoints",
      header: "endpoints",
      weight: 1.2,
      align: "right",
      cellClassName: "font-mono text-ink-muted",
      accessor: (r) => `${r.readyCount}/${r.totalCount}`,
    },
    { key: "age", header: "age", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (r) => ageFrom(r.createdAt) },
  ];

  const selectedKey = selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const editFlag = useEditorDirty(cluster, "endpointslices", selectedNs ?? undefined, selectedName);
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
          { id: "describe", label: "describe", ready: true, content: <EndpointSliceDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} source={{ kind: "builtin", yamlKind: "endpointslices" }} ns={selectedNs} name={selectedName} />, dirty: editFlag.dirty },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="endpointslices" ns={selectedNs} name={selectedName} /> },
        ]}
        actions={
          <ResourceActions
            cluster={cluster}
            source={{ kind: "builtin", yamlKind: "endpointslices" }}
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
        title="EndpointSlices"
        subtitle={query.isSuccess ? `${all.length} ${all.length === 1 ? "endpointslice" : "endpointslices"}${namespace ? ` in ${namespace}` : ""}` : undefined}
        trailing={<NamespacePicker />}
      />
      <FilterStrip search={search} onSearch={(v) => setParam("q", v)} resultCount={filtered.length} totalCount={all.length} />
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? <LoadingState resource="endpointslices" /> :
          query.isError ? isForbidden(query.error) ? <ForbiddenState resource="endpointslices" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} /> :
          filtered.length === 0 ? <EmptyState resource="endpointslices" namespace={namespace} /> :
          <SelectableDataTable<EndpointSlice>
            columns={columns}
            rows={filtered}
            rowKey={(r) => `${r.namespace}/${r.name}`}
            onRowClick={(r) => confirmDiscard(() => setMany({ sel: r.name, selNs: r.namespace, tab: "describe" }))}
            selectedKey={selectedKey}
            bulk={{
              cluster,
              kindLabel: "endpointslices",
              fetchYaml: (r, signal) => api.yaml(cluster, "endpointslices", r.namespace, r.name, signal),
            }}
          />
        }
        right={detail}
      />
    </div>
  );
}
