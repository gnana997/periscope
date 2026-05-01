import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { Pod, PodList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import {
  DataTable,
  type Column,
  type RowTint,
} from "../components/table/DataTable";
import { PhaseTag, phaseTone } from "../components/table/StatusDot";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "../components/table/states";
import { DetailPane } from "../components/detail/DetailPane";
import { PodDescribe } from "../components/detail/describe/PodDescribe";
import { OpenShellButton } from "../components/exec/OpenShellButton";
import { YamlView } from "../components/detail/YamlView";
import { EventsView } from "../components/detail/EventsView";
import { PodLogsTab } from "../components/logs/PodLogsTab";
import { NamespacePicker } from "../components/shell/NamespacePicker";
import { cn } from "../lib/cn";

export function PodsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const namespace = params.get("ns");
  const search = params.get("q") ?? "";
  const status = params.get("status");
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

  const podsQuery = useResource({
    cluster,
    resource: "pods",
    namespace: namespace ?? undefined,
  });

  const allPods = ((podsQuery.data as PodList | undefined)?.pods ?? []) as Pod[];

  const failing = useMemo(
    () =>
      allPods.filter((p) =>
        phaseTone(p.phase) === "red",
      ).length,
    [allPods],
  );
  const pending = useMemo(
    () => allPods.filter((p) => phaseTone(p.phase) === "yellow").length,
    [allPods],
  );

  const filtered = useMemo(() => {
    let r = allPods;
    if (search) r = r.filter((p) => nameMatches(p.name, search));
    if (status === "Failed")
      r = r.filter((p) =>
        phaseTone(p.phase) === "red",
      );
    else if (status === "Running") r = r.filter((p) => phaseTone(p.phase) === "green");
    return r;
  }, [allPods, search, status]);

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<Pod>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (p) => p.name },
    { key: "namespace", header: "namespace", weight: 1.4, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.namespace },
    { key: "phase", header: "phase", weight: 1.2, accessor: (p) => <PhaseTag phase={p.phase} /> },
    {
      key: "ready",
      header: "ready",
      weight: 0.6,
      align: "right",
      cellClassName: "font-mono",
      accessor: (p) => {
        const [r, t] = p.ready.split("/").map((n) => parseInt(n, 10));
        return (
          <span className={cn(r < t ? "text-yellow" : "text-ink")}>{p.ready}</span>
        );
      },
    },
    {
      key: "restarts",
      header: "restarts",
      weight: 0.7,
      align: "right",
      cellClassName: "font-mono",
      accessor: (p) => (
        <span className={p.restarts > 5 ? "text-red" : p.restarts > 0 ? "text-yellow" : "text-ink-muted"}>
          {p.restarts}
        </span>
      ),
    },
    { key: "node", header: "node", weight: 1.6, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.nodeName ?? "—" },
    { key: "ip", header: "pod ip", weight: 1.1, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.podIP ?? "—" },
    { key: "age", header: "age", weight: 0.6, align: "right", cellClassName: "font-mono text-ink-muted", accessor: (p) => ageFrom(p.createdAt) },
  ];

  const rowTint = (p: Pod): RowTint => {
    if (phaseTone(p.phase) === "red") return "red";
    if (phaseTone(p.phase) === "yellow") return "yellow";
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
          {
            id: "describe",
            label: "describe",
            ready: true,
            content: <PodDescribe cluster={cluster} ns={selectedNs} name={selectedName} />,
          },
          {
            id: "yaml",
            label: "yaml",
            ready: true,
            content: <YamlView cluster={cluster} kind="pods" ns={selectedNs} name={selectedName} />,
          },
          {
            id: "events",
            label: "events",
            ready: true,
            content: <EventsView cluster={cluster} kind="pods" ns={selectedNs} name={selectedName} />,
          },
          {
            id: "logs",
            label: "logs",
            ready: true,
            content: <PodLogsTab cluster={cluster} ns={selectedNs} name={selectedName} />,
          },
        ]}
        actions={
          <OpenShellButton
            cluster={cluster}
            namespace={selectedNs}
            pod={selectedName}
          />
        }
      />
    ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Pods"
        subtitle={
          podsQuery.isSuccess
            ? `${allPods.length} ${allPods.length === 1 ? "pod" : "pods"}${
                namespace ? ` in ${namespace}` : ""
              }`
            : undefined
        }
        chips={[
          { label: "failing", count: failing, tone: "red", active: status === "Failed", onClick: () => setParam("status", status === "Failed" ? null : "Failed") },
          { label: "pending", count: pending, tone: "yellow", active: status === "Pending", onClick: () => setParam("status", status === "Pending" ? null : "Pending") },
        ]}
        trailing={<NamespacePicker />}
      />
      <FilterStrip
        search={search}
        onSearch={(v) => setParam("q", v)}
        statusFilter={status}
        statusOptions={["Running", "Pending", "Failed"]}
        onStatusFilter={(v) => setParam("status", v)}
        resultCount={filtered.length}
        totalCount={allPods.length}
      />
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          podsQuery.isLoading ? (
            <LoadingState resource="pods" />
          ) : podsQuery.isError ? (
            <ErrorState title="couldn't reach the cluster" message={(podsQuery.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="pods" namespace={namespace} />
          ) : (
            <DataTable<Pod>
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
