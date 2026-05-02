import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import { ageFrom, nameMatches } from "../lib/format";
import type { PVC, PVCList } from "../lib/types";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { NamespacePicker } from "../components/shell/NamespacePicker";
import { SplitPane } from "../components/page/SplitPane";
import { DataTable, type Column } from "../components/table/DataTable";
import { PhaseTag } from "../components/table/StatusDot";
import { DetailPane } from "../components/detail/DetailPane";
import { YamlView } from "../components/detail/YamlView";
import { EventsView } from "../components/detail/EventsView";
import { PVCDescribe } from "../components/detail/describe/PVCDescribe";
import { EmptyState, ErrorState, ForbiddenState, LoadingState, isForbidden } from "../components/table/states";
import type { RowTint } from "../components/table/DataTable";

const PVC_STATUS_OPTIONS = ["Bound", "Pending", "Lost"];

export function PVCsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const namespace = params.get("ns");
  const search = params.get("q") ?? "";
  const statusFilter = params.get("status");
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

  const query = useResource({ cluster, resource: "pvcs", namespace: namespace ?? undefined });
  const all = ((query.data as PVCList | undefined)?.pvcs ?? []) as PVC[];

  const filtered = useMemo(() => {
    let rows = all;
    if (statusFilter) rows = rows.filter((p) => p.status === statusFilter);
    if (search) rows = rows.filter((p) => nameMatches(p.name, search));
    return rows;
  }, [all, statusFilter, search]);

  const pending = all.filter((p) => p.status === "Pending").length;
  const lost = all.filter((p) => p.status === "Lost").length;

  const selectedKey = selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<PVC>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (p) => p.name },
    { key: "namespace", header: "namespace", weight: 1.2, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.namespace },
    { key: "status", header: "status", weight: 0.8, accessor: (p) => <PhaseTag phase={p.status} /> },
    { key: "capacity", header: "capacity", weight: 0.7, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.capacity ?? "—" },
    { key: "storageClass", header: "class", weight: 1, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.storageClass ?? "—" },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (p) => ageFrom(p.createdAt) },
  ];

  const rowTint = (p: PVC): RowTint =>
    p.status === "Lost" ? "red" : p.status === "Pending" ? "yellow" : null;

  const detail =
    selectedNs && selectedName ? (
      <DetailPane
        title={selectedName}
        subtitle={selectedNs}
        activeTab={activeTab}
        onTabChange={(id) => setParam("tab", id)}
        onClose={() => setMany({ sel: null, selNs: null, tab: null })}
        tabs={[
          { id: "describe", label: "describe", ready: true, content: <PVCDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="pvcs" ns={selectedNs} name={selectedName} /> },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="pvcs" ns={selectedNs} name={selectedName} /> },
        ]}
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="PersistentVolumeClaims"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "pvc" : "pvcs"}${namespace ? ` in ${namespace}` : ""}`
            : undefined
        }
        chips={[
          { label: "pending", count: pending, tone: "yellow", active: statusFilter === "Pending", onClick: () => setParam("status", statusFilter === "Pending" ? null : "Pending") },
          { label: "lost", count: lost, tone: "red", active: statusFilter === "Lost", onClick: () => setParam("status", statusFilter === "Lost" ? null : "Lost") },
        ]}
        trailing={<NamespacePicker />}
      />
      <FilterStrip
        search={search}
        onSearch={(v) => setParam("q", v)}
        statusFilter={statusFilter}
        statusOptions={PVC_STATUS_OPTIONS}
        onStatusFilter={(v) => setParam("status", v)}
        resultCount={filtered.length}
        totalCount={all.length}
      />
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? (
            <LoadingState resource="pvcs" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="pvcs" /> : isForbidden(query.error) ? <ForbiddenState resource="pvcs" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="pvcs" namespace={namespace} />
          ) : (
            <DataTable<PVC>
              columns={columns}
              rows={filtered}
              rowKey={(p) => `${p.namespace}/${p.name}`}
              rowTint={rowTint}
              onRowClick={(p) => setMany({ sel: p.name, selNs: p.namespace, tab: "describe" })}
              selectedKey={selectedKey}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
