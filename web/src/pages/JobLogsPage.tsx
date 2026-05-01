import { WorkloadLogsPage } from "./WorkloadLogsPage";

export function JobLogsPage({ cluster }: { cluster: string }) {
  return <WorkloadLogsPage kind="job" cluster={cluster} />;
}
