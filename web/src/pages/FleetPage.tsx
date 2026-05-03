import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { PageHeader } from "../components/page/PageHeader";
import { FilterStrip } from "../components/page/FilterStrip";
import {
  LoadingState,
  ErrorState,

} from "../components/table/states";
import { isForbidden } from "../components/table/isForbidden";
import { ClusterCard } from "../components/fleet/ClusterCard";
import { EnvironmentBand } from "../components/fleet/EnvironmentBand";
import {
  FleetEmptyRegistry,
  FleetTierDenied,
  FleetAllUnreachableBanner,
} from "../components/fleet/FleetEmptyStates";
import { useFleet } from "../hooks/useFleet";
import { usePinnedClusters } from "../hooks/usePinnedClusters";
import { queryKeys } from "../lib/queryKeys";
import type { FleetClusterEntry, FleetStatus } from "../lib/types";

/**
 * FleetPage — the home page. Multi-cluster rollup; cards link into
 * the per-cluster overview. State branching:
 *
 *   query.isPending           → LoadingState
 *   query.isError + 403       → FleetTierDenied (tier mode + no group match)
 *   query.isError + other     → ErrorState (network / 5xx / unexpected)
 *   data.clusters.length == 0 → FleetEmptyRegistry
 *   all unreachable           → FleetAllUnreachableBanner above grid
 *   mixed / healthy           → grouped bands
 */
export function FleetPage() {
  const query = useFleet();
  const queryClient = useQueryClient();
  const { isPinned, toggle: togglePin } = usePinnedClusters();

  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<string | null>(null);

  const refetchAll = () =>
    queryClient.invalidateQueries({ queryKey: queryKeys.fleet() });

  if (query.isPending) {
    return <LoadingState resource="fleet" />;
  }

  if (query.isError) {
    if (isForbidden(query.error)) {
      return <FleetTierDenied />;
    }
    return (
      <ErrorState
        title="couldn't load the fleet"
        message={(query.error as Error)?.message ?? "unknown error"}
      />
    );
  }

  const data = query.data;
  if (!data || data.clusters.length === 0) {
    return (
      <div className="flex flex-1 flex-col">
        <PageHeader title="Fleet" subtitle="" />
        <FleetEmptyRegistry />
      </div>
    );
  }

  // ---- Filtering ----
  const lowerSearch = search.trim().toLowerCase();
  const filtered = data.clusters.filter((c) => {
    if (lowerSearch && !c.name.toLowerCase().includes(lowerSearch)) return false;
    if (statusFilter === "problems") {
      return c.status !== "healthy";
    }
    if (statusFilter && statusFilter !== "all") {
      return c.status === statusFilter;
    }
    return true;
  });

  // ---- Grouping (env band + pinned band) ----
  const pinnedEntries: FleetClusterEntry[] = [];
  const byEnv = new Map<string, FleetClusterEntry[]>();
  for (const c of filtered) {
    if (isPinned(c.name)) {
      pinnedEntries.push(c);
      continue;
    }
    const env = c.environment || "other";
    const arr = byEnv.get(env) ?? [];
    arr.push(c);
    byEnv.set(env, arr);
  }

  const envOrder = sortedEnvs([...byEnv.keys()]);
  const totalBands =
    (pinnedEntries.length > 0 ? 1 : 0) +
    envOrder.filter((e) => (byEnv.get(e) ?? []).length > 0).length;
  const onlyOneBand = totalBands === 1;
  const allUnreachable =
    data.rollup.totalClusters > 0 &&
    (data.rollup.byStatus.unreachable ?? 0) === data.rollup.totalClusters;

  const subtitle = makeSubtitle(data.rollup);

  return (
    <div className="flex flex-1 flex-col">
      <PageHeader title="Fleet" subtitle={subtitle} />
      <FilterStrip
        search={search}
        onSearch={setSearch}
        statusFilter={statusFilter}
        statusOptions={[
          "problems",
          "healthy",
          "degraded",
          "unreachable",
          "denied",
        ]}
        onStatusFilter={setStatusFilter}
        resultCount={filtered.length}
        totalCount={data.rollup.totalClusters}
      />

      <div className="flex flex-1 flex-col gap-6 px-6 py-5">
        {allUnreachable && <FleetAllUnreachableBanner />}

        {pinnedEntries.length > 0 && (
          <EnvironmentBand
            hideHeader={onlyOneBand}
            label="pinned"
            summary={`${pinnedEntries.length} cluster${pinnedEntries.length === 1 ? "" : "s"}`}
          >
            {pinnedEntries.map((c) => (
              <ClusterCard
                key={c.name}
                entry={c}
                isPinned={true}
                onTogglePin={togglePin}
                onRetry={refetchAll}
              />
            ))}
          </EnvironmentBand>
        )}

        {envOrder.map((env) => {
          const entries = byEnv.get(env) ?? [];
          if (entries.length === 0) return null;
          return (
            <EnvironmentBand
              hideHeader={onlyOneBand}
              key={env}
              label={env}
              summary={summarizeBand(entries)}
            >
              {entries.map((c) => (
                <ClusterCard
                  key={c.name}
                  entry={c}
                  isPinned={false}
                  onTogglePin={togglePin}
                  onRetry={refetchAll}
                />
              ))}
            </EnvironmentBand>
          );
        })}

        {filtered.length === 0 && (
          <div className="rounded-md border border-dashed border-border bg-surface px-6 py-10 text-center text-[12.5px] italic text-ink-muted">
            no clusters match the current filter
          </div>
        )}
      </div>
    </div>
  );
}

/** Editorial subtitle, falls back to numeric when count is small or env tags absent. */
function makeSubtitle(rollup: import("../lib/types").FleetRollup): string {
  const total = rollup.totalClusters;
  const healthy = rollup.byStatus.healthy ?? 0;
  const degraded = rollup.byStatus.degraded ?? 0;
  const unreachable = rollup.byStatus.unreachable ?? 0;
  const unknown = rollup.byStatus.unknown ?? 0;

  // Numeric form — terse, deterministic, easy to scan.
  const parts = [
    `${total} cluster${total === 1 ? "" : "s"}`,
    healthy > 0 && `${healthy} healthy`,
    degraded > 0 && `${degraded} degraded`,
    unreachable > 0 && `${unreachable} unreachable`,
    unknown > 0 && `${unknown} checking`,
  ].filter(Boolean) as string[];
  return parts.join(" · ");
}

/** "3 clusters · all healthy" / "2 clusters · 1 unreachable". */
function summarizeBand(entries: FleetClusterEntry[]): string {
  const total = entries.length;
  const bad = entries.filter((e) => e.status !== "healthy").length;
  if (bad === 0) return `${total} cluster${total === 1 ? "" : "s"} · all healthy`;
  return `${total} cluster${total === 1 ? "" : "s"} · ${bad} need attention`;
}

/** prod first, then staging, then alphabetical, "other" last. */
function sortedEnvs(envs: string[]): string[] {
  const rank = (e: string) => {
    if (e === "prod" || e === "production") return 0;
    if (e === "staging" || e === "stage") return 1;
    if (e === "other") return 99;
    return 50;
  };
  return [...envs].sort((a, b) => {
    const ra = rank(a);
    const rb = rank(b);
    if (ra !== rb) return ra - rb;
    return a.localeCompare(b);
  });
}

// satisfy eslint react-refresh/only-export-components by re-exporting
// the page as default; keeping the named export for routes.tsx import.
export default FleetPage;
// suppress unused-import lint when FleetStatus is only used via types
export type _UnusedFleetStatus = FleetStatus;
