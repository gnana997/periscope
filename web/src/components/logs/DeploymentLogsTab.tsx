import { useState } from "react";
import { DeploymentLogsView } from "./DeploymentLogsView";
import { DEFAULT_LOGS_STATE, type LogsViewState } from "./logsState";

// DeploymentLogsTab is the in-detail-pane mount of the deployment logs
// viewer. It owns local state (NOT URL state) so it doesn't fight the
// deployments-list page over shared `?q=`/`?ns=` query params. Switching
// between deployments unmounts and resets state — the URL-driven full
// page route exists for sharing/persistence.
export function DeploymentLogsTab({
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
    <DeploymentLogsView
      cluster={cluster}
      namespace={ns}
      name={name}
      state={state}
      onStateChange={(next) => setState((s) => ({ ...s, ...next }))}
      showExpand
    />
  );
}
