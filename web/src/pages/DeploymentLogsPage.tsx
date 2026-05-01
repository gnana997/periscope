import { WorkloadLogsPage } from "./WorkloadLogsPage";

export function DeploymentLogsPage({ cluster }: { cluster: string }) {
  return <WorkloadLogsPage kind="deployment" cluster={cluster} />;
}
