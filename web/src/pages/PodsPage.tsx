import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";
import { useResource } from "../hooks/useResource";
import type { Pod, PodList } from "../lib/types";
import { ageFrom, nameMatches } from "../lib/format";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import { SplitPane } from "../components/page/SplitPane";
import {
  type Column,
  type RowTint,
} from "../components/table/DataTable";
import { SelectableDataTable } from "../components/table/SelectableDataTable";
import { api } from "../lib/api";
import { PhaseTag } from "../components/table/StatusDot";
import { phaseTone } from "../components/table/phaseTone";
import {
  EmptyState,
  ErrorState,
  ForbiddenState,
  LoadingState,
} from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { DetailPane } from "../components/detail/DetailPane";
import { PodDescribe } from "../components/detail/describe/PodDescribe";
import { OpenShellButton } from "../components/exec/OpenShellButton";
import { YamlView } from "../components/detail/YamlView";
import { useEditorDirty } from "../hooks/useEditorDirty";
import { useConfirmDiscard } from "../hooks/useConfirmDiscard";
import { ResourceActions } from "../components/edit/ResourceActions";
import { EventsView } from "../components/detail/EventsView";
import { PodLogsTab } from "../components/logs/PodLogsTab";
import { NamespacePicker } from "../components/shell/NamespacePicker";
import { SecurityTab } from "../components/security/SecurityTab";
import { SecurityEmptyBanner } from "../components/security/SecurityEmptyBanner";
import { useCvePodSummaries } from "../hooks/useCve";
import { countVulnerable, podKey } from "../lib/cve";
import { SeverityChip } from "../components/security/SeverityChip";

import { cn } from "../lib/cn";

export function PodsPage({ cluster }: { cluster: string }) {
  const [params, setParams] = useSearchParams();
  const namespace = params.get("ns");
  const search = params.get("q") ?? "";
  const status = params.get("status");
  const selectedNs = params.get("selNs");
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

  const podsQuery = useResource({
    cluster,
    resource: "pods",
    namespace: namespace ?? undefined,
  });

  const cveSummaries = useCvePodSummaries(cluster);
  const cveByPod = cveSummaries.data;

  const allPods = useMemo<Pod[]>(
    () => (podsQuery.data as PodList | undefined)?.pods ?? [],
    [podsQuery.data],
  );

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

  const vulnerable = useMemo(() => countVulnerable(allPods, cveByPod), [allPods, cveByPod]);
  const filtered = useMemo(() => {
    let r = allPods;
    if (search) r = r.filter((p) => nameMatches(p.name, search));
    if (status === "Failed")
      r = r.filter((p) =>
        phaseTone(p.phase) === "red",
      );
    else if (status === "Running") r = r.filter((p) => phaseTone(p.phase) === "green");
    if (vulnOnly && cveByPod) {
      r = r.filter((p) => {
        const s = cveByPod.get(podKey(p.namespace, p.name));
        return !!s && (s.counts.critical > 0 || s.counts.high > 0);
      });
    }
    return r;
  }, [allPods, search, status, vulnOnly, cveByPod]);

  const selectedKey =
    selectedNs && selectedName ? `${selectedNs}/${selectedName}` : null;

  const columns: Column<Pod>[] = [
    { key: "name", header: "name", weight: 3, cellClassName: "font-mono text-ink", accessor: (p) => p.name },
    { key: "namespace", header: "namespace", weight: 1.4, cellClassName: "font-mono text-ink-muted", accessor: (p) => p.namespace },
    { key: "phase", header: "phase", weight: 1.2, accessor: (p) => <PhaseTag phase={p.phase} /> },
    {
      key: "image",
      header: "image",
      weight: 2.2,
      cellClassName: "font-mono text-ink-muted",
      accessor: (p) => {
        if (!p.image) return "—";
        return (
          <span className="block truncate" title={p.image}>
            {p.image}
            {p.imageCount && p.imageCount > 1 && (
              <span className="ml-1 text-ink-faint">+{p.imageCount - 1}</span>
            )}
          </span>
        );
      },
    },
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
    {
      key: "qos",
      header: "qos",
      weight: 0.8,
      cellClassName: "font-mono text-[10.5px]",
      accessor: (p) => {
        if (!p.qos) return "—";
        const tone =
          p.qos === "BestEffort"
            ? "text-yellow"
            : p.qos === "Guaranteed"
              ? "text-green"
              : "text-ink-muted";
        return <span className={tone}>{p.qos.toLowerCase()}</span>;
      },
    },
    {
      key: "vuln",
      header: "vulnerabilities",
      weight: 1.4,
      cellClassName: "font-mono",
      accessor: (p) => {
        const s = cveByPod?.get(podKey(p.namespace, p.name));
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
            state={
              isClean
                ? s.coverage === "none"
                  ? "non-ecr"
                  : s.coverage === "partial"
                    ? "partial"
                    : "clean"
                : "has-findings"
            }
          />
        );
      },
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

  const editFlag = useEditorDirty(cluster, "pods", selectedNs ?? undefined, selectedName);
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
            content: <YamlView cluster={cluster} source={{ kind: "builtin", yamlKind: "pods" }} ns={selectedNs} name={selectedName} />,
            dirty: editFlag.dirty,
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
          {
            id: "security",
            label: "security",
            ready: true,
            content: (
              <SecurityTab
                kind="pod"
                cluster={cluster}
                ns={selectedNs}
                name={selectedName}
              />
            ),
          },
        ]}
        actions={
          <ResourceActions
            cluster={cluster}
            source={{ kind: "builtin", yamlKind: "pods" }}
            namespace={selectedNs}
            name={selectedName}
            onDeleted={() => setParam("sel", null)}
            trailing={
              <OpenShellButton
                cluster={cluster}
                namespace={selectedNs}
                pod={selectedName}
              />
            }
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
          {
            label: "vulnerable",
            count: vulnerable,
            tone: "red",
            active: vulnOnly,
            onClick: () => setParam("vuln", vulnOnly ? null : "1"),
          },
        ]}
        streamStatus={podsQuery.streamStatus}
        trailing={<NamespacePicker />}
      />
      <SecurityEmptyBanner cluster={cluster} />
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
            isForbidden(podsQuery.error) ? <ForbiddenState resource="pods" /> : <ErrorState title="couldn't reach the cluster" message={(podsQuery.error as Error).message} />
          ) : filtered.length === 0 ? (
            <EmptyState resource="pods" namespace={namespace} />
          ) : (
            <SelectableDataTable<Pod>
              columns={columns}
              rows={filtered}
              rowKey={(p) => `${p.namespace}/${p.name}`}
              rowTint={rowTint}
              onRowClick={(p) => confirmDiscard(() => setMany({ sel: p.name, selNs: p.namespace, tab: "describe" }))}
              selectedKey={selectedKey}
              bulk={{
                cluster,
                kindLabel: "pods",
                fetchYaml: (p, signal) => api.yaml(cluster, "pods", p.namespace, p.name, signal),
              }}
            />
          )
        }
        right={detail}
      />
    </div>
  );
}
