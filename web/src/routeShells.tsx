// routeShells — components used as route `element` props.
//
// Kept separate from routes.tsx so the data export (`router`) isn't
// in the same file as component definitions — Vite's react-refresh
// linter requires one or the other per file.
//
//   - AppShell     layout for /clusters/:cluster (sidebar + main pane)
//   - WithCluster  generic helper that pulls :cluster from the URL
//                  and forwards it as a prop to the page component
//   - RootRedirect entry point — redirects to the first cluster's
//                  default page once the cluster list resolves

import { useEffect, useRef } from "react";
import { Outlet, useNavigate, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { Brand } from "./components/shell/Brand";
import { ClusterRail } from "./components/shell/ClusterRail";
import { Sidebar } from "./components/shell/Sidebar";
import { ErrorState, NoClustersState } from "./components/table/states";
import { useClusters } from "./hooks/useClusters";
import { queryKeys } from "./lib/queryKeys";

export function AppShell() {
  return (
    <div className="flex h-full">
      <div className="flex h-full shrink-0 flex-col border-r border-border bg-surface">
        <Brand />
        <div className="h-px bg-border" />
        <div className="flex min-h-0 flex-1">
          <ClusterRail />
          <Sidebar />
        </div>
      </div>
      <main className="flex min-w-0 flex-1 flex-col bg-bg">
        <Outlet />
      </main>
    </div>
  );
}

export function WithCluster<P extends { cluster: string }>({
  Page,
}: {
  Page: React.ComponentType<P>;
}) {
  const { cluster } = useParams<{ cluster: string }>();
  const qc = useQueryClient();
  const prevCluster = useRef<string | undefined>(undefined);

  // Single-cluster-at-a-time UX: when the route's cluster id changes,
  // evict the prior cluster's subtree so its stale entries don't sit
  // in the cache (and don't trigger ghost background refetches).
  useEffect(() => {
    if (cluster && prevCluster.current && prevCluster.current !== cluster) {
      qc.removeQueries({
        queryKey: queryKeys.cluster(prevCluster.current).all,
      });
    }
    prevCluster.current = cluster;
  }, [cluster, qc]);

  if (!cluster) return null;
  return <Page {...({ cluster } as unknown as P)} />;
}

export function RootRedirect() {
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
