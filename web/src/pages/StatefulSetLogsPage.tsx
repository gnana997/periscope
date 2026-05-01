import { WorkloadLogsPage } from "./WorkloadLogsPage";

export function StatefulSetLogsPage({ cluster }: { cluster: string }) {
  return <WorkloadLogsPage kind="statefulset" cluster={cluster} />;
}
