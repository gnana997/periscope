import { useEffect } from "react";
import { useDeploymentDetail } from "../../hooks/useResource";
import { useLogStream } from "../../hooks/useLogStream";
import { LogToolbar } from "./LogToolbar";
import { LogStream } from "./LogStream";
import { PodFilterStrip } from "./PodFilterStrip";
import type { LogsViewState } from "./logsState";
import { buildDeploymentLogsPath } from "./logsState";

export interface DeploymentLogsViewProps {
  cluster: string;
  namespace: string;
  name: string;
  state: LogsViewState;
  onStateChange: (next: Partial<LogsViewState>) => void;
  // When true, toolbar shows an "↗ expand" link to the full-page route.
  // Suppress on the full page itself (we ARE the page).
  showExpand?: boolean;
}

export function DeploymentLogsView({
  cluster,
  namespace,
  name,
  state,
  onStateChange,
  showExpand,
}: DeploymentLogsViewProps) {
  const detail = useDeploymentDetail(cluster, namespace, name);
  const containers = (detail.data?.containers ?? []).map((c) => c.name);

  // Resolve container: explicit selection > first in template.
  const resolved = state.container || containers[0] || "";

  // Once the deployment loads, write the resolved container into state so
  // the URL/share view reflects what's actually streaming.
  useEffect(() => {
    if (!state.container && resolved) onStateChange({ container: resolved });
  }, [state.container, resolved, onStateChange]);

  const stream = useLogStream({
    source: { kind: "deployment", cluster, namespace, name },
    container: resolved,
    tailLines: state.tailLines,
    sinceSeconds: state.sinceSeconds ?? undefined,
    previous: state.previous,
    follow: state.follow,
  });

  const pagePath = buildDeploymentLogsPath(cluster, namespace, name, {
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
