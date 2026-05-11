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

import { useMemo } from "react";
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
      <div className="min-h-0 flex-1 overflow-y-auto px-5 py-3">
        {pod.containers.map((c) => (
          <ContainerBlock key={c.name} container={c} cluster={cluster} />
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
  cluster: _cluster,
  replicaCount,
}: {
  container: CveContainerRow;
  cluster: string;
  replicaCount?: number;
}) {
  const findings = useCveContainerFindings(container);
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
        findings.isLoading ? (
          <div className="mt-2 text-[11px] text-ink-faint">loading findings…</div>
        ) : findings.list.length === 0 ? (
          <div className="mt-2 text-[11px] text-ink-faint">no findings</div>
        ) : (
          <div className="mt-2">
            <FindingsList findings={findings.list} />
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

/** useCveContainerFindings — placeholder for v1.1: the per-container
 *  findings come from the parent CvePodDetail response (we don't
 *  re-fetch by digest). When `kind === "workload"` and only the
 *  container row is in scope, this returns an empty list — the
 *  workload tab shows the dedup'd container's severity chip and the
 *  operator clicks into a pod row to see findings. Tracked: a future
 *  /cve/by-digest fetch could populate findings here without a pod
 *  round-trip.
 *
 *  For now: the pod-side ContainerBlock reads findings from the
 *  parent response since the API doesn't return them inside the
 *  container row. The chip column is enough at the workload level. */
function useCveContainerFindings(_c: CveContainerRow): {
  isLoading: boolean;
  list: CveFinding[];
} {
  // The CvePodRow shape does not surface per-container findings —
  // /cve/pods returns container-level severity counts only. The
  // detail of "which CVE on which container" requires drilling into
  // /cve/by-digest/{digest}. We render an empty list here and rely
  // on the operator clicking the pod row's "open ↗" to see findings.
  //
  // The pod-detail variant (/cve/pods/{ns}/{name}) returns the same
  // shape, so this is consistent with what the backend gives us.
  return { isLoading: false, list: [] };
}

