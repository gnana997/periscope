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
import { PhaseTag } from "../components/table/StatusDot";
import {
  EmptyState,
  ErrorState,
  LoadingState,
} from "../components/table/states";
import { DetailPane } from "../components/detail/DetailPane";
import { NamespacePicker } from "../components/shell/NamespacePicker";
import { cn } from "../lib/cn";

export function PodsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const namespace = params.get("ns");
  const search = params.get("q") ?? "";
  const status = params.get("status");
  const selectedName = params.get("sel");

  const setParam = (key: string, value: string | null) => {
    const next = new URLSearchParams(params);
    if (value === null || value === "") next.delete(key);
    else next.set(key, value);
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
        ["Failed", "CrashLoopBackOff"].includes(p.phase),
      ).length,
    [allPods],
  );
  const pending = useMemo(
    () => allPods.filter((p) => p.phase === "Pending").length,
    [allPods],
  );

  const filtered = useMemo(() => {
    let r = allPods;
    if (search) r = r.filter((p) => nameMatches(p.name, search));
    if (status === "Failed")
      r = r.filter((p) =>
        ["Failed", "CrashLoopBackOff"].includes(p.phase),
      );
    else if (status) r = r.filter((p) => p.phase === status);
    return r;
  }, [allPods, search, status]);

  const selected = selectedName
    ? allPods.find((p) => p.name === selectedName) ?? null
    : null;

  const columns: Column<Pod>[] = [
    {
      key: "name",
      header: "name",
      weight: 3,
      cellClassName: "font-mono text-ink",
      accessor: (p) => p.name,
    },
    {
      key: "namespace",
      header: "namespace",
      weight: 1.4,
      cellClassName: "font-mono text-ink-muted",
      accessor: (p) => p.namespace,
    },
    {
      key: "phase",
      header: "phase",
      weight: 1.2,
      accessor: (p) => <PhaseTag phase={p.phase} />,
    },
    {
      key: "ready",
      header: "ready",
      weight: 0.6,
      align: "right",
      cellClassName: "font-mono",
      accessor: (p) => (
        <span
          className={cn(
            (() => {
              const [r, t] = p.ready.split("/").map((n) => parseInt(n, 10));
              return r < t ? "text-yellow" : "text-ink";
            })(),
          )}
        >
          {p.ready}
        </span>
      ),
    },
    {
      key: "restarts",
      header: "restarts",
      weight: 0.7,
      align: "right",
      cellClassName: "font-mono",
      accessor: (p) => (
        <span
          className={
            p.restarts > 5
              ? "text-red"
              : p.restarts > 0
                ? "text-yellow"
                : "text-ink-muted"
          }
        >
          {p.restarts}
        </span>
      ),
    },
    {
      key: "node",
      header: "node",
      weight: 1.6,
      cellClassName: "font-mono text-ink-muted",
      accessor: (p) => p.nodeName ?? "—",
    },
    {
      key: "ip",
      header: "pod ip",
      weight: 1.1,
      cellClassName: "font-mono text-ink-muted",
      accessor: (p) => p.podIP ?? "—",
    },
    {
      key: "age",
      header: "age",
      weight: 0.6,
      align: "right",
      cellClassName: "font-mono text-ink-muted",
      accessor: (p) => ageFrom(p.createdAt),
    },
  ];

  const rowTint = (p: Pod): RowTint => {
    if (["Failed", "CrashLoopBackOff"].includes(p.phase)) return "red";
    if (p.phase === "Pending") return "yellow";
    return null;
  };

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
          {
            label: "failing",
            count: failing,
            tone: "red",
            active: status === "Failed",
            onClick: () =>
              setParam("status", status === "Failed" ? null : "Failed"),
          },
          {
            label: "pending",
            count: pending,
            tone: "yellow",
            active: status === "Pending",
            onClick: () =>
              setParam("status", status === "Pending" ? null : "Pending"),
          },
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
        left={
          podsQuery.isLoading ? (
            <LoadingState resource="pods" />
          ) : podsQuery.isError ? (
            <ErrorState
              title="couldn't reach the cluster"
              message={(podsQuery.error as Error).message}
            />
          ) : filtered.length === 0 ? (
            <EmptyState resource="pods" namespace={namespace} />
          ) : (
            <DataTable<Pod>
              columns={columns}
              rows={filtered}
              rowKey={(p) => `${p.namespace}/${p.name}`}
              rowTint={rowTint}
              onRowClick={(p) => setParam("sel", p.name)}
              selectedKey={
                selected
                  ? `${selected.namespace}/${selected.name}`
                  : null
              }
            />
          )
        }
        right={
          selected ? (
            <DetailPane pod={selected} onClose={() => setParam("sel", null)} />
          ) : null
        }
      />
    </div>
  );
}
