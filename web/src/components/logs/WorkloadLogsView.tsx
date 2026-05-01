import { useEffect } from "react";
import {
  useDaemonSetDetail,
  useDeploymentDetail,
  useJobDetail,
  useStatefulSetDetail,
} from "../../hooks/useResource";
import { useLogStream } from "../../hooks/useLogStream";
import { LogToolbar } from "./LogToolbar";
import { LogStream } from "./LogStream";
import { PodFilterStrip, type WorkloadKind } from "./PodFilterStrip";
import type { LogsViewState } from "./logsState";
import { buildWorkloadLogsPath } from "./logsState";

export interface WorkloadLogsViewProps {
  kind: WorkloadKind;
  cluster: string;
  namespace: string;
  name: string;
  state: LogsViewState;
  onStateChange: (next: Partial<LogsViewState>) => void;
  // When true, toolbar shows an "↗ expand" link to the full-page route.
  // Suppress on the full page itself (we ARE the page).
  showExpand?: boolean;
}

// useWorkloadContainers calls every detail hook unconditionally (rules of
// hooks) but only enables the one matching `kind` (passing name=null is
// the convention for "don't fetch"). Returns the container-name list once
// the active hook resolves.
function useWorkloadContainers(
  kind: WorkloadKind,
  cluster: string,
  namespace: string,
  name: string,
): string[] {
  const dep = useDeploymentDetail(cluster, namespace, kind === "deployment" ? name : null);
  const sts = useStatefulSetDetail(cluster, namespace, kind === "statefulset" ? name : null);
  const ds = useDaemonSetDetail(cluster, namespace, kind === "daemonset" ? name : null);
  const job = useJobDetail(cluster, namespace, kind === "job" ? name : null);

  switch (kind) {
    case "deployment":
      return (dep.data?.containers ?? []).map((c) => c.name);
    case "statefulset":
      return (sts.data?.containers ?? []).map((c) => c.name);
    case "daemonset":
      return (ds.data?.containers ?? []).map((c) => c.name);
    case "job":
      return (job.data?.containers ?? []).map((c) => c.name);
  }
}

export function WorkloadLogsView({
  kind,
  cluster,
  namespace,
  name,
  state,
  onStateChange,
  showExpand,
}: WorkloadLogsViewProps) {
  const containers = useWorkloadContainers(kind, cluster, namespace, name);

  // Resolve container: explicit selection > first in template.
  const resolved = state.container || containers[0] || "";

  // Once the workload loads, write the resolved container into state so
  // the URL/share view reflects what's actually streaming.
  useEffect(() => {
    if (!state.container && resolved) onStateChange({ container: resolved });
  }, [state.container, resolved, onStateChange]);

  const stream = useLogStream({
    source: { kind, cluster, namespace, name },
    container: resolved,
    tailLines: state.tailLines,
    sinceSeconds: state.sinceSeconds ?? undefined,
    previous: state.previous,
    follow: state.follow,
  });

  const pagePath = buildWorkloadLogsPath(kind, cluster, namespace, name, {
    ...state,
    container: resolved,
  });
  const shareUrl = `${window.location.origin}${pagePath}`;
  const expandTo = showExpand ? pagePath : undefined;

  const togglePod = (pod: string) => {
    const has = state.podFilter.includes(pod);
    onStateChange({
      podFilter: has
        ? state.podFilter.filter((p) => p !== pod)
        : [...state.podFilter, pod],
    });
  };

  return (
    <div className="flex h-full min-h-0 flex-col">
      <LogToolbar
        containers={containers}
        initContainers={[]}
        container={resolved}
        onContainerChange={(v) => onStateChange({ container: v })}
        tailLines={state.tailLines}
        onTailLinesChange={(v) => onStateChange({ tailLines: v })}
        sinceSeconds={state.sinceSeconds}
        onSinceSecondsChange={(v) => onStateChange({ sinceSeconds: v })}
        previous={state.previous}
        onPreviousChange={(v) => onStateChange({ previous: v })}
        follow={state.follow}
        onFollowChange={(v) => onStateChange({ follow: v })}
        timestamps={state.timestamps}
        onTimestampsChange={(v) => onStateChange({ timestamps: v })}
        wrap={state.wrap}
        onWrapChange={(v) => onStateChange({ wrap: v })}
        search={state.search}
        onSearchChange={(v) => onStateChange({ search: v })}
        status={stream.status}
        totalReceived={stream.totalReceived}
        overflowed={stream.overflowed}
        onReload={stream.reload}
        shareUrl={shareUrl}
        expandTo={expandTo}
      />

      <PodFilterStrip
        kind={kind}
        pods={stream.pods}
        selected={state.podFilter}
        onToggle={togglePod}
        onClear={() => onStateChange({ podFilter: [] })}
      />

      {stream.status === "error" && stream.error && (
        <div className="border-b border-red/30 bg-red-soft/30 px-5 py-2 font-mono text-[11.5px] text-red">
          {stream.error} — click reload to retry
        </div>
      )}

      <LogStream
        lines={stream.lines}
        search={state.search}
        wrap={state.wrap}
        timestamps={state.timestamps}
        follow={state.follow}
        podFilter={state.podFilter}
      />
    </div>
  );
}
