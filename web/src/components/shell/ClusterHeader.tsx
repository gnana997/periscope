import { OpenClusterShellButton } from "../exec/OpenClusterShellButton";

/**
 * ClusterHeader — thin horizontal strip at the top of every cluster
 * page. Currently hosts only the cluster-shell action (issue #104);
 * future actions slot into the right-aligned action area.
 *
 * Lives between <main> and the page <Outlet/> in routeShells.AppShell.
 */
interface ClusterHeaderProps {
  cluster: string;
}

export function ClusterHeader({ cluster }: ClusterHeaderProps) {
  return (
    <header className="flex h-9 shrink-0 items-center justify-end gap-2 border-b border-border bg-surface px-3">
      <OpenClusterShellButton cluster={cluster} />
    </header>
  );
}
