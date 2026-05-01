import { useState } from "react";
import { WorkloadLogsView } from "./WorkloadLogsView";
import type { WorkloadKind } from "./PodFilterStrip";
import { DEFAULT_LOGS_STATE, type LogsViewState } from "./logsState";

// WorkloadLogsTab is the in-detail-pane mount of the multi-pod logs viewer
// for any controller kind. Local state (NOT URL state) — switching between
// list rows unmounts and resets state. The URL-driven full-page route
// exists for sharing/persistence.
export function WorkloadLogsTab({
  kind,
  cluster,
  ns,
  name,
}: {
  kind: WorkloadKind;
  cluster: string;
  ns: string;
  name: string;
}) {
  const [state, setState] = useState<LogsViewState>(DEFAULT_LOGS_STATE);
  return (
    <WorkloadLogsView
      kind={kind}
      cluster={cluster}
      namespace={ns}
      name={name}
      state={state}
      onStateChange={(next) => setState((s) => ({ ...s, ...next }))}
      showExpand
    />
  );
}
