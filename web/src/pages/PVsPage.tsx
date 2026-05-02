import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import { ageFrom, nameMatches } from "../lib/format";
import type { PV, PVList } from "../lib/types";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import { DataTable, type Column } from "../components/table/DataTable";
import { PhaseTag } from "../components/table/StatusDot";
import { DetailPane } from "../components/detail/DetailPane";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { EventsView } from "../components/detail/EventsView";
import { PVDescribe } from "../components/detail/describe/PVDescribe";
import { EmptyState, ErrorState, ForbiddenState, LoadingState, isForbidden } from "../components/table/states";
import type { RowTint } from "../components/table/DataTable";

const PV_STATUS_OPTIONS = ["Available", "Bound", "Released", "Failed"];

export function PVsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const statusFilter = params.get("status");
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

  const query = useResource({ cluster, resource: "pvs" });
  const all = ((query.data as PVList | undefined)?.pvs ?? []) as PV[];

  const filtered = useMemo(() => {
    let rows = all;
    if (statusFilter) rows = rows.filter((p) => p.status === statusFilter);
    if (search) rows = rows.filter((p) => nameMatches(p.name, search));
    return rows;
  }, [all, statusFilter, search]);

  const available = all.filter((p) => p.status === "Available").length;
  const released = all.filter((p) => p.status === "Released").length;

  const columns: Column<PV>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (p) => p.name },
    { key: "status", header: "status", weight: 0.8, accessor: (p) => <PhaseTag phase={p.status} /> },
    { key: "capacity", header: "capacity", weight: 0.7, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.capacity ?? "—" },
    { key: "storageClass", header: "class", weight: 1, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.storageClass ?? "—" },
    { key: "reclaim", header: "reclaim", weight: 0.8, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.reclaimPolicy ?? "—" },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (p) => ageFrom(p.createdAt) },
  ];

  const rowTint = (p: PV): RowTint =>
    p.status === "Failed" ? "red" : p.status === "Released" ? "yellow" : null;

  const editFlag = useEditorDirty(cluster, "pvs", undefined, selectedName);

  const detail = selectedName ? (
    <DetailPane
      title={selectedName}
      subtitle="cluster-scoped"
      activeTab={activeTab}
      onTabChange={(id) => setParam("tab", id)}
      onClose={() => setMany({ sel: null, tab: null })}
      tabs={[
        { id: "describe", label: "describe", ready: true, content: <PVDescribe cluster={cluster} name={selectedName} /> },
        { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="pvs" ns="" name={selectedName} />, dirty: editFlag.dirty },
        { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="pvs" ns="" name={selectedName} /> },
      ]}
    />
  ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="PersistentVolumes"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "pv" : "pvs"}`
            : undefined
        }
        chips={[
          { label: "available", count: available, tone: "green", active: statusFilter === "Available", onClick: () => setParam("status", statusFilter === "Available" ? null : "Available") },
          { label: "released", count: released, tone: "yellow", active: statusFilter === "Released", onClick: () => setParam("status", statusFilter === "Released" ? null : "Released") },
        ]}
      />
      <FilterStrip
        search={search}
        onSearch={(v) => setParam("q", v)}
        statusFilter={statusFilter}
        statusOptions={PV_STATUS_OPTIONS}
        onStatusFilter={(v) => setParam("status", v)}
        resultCount={filtered.length}
        totalCount={all.length}
      />
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? (
            <LoadingState resource="pvs" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="pvs" /> : isForbidden(query.error) ? <ForbiddenState resource="pvs" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="pvs" namespace={null} />
          ) : (
            <DataTable<PV>
              columns={columns}
              rows={filtered}
              rowKey={(p) => p.name}
              rowTint={rowTint}
              onRowClick={(p) => setMany({ sel: p.name, tab: "describe" })}
              selectedKey={selectedName}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
