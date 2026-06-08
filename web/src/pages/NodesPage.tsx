import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { Node, NodeList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { DetailOverlay } from "../components/page/DetailOverlay";
import { buildOverlayNav } from "../components/page/detailOverlayHelpers";
import {
  type Column,
  type RowTint,
} from "../components/table/DataTable";
import { SelectableDataTable } from "../components/table/SelectableDataTable";
import { api } from "../lib/api";
import { StatusDot } from "../components/table/StatusDot";
import {
  EmptyState,
  ErrorState,
  ForbiddenState,
  LoadingState,
} from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { NodeDescribe } from "../components/detail/describe/NodeDescribe";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { useConfirmDiscard } from "../hooks/useConfirmDiscard";
import { ResourceActions } from "../components/edit/ResourceActions";
import { OpenNodeShellButton } from "../components/exec/OpenNodeShellButton";
import { SecurityTab } from "../components/security/SecurityTab";
import { SecurityEmptyBanner } from "../components/security/SecurityEmptyBanner";
import { SeverityChip } from "../components/security/SeverityChip";
import { useCveByInstance } from "../hooks/useCve";
import { extractInstanceId } from "../lib/cve";
import { cn } from "../lib/cn";


function nodeByName(name: string | null, all: Node[]): Node | undefined {
  if (!name) return undefined;
  return all.find((n) => n.name === name);
}

function NodeStatusTag({
  status,
  unschedulable,
}: {
  status: string;
  unschedulable: boolean;
}) {
  // NotReady wins over Cordoned for the dot tone — both are abnormal,
  // but a NotReady node is a louder operational signal. Cordoned is
  // surfaced as a follow-on badge so the operator can still see it.
  const tone =
    status === "Ready" ? "green" : status === "NotReady" ? "red" : "muted";
  const colorCls =
    tone === "green"
      ? "text-green"
      : tone === "red"
        ? "text-red"
        : "text-ink-muted";
  return (
    <span className="inline-flex items-center gap-2">
      <span className={cn("inline-flex items-center gap-1.5", colorCls)}>
        <StatusDot tone={tone} />
        <span>{status}</span>
      </span>
      {unschedulable && (
        <span className="rounded-sm border border-yellow/40 bg-yellow/10 px-1.5 py-px font-mono text-[10.5px] uppercase tracking-[0.04em] text-yellow">
          cordoned
        </span>
      )}
    </span>
  );
}

export function NodesPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const search = params.get("q") ?? "";
  const selectedName = params.get("sel");
  const activeTab = params.get("tab") ?? "describe";
  const vulnOnly = params.get("vuln") === "1";

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
  const cveInstances = useCveByInstance(cluster);
  const cveByInstance = useMemo(() => {
    const m = new Map<string, { counts: import("../lib/types").CveSeverityCounts; lastFetchedAt: string }>();
    if (cveInstances.data) {
      for (const inst of cveInstances.data.instances) {
        m.set(inst.instanceId, { counts: inst.severityCounts, lastFetchedAt: inst.lastFetchedAt });
      }
    }
    return m;
  }, [cveInstances.data]);
  const all = useMemo<Node[]>(() => (query.data as NodeList | undefined)?.nodes ?? [], [query.data]);
  const vulnerable = useMemo(() => {
    if (!cveByInstance.size) return 0;
    let n = 0;
    for (const node of all) {
      const id = extractInstanceId(node.providerID);
      if (!id) continue;
      const s = cveByInstance.get(id);
      if (s && (s.counts.critical > 0 || s.counts.high > 0)) n++;
    }
    return n;
  }, [all, cveByInstance]);
  const filtered = useMemo(() => {
    let r = search ? all.filter((n) => nameMatches(n.name, search)) : all;
    if (vulnOnly) {
      r = r.filter((n) => {
        const id = extractInstanceId(n.providerID);
        if (!id) return false;
        const s = cveByInstance.get(id);
        return !!s && (s.counts.critical > 0 || s.counts.high > 0);
      });
    }
    return r;
  }, [all, search, vulnOnly, cveByInstance]);

  // Nodes are cluster-scoped — pass undefined ns to useEditorDirty and
  // an empty ns string to YamlView. The confirm gate guards the YAML
  // editor's unsaved changes on cycle / dismiss / tab switch.
  const editFlag = useEditorDirty(cluster, "nodes", undefined, selectedName);
  const confirmDiscard = useConfirmDiscard(editFlag.dirty);

  const overlayNav = buildOverlayNav({
    rows: filtered,
    selectedKey: selectedName,
    keyOf: (n) => n.name,
    navigateTo: (n) => confirmDiscard(() => setMany({ sel: n.name, tab: activeTab })),
    dismiss: () => confirmDiscard(() => setMany({ sel: null, tab: null })),
  });

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
      accessor: (n) => (
        <NodeStatusTag status={n.status} unschedulable={n.unschedulable} />
      ),
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
      key: "instance",
      header: "instance",
      weight: 1.8,
      cellClassName: "font-mono text-ink-muted",
      accessor: (n) => {
        if (!n.instanceType && !n.zone) return "—";
        const isSpot = n.capacityType === "SPOT" || n.capacityType === "spot";
        return (
          <span className="flex items-baseline gap-1.5">
            <span>{n.instanceType || "—"}</span>
            {n.zone && (
              <span className="text-ink-faint">·&nbsp;{n.zone}</span>
            )}
            {isSpot && (
              <span className="rounded border border-yellow/40 bg-yellow-soft px-1 py-px text-[9.5px] uppercase tracking-wide text-yellow">
                spot
              </span>
            )}
          </span>
        );
      },
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
      key: "vuln",
      header: "vulnerabilities",
      weight: 1.4,
      cellClassName: "font-mono",
      accessor: (n) => {
        const id = extractInstanceId(n.providerID);
        if (!id) {
          return (
            <SeverityChip
              mode="compact"
              counts={{ critical: 0, high: 0, medium: 0, low: 0, informational: 0 }}
              state="unscanned"
            />
          );
        }
        const s = cveByInstance.get(id);
        if (!s) {
          return (
            <SeverityChip
              mode="compact"
              counts={{ critical: 0, high: 0, medium: 0, low: 0, informational: 0 }}
              state="unscanned"
            />
          );
        }
        const isClean =
          s.counts.critical + s.counts.high + s.counts.medium + s.counts.low === 0;
        return (
          <SeverityChip
            mode="compact"
            counts={s.counts}
            state={isClean ? "clean" : "has-findings"}
          />
        );
      },
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

  const rowTint = (n: Node): RowTint => {
    // NotReady wins over cordoned — a broken node is louder than an
    // intentionally-cordoned one. Cordoned still gets a yellow tint
    // so it's visible at a scroll glance.
    if (n.status === "NotReady") return "red";
    if (n.unschedulable) return "yellow";
    return null;
  };

  const detail = selectedName ? (
    <DetailPane
      title={selectedName}
      subtitle="cluster-scoped"
      activeTab={activeTab}
      onTabChange={(id) => confirmDiscard(() => setParam("tab", id))}
      onClose={() => confirmDiscard(() => setMany({ sel: null, tab: null }))}
      actions={
        <>
          <OpenNodeShellButton cluster={cluster} node={selectedName} />
          <ResourceActions
            cluster={cluster}
            source={{ kind: "builtin", yamlKind: "nodes" }}
            namespace={null}
            name={selectedName}
            onDeleted={() => setMany({ sel: null })}
          />
        </>
      }
      tabs={[
        {
          id: "describe",
          label: "describe",
          ready: true,
          content: <NodeDescribe cluster={cluster} name={selectedName} />,
        },
        {
          id: "yaml",
          label: "yaml",
          ready: true,
          content: (
            <YamlView
              cluster={cluster}
              source={{ kind: "builtin", yamlKind: "nodes" }}
              ns=""
              name={selectedName}
            />
          ),
          dirty: editFlag.dirty,
        },
        {
          id: "security",
          label: "security",
          ready: true,
          content: (
            <SecurityTab
              kind="instance"
              cluster={cluster}
              instanceId={extractInstanceId(nodeByName(selectedName, all)?.providerID)}
            />
          ),
        },
      ]}
    />
  ) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader
        title="Nodes"
        chips={
          vulnerable > 0 || vulnOnly
            ? [
                {
                  label: "vulnerable",
                  count: vulnerable,
                  tone: "red" as const,
                  active: vulnOnly,
                  onClick: () => setParam("vuln", vulnOnly ? null : "1"),
                },
              ]
            : undefined
        }
        subtitle={
          query.isSuccess
            ? `${all.length} ${all.length === 1 ? "node" : "nodes"}`
            : undefined
        }
      />
      <SecurityEmptyBanner cluster={cluster} />
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
      <DetailOverlay {...overlayNav}
        storageKey="periscope.detailWidth.v4"
        left={
          query.isLoading ? (
            <LoadingState resource="nodes" />
          ) : query.isError ? (
            isForbidden(query.error) ? <ForbiddenState resource="nodes" /> : isForbidden(query.error) ? <ForbiddenState resource="nodes" /> : <ErrorState title="couldn't reach the cluster" message={(query.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="nodes" namespace={null} />
          ) : (
            <SelectableDataTable<Node>
              columns={columns}
              rows={filtered}
              rowKey={(n) => n.name}
              rowTint={rowTint}
              onRowClick={(n) => confirmDiscard(() => setMany({ sel: n.name, tab: "describe" }))}
              selectedKey={selectedName}
              bulk={{
                cluster,
                kindLabel: "nodes",
                fetchYaml: (n, signal) => api.clusterScopedYaml(cluster, "nodes", n.name, signal),
              }}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
