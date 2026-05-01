import { useState } from "react";
import { PodLogsView } from "./PodLogsView";
import { DEFAULT_LOGS_STATE, type LogsViewState } from "./logsState";

// PodLogsTab is the in-detail-pane mount of the logs viewer. It owns local
// state (NOT URL state) so it doesn't fight the pod-list page over the
// shared `?q=` / `?ns=` query params. Switching between pods unmounts and
// resets state, which is the expected behavior — the URL-driven full-page
// route exists for sharing/persistence.
export function PodLogsTab({
  cluster,
  ns,
  name,
}: {
  cluster: string;
  ns: string;
  name: string;
}) {
  const [state, setState] = useState<LogsViewState>(DEFAULT_LOGS_STATE);
  return (
    <PodLogsView
      cluster={cluster}
      namespace={ns}
      name={name}
      state={state}
      onStateChange={(next) => setState((s) => ({ ...s, ...next }))}
      showExpand
    />
  );
}
