import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { CronJob, CronJobList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { describeCron } from "../lib/cron";
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
import { CronJobDescribe } from "../components/detail/describe/CronJobDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { ResourceActions } from "../components/edit/ResourceActions";
import { EventsView } from "../components/detail/EventsView";
import { NamespacePicker } from "../components/shell/NamespacePicker";

function relativeTime(iso?: string): string {
  if (!iso) return "—";
  return ageFrom(iso);
}

export function CronJobsPage({ cluster }: { cluster: string }) {
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
    resource: "cronjobs",
    namespace: namespace ?? undefined,
  });
  const all = useMemo<CronJob[]>(() => (query.data as CronJobList | undefined)?.cronJobs ?? [], [query.data]);

  const suspendedFlag = params.get("suspended") === "true";

  const suspended = all.filter((c) => c.suspend).length;

  const filtered = useMemo(() => {
    let rows = all;
    if (suspendedFlag) rows = rows.filter((c) => c.suspend);
    if (search) rows = rows.filter((c) => nameMatches(c.name, search));
    return rows;
  }, [all, search, suspendedFlag]);

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<CronJob>[] = [
    { key: "name", header: "name", weight: 2.4, cellClassName: "font-mono text-ink", accessor: (c) => c.name },
    { key: "namespace", header: "namespace", weight: 1.2, cellClassName: "font-mono text-ink-muted", accessor: (c) => c.namespace },
    {
      key: "schedule",
      header: "schedule",
      weight: 1.4,
      cellClassName: "font-mono text-ink",
      accessor: (c) => (
        <span title={describeCron(c.schedule)}>{c.schedule}</span>
      ),
    },
    {
      key: "suspend",
      header: "suspend",
      weight: 0.6,
      cellClassName: "font-mono text-ink-muted",
      accessor: (c) =>
        c.suspend ? (
          <span className="text-yellow">true</span>
        ) : (
          <span className="text-ink-faint">false</span>
        ),
    },
    {
      key: "active",
      header: "active",
      weight: 0.5,
      align: "right",
      cellClassName: "font-mono",
      accessor: (c) => (
        <span className={c.active > 0 ? "text-accent" : "text-ink-muted"}>
          {c.active}
        </span>
      ),
    },
    {
      key: "lastSchedule",
      header: "last schedule",
      weight: 0.9,
      align: "right",
      cellClassName: "font-mono text-ink-muted",
      accessor: (c) => relativeTime(c.lastScheduleTime),
    },
    { key: "age", header: "age", weight: 0.5, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (c) => ageFrom(c.createdAt) },
  ];

  const rowTint = (c: CronJob): RowTint => {
    if (c.suspend) return "yellow";
    return null;
  };

  const editFlag = useEditorDirty(cluster, "cronjobs", selectedNs ?? undefined, selectedName);

  const detail =
    selectedNs && selectedName ? (
      <DetailPane
        title={selectedName}
        subtitle={selectedNs}
        activeTab={activeTab}
        onTabChange={(id) => setParam("tab", id)}
        onClose={() => setMany({ sel: null, selNs: null, tab: null })}
        tabs={[
          { id: "describe", label: "describe", ready: true, content: <CronJobDescribe cluster={cluster} ns={selectedNs} name={selectedName} /> },
          { id: "yaml", label: "yaml", ready: true, content: <YamlView cluster={cluster} kind="cronjobs" ns={selectedNs} name={selectedName} />, dirty: editFlag.dirty },
          { id: "events", label: "events", ready: true, content: <EventsView cluster={cluster} kind="cronjobs" ns={selectedNs} name={selectedName} /> },
        ]}
        actions={
          <ResourceActions
            cluster={cluster}
            source={{ kind: "builtin", yamlKind: "cronjobs" }}
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
        title="CronJobs"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "cronjob" : "cronjobs"}${
                namespace ? ` in ${namespace}` : ""
              }`
            : undefined
        }
        chips={[
          {
            label: "suspended",
            count: suspended,
            tone: "yellow",
            active: suspendedFlag,
            onClick: () => setParam("suspended", suspendedFlag ? null : "true"),
          },
        ]}
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
            <LoadingState resource="cronjobs" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="cronjobs" /> : isForbidden(query.error) ? <ForbiddenState resource="cronjobs" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="cronjobs" namespace={namespace} />
          ) : (
            <DataTable<CronJob>
              columns={columns}
              rows={filtered}
              rowKey={(c) => `${c.namespace}/${c.name}`}
              rowTint={rowTint}
              onRowClick={(c) => setMany({ sel: c.name, selNs: c.namespace, tab: "describe" })}
              selectedKey={selectedKey}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
