import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { ServiceAccount, ServiceAccountList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import { DataTable, type Column } from "../components/table/DataTable";
import { EmptyState, ErrorState, ForbiddenState, LoadingState } from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { ServiceAccountDescribe } from "../components/detail/describe/ServiceAccountDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { ResourceActions } from "../components/edit/ResourceActions";
import { NamespacePicker } from "../components/shell/NamespacePicker";

export function ServiceAccountsPage({ cluster }: { cluster: string }) {
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

  const query = useResource({ cluster, resource: "serviceaccounts", namespace: namespace ?? undefined });
  const all = useMemo<ServiceAccount[]>(() => (query.data as ServiceAccountList | undefined)?.serviceAccounts ?? [], [query.data]);
  const filtered = useMemo(
    () => (search ? all.filter((sa) => nameMatches(sa.name, search)) : all),
    [all, search],
  );

  const columns: Column<ServiceAccount>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (sa) => sa.name },
    { key: "namespace", header: "namespace", weight: 2, cellClassName: "font-mono text-ink-muted", accessor: (sa) => sa.namespace },
    { key: "secrets", header: "secrets", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (sa) => String(sa.secrets) },
    { key: "age", header: "age", weight: 0.8, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (sa) => ageFrom(sa.createdAt) },
  ];

  const selectedKey = selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const editFlag = useEditorDirty(cluster, "serviceaccounts", selectedNs ?? undefined, selectedName);

  const detail =
    selectedNs && selectedName ? (
      <DetailPane
        title={selectedName}
        subtitle={selectedNs}
        activeTab={activeTab}
        onTabChange={(id) => setParam("tab", id)}
        onClose={() => setMany({ sel: null, selNs: null, tab: null })}
        tabs={[
          { id: "describe", label: "describe", ready: true, content: <ServiceAccountDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="serviceaccounts" ns={selectedNs} name={selectedName} />, dirty: editFlag.dirty },
        ]}
        actions={
          <ResourceActions
            cluster={cluster}
            yamlKind="serviceaccounts"
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
        title="ServiceAccounts"
        subtitle={query.isSuccess ? `${all.length} ${all.length === 1 ? "serviceaccount" : "serviceaccounts"}${namespace ? ` in ${namespace}` : ""}` : undefined}
        trailing={<NamespacePicker />}
      />
      <FilterStrip search={search} onSearch={(v) => setParam("q", v)} resultCount={filtered.length} totalCount={all.length} />
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? <LoadingState resource="serviceaccounts" /> :
          query.isError ? isForbidden(query.error) ? <ForbiddenState resource="serviceaccounts" /> : isForbidden(query.error) ? <ForbiddenState resource="serviceaccounts" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} /> :
          filtered.length === 0 ? <EmptyState resource="serviceaccounts" namespace={namespace} /> :
          <DataTable<ServiceAccount>
            columns={columns}
            rows={filtered}
            rowKey={(sa) => `${sa.namespace}/${sa.name}`}
            onRowClick={(sa) => setMany({ sel: sa.name, selNs: sa.namespace, tab: "describe" })}
            selectedKey={selectedKey}
          />
        }
        right={detail}
      />
    </div>
  );
}
