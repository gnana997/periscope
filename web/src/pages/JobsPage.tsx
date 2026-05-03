import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { Job, JobList, JobStatus } from "../lib/types";
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
  LoadingState,
} from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { JobDescribe } from "../components/detail/describe/JobDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { useConfirmDiscard } from "../hooks/useConfirmDiscard";
import { ResourceActions } from "../components/edit/ResourceActions";
import { EventsView } from "../components/detail/EventsView";
import { WorkloadLogsTab } from "../components/logs/WorkloadLogsTab";
import { NamespacePicker } from "../components/shell/NamespacePicker";
import { cn } from "../lib/cn";

const JOB_STATUS_OPTIONS: JobStatus[] = [
  "Running",
  "Complete",
  "Failed",
  "Suspended",
  "Pending",
];

function statusToneClass(s: JobStatus): string {
  switch (s) {
    case "Running":
      return "text-accent";
    case "Complete":
      return "text-green";
    case "Failed":
      return "text-red";
    case "Suspended":
      return "text-ink-muted";
    case "Pending":
      return "text-yellow";
    default:
      return "text-ink";
  }
}

export function JobsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const namespace = params.get("ns");
  const search = params.get("q") ?? "";
  const status = params.get("status") as JobStatus | null;
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
    resource: "jobs",
    namespace: namespace ?? undefined,
  });
  const all = useMemo<Job[]>(() => (query.data as JobList | undefined)?.jobs ?? [], [query.data]);

  const failing = all.filter((j) => j.status === "Failed").length;
  const running = all.filter((j) => j.status === "Running").length;

  const filtered = useMemo(() => {
    let rows = all;
    if (status) rows = rows.filter((j) => j.status === status);
    if (search) rows = rows.filter((j) => nameMatches(j.name, search));
    return rows;
  }, [all, search, status]);

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<Job>[] = [
    { key: "name", header: "name", weight: 2.6, cellClassName: "font-mono text-ink", accessor: (j) => j.name },
    { key: "namespace", header: "namespace", weight: 1.3, cellClassName: "font-mono text-ink-muted", accessor: (j) => j.namespace },
    {
      key: "completions",
      header: "completions",
      weight: 0.8,
      align: "right",
      cellClassName: "font-mono",
      accessor: (j) => {
        const [done, target] = j.completions.split("/").map((n) => parseInt(n, 10));
        const pending = !Number.isNaN(done) && !Number.isNaN(target) && done < target;
        return (
          <span className={cn(pending && j.status !== "Failed" ? "text-yellow" : "text-ink")}>
            {j.completions}
          </span>
        );
      },
    },
    {
      key: "status",
      header: "status",
      weight: 1,
      cellClassName: "font-mono",
      accessor: (j) => (
        <span className={cn("inline-flex items-center gap-1.5", statusToneClass(j.status))}>
          <span className="block size-1.5 rounded-full bg-current" />
          {j.status}
        </span>
      ),
    },
    {
      key: "duration",
      header: "duration",
      weight: 0.7,
      align: "right",
      cellClassName: "font-mono text-ink-muted",
      accessor: (j) => j.duration ?? "—",
    },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (j) => ageFrom(j.createdAt) },
  ];

  const rowTint = (j: Job): RowTint => {
    if (j.status === "Failed") return "red";
    if (j.status === "Running") return "yellow";
    return null;
  };

  const editFlag = useEditorDirty(cluster, "jobs", selectedNs ?? undefined, selectedName);
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
          { id: "describe", label: "describe", ready: true, content: <JobDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} source={{ kind: "builtin", yamlKind: "jobs" }} ns={selectedNs} name={selectedName} />, dirty: editFlag.dirty },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="jobs" ns={selectedNs} name={selectedName} /> },
          { id: "logs", label: "logs", ready: true, content: <WorkloadLogsTab kind="job" cluster={cluster} ns={selectedNs} name={selectedName} /> },
        ]}
        actions={
          <ResourceActions
            cluster={cluster}
            source={{ kind: "builtin", yamlKind: "jobs" }}
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
        title="Jobs"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "job" : "jobs"}${
                namespace ? ` in ${namespace}` : ""
              }`
            : undefined
        }
        chips={[
          { label: "failing", count: failing, tone: "red", active: status === "Failed", onClick: () => setParam("status", status === "Failed" ? null : "Failed") },
          { label: "running", count: running, tone: "yellow", active: status === "Running", onClick: () => setParam("status", status === "Running" ? null : "Running") },
        ]}
        trailing={<NamespacePicker />}
      />
      <FilterStrip
        search={search}
        onSearch={(v) => setParam("q", v)}
        statusFilter={status}
        statusOptions={JOB_STATUS_OPTIONS}
        onStatusFilter={(v) => setParam("status", v)}
        resultCount={filtered.length}
        totalCount={all.length}
      />
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? (
            <LoadingState resource="jobs" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="jobs" /> : isForbidden(query.error) ? <ForbiddenState resource="jobs" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="jobs" namespace={namespace} />
          ) : (
            <DataTable<Job>
              columns={columns}
              rows={filtered}
              rowKey={(j) => `${j.namespace}/${j.name}`}
              rowTint={rowTint}
              onRowClick={(j) => confirmDiscard(() => setMany({ sel: j.name, selNs: j.namespace, tab: "describe" }))}
              selectedKey={selectedKey}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
