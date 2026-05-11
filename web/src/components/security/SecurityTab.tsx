// SecurityTab — the new `security` detail-pane tab (#166).
//
// One component, three entity shapes (discriminated union by `kind`):
//
//   { kind: "pod",      cluster, ns, name }          — pod detail pane
//   { kind: "instance", cluster, instanceId }        — node detail pane
//   { kind: "workload", cluster, workloadKind,       — Deployment/STS/DS
//                       ns, name }                     detail pane
//
// The header shows entity identity + verbose severity chip + scan
// coverage + scanned-at + a refresh button scoped to this entity.
// The body branches:
//   - pod      → per-container groups (each with its own findings list)
//   - instance → flat findings list + AMI/owner badge
//   - workload → deduped-by-digest container groups across replicas,
//                plus a per-pod replica chip list at the bottom.

import { useMemo, useState } from "react";
import {
  useCveByInstanceOne,
  useCveByWorkload,
  useCvePodDetail,
  useCveRefresh,
} from "../../hooks/useCve";
import { queryKeys } from "../../lib/queryKeys";
import type {
  CveContainerRow,
  CveFinding,
  CveScanCoverage,
} from "../../lib/types";
import { type ScanState } from "../../lib/severity";
import {
  type FindingFilters,
  NO_FILTERS,
  filterPackageGroup,
  isExploit,
  isFixable,
} from "../../lib/findingFilters";
import { PackageGroupBlock } from "./PackageGroupBlock";
import { FindingFilterChips } from "./FindingFilterChips";
import {
  collectDigests,
  collectDigestsAcrossPods,
  containerScanState,
  countSeverities,
  coverageToState,
  dedupContainersByDigest,
  humanizeAge,
} from "../../lib/cve";
import { cn } from "../../lib/cn";
import { DetailEmpty, DetailError, DetailLoading } from "../detail/states";
import { SectionTitle } from "../detail/describe/shared";
import { SeverityChip } from "./SeverityChip";
import { FindingRow } from "./FindingRow";

type SecurityTabProps =
  | {
      kind: "pod";
      cluster: string;
      ns: string;
      name: string;
    }
  | {
      kind: "instance";
      cluster: string;
      instanceId: string;
    }
  | {
      kind: "workload";
      cluster: string;
      workloadKind: string; // "Deployment" | "StatefulSet" | "DaemonSet"
      ns: string;
      name: string;
    };

export function SecurityTab(props: SecurityTabProps) {
  switch (props.kind) {
    case "pod":
      return <PodSecurityTab {...props} />;
    case "instance":
      return <InstanceSecurityTab {...props} />;
    case "workload":
      return <WorkloadSecurityTab {...props} />;
  }
}

// ── Pod ────────────────────────────────────────────────────────────

function PodSecurityTab({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const q = useCvePodDetail(cluster, ns, name);
  const refresh = useCveRefresh(cluster);
  const [filters, setFilters] = useState<FindingFilters>(NO_FILTERS);
  const totals = useMemo(
    () => computeFilterTotals(q.data?.containers ?? []),
    [q.data?.containers],
  );
  const visibleFindings = useMemo(
    () => countVisibleFindings(q.data?.containers ?? [], filters),
    [q.data?.containers, filters],
  );
  if (q.isLoading) {
    return <DetailLoading label="scanning for vulnerabilities…" />;
  }
  if (q.isError) {
    return (
      <DetailError message={(q.error as Error)?.message ?? "unknown error"} />
    );
  }
  if (!q.data) {
    return <DetailEmpty label="no findings" />;
  }
  const pod = q.data;
  const digests = collectDigests(pod.containers);
  const state = coverageToState(pod.scanCoverage);

  return (
    <div className="flex h-full flex-col">
      <Header
        title={`${pod.namespace}/${pod.name}`}
        coverage={pod.scanCoverage}
        state={state}
        counts={pod.rolledUpSeverityCounts}
        onRefresh={() =>
          refresh.mutate({
            digests,
            invalidate: [queryKeys.cluster(cluster).cve.podDetail(ns, name)],
          })
        }
        refreshing={refresh.isPending}
      />
      <FindingFilterChips
        totals={totals.counts}
        exploitCount={totals.exploitCount}
        fixableCount={totals.fixableCount}
        totalFindings={totals.totalFindings}
        visibleFindings={visibleFindings}
        filters={filters}
        onChange={setFilters}
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-3">
        {pod.containers.map((c) => (
          <ContainerBlock key={c.name} container={c} cluster={cluster} filters={filters} />
        ))}
      </div>
    </div>
  );
}

// ── Instance ───────────────────────────────────────────────────────

function InstanceSecurityTab({
  cluster,
  instanceId,
}: {
  cluster: string;
  instanceId: string;
}) {
  // Defensive: bare-metal / pre-Initialized NodeClaim → no instanceId.
  const q = useCveByInstanceOne(cluster, instanceId);
  const refresh = useCveRefresh(cluster);
  if (!instanceId) {
    return (
      <DetailEmpty label="no scan possible — instance not on a cloud provider Periscope can introspect" />
    );
  }
  if (q.isLoading) {
    return <DetailLoading label="scanning for vulnerabilities…" />;
  }
  if (q.isError) {
    return (
      <DetailError message={(q.error as Error)?.message ?? "unknown error"} />
    );
  }
  if (!q.data) {
    return <DetailEmpty label="no findings" />;
  }
  const findings = q.data.findings ?? [];
  const counts = countSeverities(findings);
  const state: ScanState =
    counts.critical + counts.high + counts.medium + counts.low > 0
      ? "has-findings"
      : "clean";
  return (
    <div className="flex h-full flex-col">
      <Header
        title={instanceId}
        coverage="full"
        state={state}
        counts={counts}
        onRefresh={() =>
          refresh.mutate({
            instanceIds: [instanceId],
            invalidate: [
              queryKeys.cluster(cluster).cve.byInstanceOne(instanceId),
              queryKeys.cluster(cluster).cve.byInstance(),
            ],
          })
        }
        refreshing={refresh.isPending}
        scannedAt={q.data.lastFetchedAt}
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-3">
        {findings.length === 0 ? (
          <DetailEmpty label="no findings on this instance" />
        ) : (
          <FindingsList findings={findings} />
        )}
      </div>
    </div>
  );
}

// ── Workload ───────────────────────────────────────────────────────

function WorkloadSecurityTab({
  cluster,
  workloadKind,
  ns,
  name,
}: {
  cluster: string;
  workloadKind: string;
  ns: string;
  name: string;
}) {
  const q = useCveByWorkload(cluster, workloadKind, ns, name);
  const refresh = useCveRefresh(cluster);
  const [filters, setFilters] = useState<FindingFilters>(NO_FILTERS);
  const wlContainers = useMemo(
    () => (q.data ? q.data.pods.flatMap((p) => p.containers) : []),
    [q.data],
  );
  const totals = useMemo(
    () => computeFilterTotals(wlContainers),
    [wlContainers],
  );
  const visibleFindings = useMemo(
    () => countVisibleFindings(wlContainers, filters),
    [wlContainers, filters],
  );
  const dedupedContainers = useMemo(
    () => (q.data ? dedupContainersByDigest(q.data.pods) : []),
    [q.data],
  );

  if (q.isLoading) {
    return <DetailLoading label="scanning for vulnerabilities…" />;
  }
  if (q.isError) {
    return (
      <DetailError message={(q.error as Error)?.message ?? "unknown error"} />
    );
  }
  if (!q.data) {
    return <DetailEmpty label="no findings" />;
  }
  const wl = q.data;
  if (wl.pods.length === 0) {
    return <DetailEmpty label="no running pods to scan" />;
  }
  const digests = collectDigestsAcrossPods(wl.pods);
  return (
    <div className="flex h-full flex-col">
      <Header
        title={`${ns}/${name} (${workloadKind})`}
        subtitle={`${wl.pods.length} ${wl.pods.length === 1 ? "pod" : "pods"}`}
        coverage={wl.scanCoverage}
        state={coverageToState(wl.scanCoverage)}
        counts={wl.rolledUpSeverityCounts}
        onRefresh={() =>
          refresh.mutate({
            digests,
            invalidate: [
              queryKeys.cluster(cluster).cve.byWorkload(workloadKind, ns, name),
            ],
          })
        }
        refreshing={refresh.isPending}
      />
      <FindingFilterChips
        totals={totals.counts}
        exploitCount={totals.exploitCount}
        fixableCount={totals.fixableCount}
        totalFindings={totals.totalFindings}
        visibleFindings={visibleFindings}
        filters={filters}
        onChange={setFilters}
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-3">
        <SectionTitle>Unique containers (deduped across pods)</SectionTitle>
        {dedupedContainers.length === 0 ? (
          <DetailEmpty label="no container images to scan" />
        ) : (
          dedupedContainers.map((c) => (
            <ContainerBlock
              key={`${c.name}/${c.digest ?? c.row.image}`}
              container={c.row}
              cluster={cluster}
              replicaCount={c.podCount}
              filters={filters}
            />
          ))
        )}

        <SectionTitle>Replicas</SectionTitle>
        <ul className="space-y-1 font-mono text-[12px]">
          {wl.pods.map((p) => (
            <li
              key={`${p.namespace}/${p.name}`}
              className="flex items-baseline justify-between gap-3 border-b border-border/40 py-1 last:border-b-0"
            >
              <span className="text-ink">{p.name}</span>
              <span className="flex items-center gap-3">
                <SeverityChip
                  mode="compact"
                  counts={p.rolledUpSeverityCounts}
                  state={coverageToState(p.scanCoverage)}
                />
                <a
                  className="text-ink-faint underline decoration-dotted underline-offset-2 hover:text-ink"
                  href={`/clusters/${cluster}/pods?sel=${encodeURIComponent(
                    p.name,
                  )}&selNs=${encodeURIComponent(p.namespace)}&tab=security`}
                >
                  open ↗
                </a>
              </span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

// ── Shared ─────────────────────────────────────────────────────────

function Header({
  title,
  subtitle,
  coverage,
  state,
  counts,
  onRefresh,
  refreshing,
  scannedAt,
}: {
  title: string;
  subtitle?: string;
  coverage: CveScanCoverage;
  state: ScanState;
  counts: CveFinding extends never
    ? never
    : import("../../lib/types").CveSeverityCounts;
  onRefresh: () => void;
  refreshing: boolean;
  scannedAt?: string;
}) {
  return (
    <div className="flex shrink-0 flex-col gap-1 border-b border-border bg-surface px-5 py-3 font-mono text-[12px]">
      <div className="flex items-baseline gap-3">
        <span className="truncate text-ink" title={title}>
          {title}
        </span>
        {subtitle && <span className="text-ink-faint">· {subtitle}</span>}
        <CoveragePill coverage={coverage} />
        <button
          type="button"
          onClick={onRefresh}
          disabled={refreshing}
          className={cn(
            "ml-auto rounded-md border border-border px-2 py-0.5 text-[11px] text-ink-muted transition-colors",
            "hover:bg-surface-2 hover:text-ink disabled:cursor-not-allowed disabled:opacity-50",
          )}
        >
          {refreshing ? "refreshing…" : "↻ refresh"}
        </button>
      </div>
      <SeverityChip
        mode="verbose"
        counts={counts}
        state={state}
        scannedAt={scannedAt ? humanizeAge(scannedAt) : undefined}
      />
    </div>
  );
}

function CoveragePill({ coverage }: { coverage: CveScanCoverage }) {
  const cls =
    coverage === "full"
      ? "border-border text-ink-muted"
      : coverage === "partial"
        ? "border-yellow/40 bg-yellow/10 text-yellow"
        : "border-border text-ink-faint";
  return (
    <span
      className={cn(
        "rounded-sm border px-1.5 py-0.5 text-[10px] uppercase tracking-[0.06em]",
        cls,
      )}
    >
      {coverage} scan
    </span>
  );
}

function ContainerBlock({
  container,
  replicaCount,
  filters,
}: {
  container: CveContainerRow;
  cluster: string;
  replicaCount?: number;
  filters: FindingFilters;
}) {
  // Server-side groups + sorts findings into package buckets
  // (internal/cve/findings_group.go). Listing endpoints omit
  // `packages` to keep their payload small; detail + by-workload
  // populate it. Client-side filters hide rows from view without
  // re-querying.
  const packages = container.packages ?? [];
  const filteredPackages = packages.map((g) => filterPackageGroup(g, filters));
  const firstWithFindings = filteredPackages.findIndex(
    (g) => g.findings.length > 0,
  );
  return (
    <div className="mb-3 border-l-2 border-border pl-3">
      <div className="flex items-baseline gap-3 font-mono text-[12px]">
        <span className="text-ink">container: {container.name}</span>
        {replicaCount && replicaCount > 1 && (
          <span className="text-ink-faint">× {replicaCount} pods</span>
        )}
      </div>
      <div className="mt-1 space-y-0.5 font-mono text-[11.5px] text-ink-muted">
        <div className="truncate">image: {container.image}</div>
        {container.digest && (
          <div className="truncate text-ink-faint">
            digest: {container.digest}
          </div>
        )}
        <div className="pt-0.5">
          <SeverityChip
            mode="compact"
            counts={
              container.severityCounts ?? {
                critical: 0,
                high: 0,
                medium: 0,
                low: 0,
                informational: 0,
              }
            }
            state={containerScanState(container)}
          />
        </div>
      </div>
      {container.scanState === "scanned" ? (
        packages.length === 0 ? (
          <div className="mt-2 text-[11px] text-ink-faint">no findings</div>
        ) : (
          <div className="mt-2">
            {filteredPackages.map((g, idx) => (
              <PackageGroupBlock
                key={g.packageName}
                group={g}
                totalCount={packages[idx].findings.length}
                defaultOpen={idx === firstWithFindings}
              />
            ))}
          </div>
        )
      ) : container.scanState === "non-ecr" ? (
        <div className="mt-2 text-[11.5px] text-ink-faint">
          Inspector v2 covers ECR images only — not scanned.
        </div>
      ) : (
        <div className="mt-2 text-[11.5px] text-ink-faint">
          scan pending — pod has not pulled the image yet.
        </div>
      )}
    </div>
  );
}

function FindingsList({ findings }: { findings: CveFinding[] }) {
  return (
    <div className="rounded-sm border border-border bg-surface-2/40">
      {findings.map((f, i) => (
        <FindingRow key={f.arn ?? `${f.cve}-${i}`} finding={f} />
      ))}
    </div>
  );
}



// ── Filter totals + visibility (helpers used by Pod + Workload) ────

interface FilterTotals {
  counts: { critical: number; high: number; medium: number; low: number; informational: number };
  exploitCount: number;
  fixableCount: number;
  totalFindings: number;
}

function computeFilterTotals(containers: readonly CveContainerRow[]): FilterTotals {
  const counts = { critical: 0, high: 0, medium: 0, low: 0, informational: 0 };
  let exploitCount = 0;
  let fixableCount = 0;
  let totalFindings = 0;
  for (const c of containers) {
    for (const g of c.packages ?? []) {
      for (const f of g.findings) {
        totalFindings++;
        if (isExploit(f)) exploitCount++;
        if (isFixable(f)) fixableCount++;
        const s = (f.severity ?? "").toUpperCase();
        if (s === "CRITICAL") counts.critical++;
        else if (s === "HIGH") counts.high++;
        else if (s === "MEDIUM") counts.medium++;
        else if (s === "LOW") counts.low++;
        else if (s === "INFORMATIONAL" || s === "INFO") counts.informational++;
      }
    }
  }
  return { counts, exploitCount, fixableCount, totalFindings };
}

function countVisibleFindings(
  containers: readonly CveContainerRow[],
  filters: FindingFilters,
): number {
  if (!filters.severity && !filters.exploitOnly && !filters.fixableOnly) {
    return computeFilterTotals(containers).totalFindings;
  }
  let n = 0;
  for (const c of containers) {
    for (const g of c.packages ?? []) {
      for (const f of g.findings) {
        if (filters.severity) {
          const got = (f.severity ?? "").toUpperCase();
          const norm = got === "INFO" ? "INFORMATIONAL" : got;
          if (norm !== filters.severity.toUpperCase()) continue;
        }
        if (filters.exploitOnly && !isExploit(f)) continue;
        if (filters.fixableOnly && !isFixable(f)) continue;
        n++;
      }
    }
  }
  return n;
}
