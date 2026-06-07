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

import { Suspense, useEffect, useRef } from "react";
import { Outlet, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { Brand } from "./components/shell/Brand";
import { ClusterRail } from "./components/shell/ClusterRail";
import { Sidebar } from "./components/shell/Sidebar";
import { queryKeys } from "./lib/queryKeys";
import { LoadingState } from "./components/table/states";

export function AppShell() {
  const { cluster } = useParams<{ cluster: string }>();
  const qc = useQueryClient();
  const prevCluster = useRef<string | undefined>(undefined);

  // Single-cluster-at-a-time UX: when the route's cluster id changes,
  // evict the prior cluster's subtree so its stale entries don't sit
  // in the cache (and don't trigger ghost background refetches).
  // Lives here (the /clusters/:cluster layout) rather than on each
  // page's WithCluster wrapper because per-page WithClusters unmount
  // when navigating between pages — the ref would lose its history.
  // AppShell stays mounted across page changes within a cluster, so
  // the ref correctly tracks the previous cluster across all
  // navigations.
  useEffect(() => {
    if (cluster && prevCluster.current && prevCluster.current !== cluster) {
      qc.removeQueries({
        queryKey: queryKeys.cluster(prevCluster.current).all,
      });
    }
    prevCluster.current = cluster;
  }, [cluster, qc]);

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
        <Suspense fallback={<LoadingState resource="page" />}><Outlet /></Suspense>
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
  if (!cluster) return null;
  return <Page {...({ cluster } as unknown as P)} />;
}

