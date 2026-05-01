import { useEffect } from "react";
import {
  Navigate,
  Outlet,
  Route,
  Routes,
  useNavigate,
  useParams,
} from "react-router-dom";
import { Sidebar } from "./components/shell/Sidebar";
import { ErrorState, NoClustersState } from "./components/table/states";
import { useClusters } from "./hooks/useClusters";
import { useTheme } from "./hooks/useTheme";
import { ConfigMapsPage } from "./pages/ConfigMapsPage";
import { CronJobsPage } from "./pages/CronJobsPage";
import { DaemonSetsPage } from "./pages/DaemonSetsPage";
import { DeploymentsPage } from "./pages/DeploymentsPage";
import { IngressesPage } from "./pages/IngressesPage";
import { JobsPage } from "./pages/JobsPage";
import { NamespacesPage } from "./pages/NamespacesPage";
import { PodsPage } from "./pages/PodsPage";
import { SecretsPage } from "./pages/SecretsPage";
import { ServicesPage } from "./pages/ServicesPage";
import { StatefulSetsPage } from "./pages/StatefulSetsPage";

export default function App() {
  useTheme();

  return (
    <Routes>
      <Route path="/" element={<RootRedirect />} />
      <Route path="/clusters/:cluster" element={<AppShell />}>
        <Route index element={<Navigate to="pods" replace />} />
        <Route path="pods" element={<WithCluster Page={PodsPage} />} />
        <Route path="deployments" element={<WithCluster Page={DeploymentsPage} />} />
        <Route path="statefulsets" element={<WithCluster Page={StatefulSetsPage} />} />
        <Route path="daemonsets" element={<WithCluster Page={DaemonSetsPage} />} />
        <Route path="jobs" element={<WithCluster Page={JobsPage} />} />
        <Route path="cronjobs" element={<WithCluster Page={CronJobsPage} />} />
        <Route path="services" element={<WithCluster Page={ServicesPage} />} />
        <Route path="ingresses" element={<WithCluster Page={IngressesPage} />} />
        <Route path="configmaps" element={<WithCluster Page={ConfigMapsPage} />} />
        <Route path="secrets" element={<WithCluster Page={SecretsPage} />} />
        <Route path="namespaces" element={<WithCluster Page={NamespacesPage} />} />
      </Route>
      <Route path="*" element={<RootRedirect />} />
    </Routes>
  );
}

function AppShell() {
  return (
    <div className="flex h-full">
      <Sidebar />
      <main className="flex min-w-0 flex-1 flex-col bg-bg">
        <Outlet />
      </main>
    </div>
  );
}

function WithCluster<P extends { cluster: string }>({
  Page,
}: {
  Page: React.ComponentType<P>;
}) {
  const { cluster } = useParams<{ cluster: string }>();
  if (!cluster) return null;
  return <Page {...({ cluster } as unknown as P)} />;
}

function RootRedirect() {
  const { data, isLoading, isError, error } = useClusters();
  const navigate = useNavigate();

  useEffect(() => {
    if (data && data.clusters.length > 0) {
      navigate(`/clusters/${data.clusters[0].name}/pods`, { replace: true });
    }
  }, [data, navigate]);

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center bg-bg">
        <div className="flex items-center gap-3 text-[13px] text-ink-muted">
          <span
            aria-hidden
            className="block size-3.5 animate-spin rounded-full border-[1.5px] border-border-strong border-t-accent"
          />
          loading clusters…
        </div>
      </div>
    );
  }
  if (isError) {
    return (
      <div className="flex h-full bg-bg">
        <ErrorState
          title="couldn't reach periscope backend"
          message={(error as Error)?.message ?? "unknown"}
          hint={
            <>
              Is the backend running on{" "}
              <code className="rounded bg-surface-2 px-1 py-0.5 font-mono">
                :8080
              </code>
              ?
            </>
          }
        />
      </div>
    );
  }
  if (data && data.clusters.length === 0) {
    return (
      <div className="flex h-full bg-bg">
        <NoClustersState />
      </div>
    );
  }
  return null;
}
