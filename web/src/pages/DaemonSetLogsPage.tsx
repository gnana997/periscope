import { WorkloadLogsPage } from "./WorkloadLogsPage";

export function DaemonSetLogsPage({ cluster }: { cluster: string }) {
  return <WorkloadLogsPage kind="daemonset" cluster={cluster} />;
}
