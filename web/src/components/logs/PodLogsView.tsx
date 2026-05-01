import { useEffect } from "react";
import { usePodDetail } from "../../hooks/useResource";
import { useLogStream } from "../../hooks/useLogStream";
import { LogToolbar } from "./LogToolbar";
import { LogStream } from "./LogStream";
import type { LogsViewState } from "./logsState";
import { buildLogsPagePath } from "./logsState";

export interface PodLogsViewProps {
  cluster: string;
  namespace: string;
  name: string;
  state: LogsViewState;
  onStateChange: (next: Partial<LogsViewState>) => void;
  // When true, toolbar shows an "↗ expand" link to the full-page route.
  // Suppress on the full page itself (we ARE the page).
  showExpand?: boolean;
}

export function PodLogsView({
  cluster,
  namespace,
  name,
  state,
  onStateChange,
  showExpand,
}: PodLogsViewProps) {
  const podDetail = usePodDetail(cluster, namespace, name);

  const containers = (podDetail.data?.containers ?? []).map((c) => c.name);
  const initContainers = (podDetail.data?.initContainers ?? []).map((c) => c.name);

  // Resolve container: explicit selection > first regular > first init.
  const resolved =
    state.container || containers[0] || initContainers[0] || "";

  // Once the pod loads, write the resolved container into state so the
  // URL/share view reflects what's actually streaming.
  useEffect(() => {
    if (!state.container && resolved) onStateChange({ container: resolved });
  }, [state.container, resolved, onStateChange]);

  const stream = useLogStream({
    cluster,
    namespace,
    name,
    container: resolved,
    tailLines: state.tailLines,
    sinceSeconds: state.sinceSeconds ?? undefined,
    previous: state.previous,
    follow: state.follow,
  });

  const pagePath = buildLogsPagePath(cluster, namespace, name, {
    ...state,
    container: resolved,
  });
  const shareUrl = `${window.location.origin}${pagePath}`;
  const expandTo = showExpand ? pagePath : undefined;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <LogToolbar
        containers={containers}
        initContainers={initContainers}
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
      />
    </div>
  );
}
