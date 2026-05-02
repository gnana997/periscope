import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { Node, NodeList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { SplitPane } from "../components/page/SplitPane";
import {
  DataTable,
  type Column,
  type RowTint,
} from "../components/table/DataTable";
import { StatusDot } from "../components/table/StatusDot";
import {
  EmptyState,
  ErrorState,
  ForbiddenState,
  isForbidden,
  LoadingState,
} from "../components/table/states";
import { DetailPane } from "../components/detail/DetailPane";
import { NodeDescribe } from "../components/detail/describe/NodeDescribe";
import { cn } from "../lib/cn";

function NodeStatusTag({ status }: { status: string }) {
  const tone =
    status === "Ready" ? "green" : status === "NotReady" ? "red" : "muted";
  const colorCls =
    tone === "green"
      ? "text-green"
      : tone === "red"
        ? "text-red"
        : "text-ink-muted";
  return (
    <span className={cn("inline-flex items-center gap-1.5", colorCls)}>
      <StatusDot tone={tone} />
      <span>{status}</span>
    </span>
  );
}

export function NodesPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const selectedName = params.get("sel");

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

  const query = useResource({ cluster, resource: "nodes" });
  const all =
    ((query.data as NodeList | undefined)?.nodes ?? []) as Node[];
  const filtered = useMemo(
    () => (search ? all.filter((n) => nameMatches(n.name, search)) : all),
    [all, search],
  );

  const columns: Column<Node>[] = [
    {
      key: "name",
      header: "name",
      weight: 3,
      cellClassName: "font-mono text-ink",
      accessor: (n) => n.name,
    },
    {
      key: "status",
      header: "status",
      weight: 1.2,
      accessor: (n) => <NodeStatusTag status={n.status} />,
    },
    {
      key: "roles",
      header: "roles",
      weight: 1.5,
      cellClassName: "font-mono text-ink-muted",
      accessor: (n) => n.roles.join(", "),
    },
    {
      key: "version",
      header: "version",
      weight: 1.5,
      cellClassName: "font-mono text-ink-muted",
      accessor: (n) => n.kubeletVersion,
    },
    {
      key: "ip",
      header: "ip",
      weight: 1.2,
      cellClassName: "font-mono text-ink-muted",
      accessor: (n) => n.internalIP,
    },
    {
      key: "cpu",
      header: "cpu",
      weight: 0.8,
      align: "right",
      cellClassName: "font-mono text-ink-muted",
      accessor: (n) => n.cpuCapacity,
    },
    {
      key: "mem",
      header: "mem",
      weight: 1,
      align: "right",
      cellClassName: "font-mono text-ink-muted",
      accessor: (n) => n.memoryCapacity,
    },
    {
      key: "age",
      header: "age",
      weight: 0.5,
      align: "right",
      cellClassName: "font-mono text-ink-muted",
      accessor: (n) => ageFrom(n.createdAt),
    },
  ];

  const rowTint = (n: Node): RowTint =>
    n.status === "NotReady" ? "red" : null;

  const detail = selectedName ? (
    <DetailPane
      title={selectedName}
      subtitle="cluster-scoped"
      activeTab="describe"
      onTabChange={() => {}}
      onClose={() => setMany({ sel: null })}
      tabs={[
        {
          id: "describe",
          label: "describe",
          ready: true,
          content: <NodeDescribe cluster={cluster} name={selectedName} />,
        },
      ]}
    />
  ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Nodes"
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "node" : "nodes"}`
            : undefined
        }
      />
      <div className="flex items-center gap-2 border-b border-border bg-bg px-6 py-2.5">
        <div className="flex min-w-[240px] flex-1 items-center gap-2 rounded-md border border-border bg-surface px-3 py-1.5 text-[12.5px] focus-within:border-border-strong">
          <svg width="13" height="13" viewBox="0 0 13 13" className="text-ink-faint" aria-hidden>
            <circle cx="5.5" cy="5.5" r="3.6" stroke="currentColor" strokeWidth="1.3" fill="none" />
            <path d="M8.3 8.3l2.4 2.4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" />
          </svg>
          <input
            value={search}
            onChange={(e) => setParam("q", e.target.value)}
            placeholder="filter by name"
            className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink outline-none placeholder:text-ink-faint"
          />
        </div>
        <div className={cn("ml-auto font-mono text-[11px] text-ink-muted tabular")}>
          {filtered.length}
          <span className="text-ink-faint"> / </span>
          {all.length}
        </div>
      </div>
      <SplitPane
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? (
            <LoadingState resource="nodes" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="nodes" /> : isForbidden(query.error) ? <ForbiddenState resource="nodes" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="nodes" namespace={null} />
          ) : (
            <DataTable<Node>
              columns={columns}
              rows={filtered}
              rowKey={(n) => n.name}
              rowTint={rowTint}
              onRowClick={(n) => setMany({ sel: n.name })}
              selectedKey={selectedName}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
