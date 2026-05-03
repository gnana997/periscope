import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type { FleetResponse } from "../lib/types";

/**
 * useFleet polls the /api/fleet aggregator on a 15s cadence.
 *
 * Visibility is handled by TanStack Query v5 defaults:
 * - `refetchIntervalInBackground: false` (default) pauses polling
 *   while the tab is hidden.
 * - `refetchOnWindowFocus: true` (default) triggers a refetch the
 *   moment the user comes back.
 *
 * Per-cluster errors are encoded inside the response (see
 * FleetClusterEntry.error). The query itself only fails on a true
 * page-level failure — 401 (session expired), 403 (tier denies all),
 * or 5xx.
 */
export function useFleet() {
  return useQuery<FleetResponse>({
    queryKey: queryKeys.fleet(),
    queryFn: ({ signal }) => api.fleet(signal),
    refetchInterval: 15_000,
    // Be generous on staleTime so multiple components on the page
    // don't kick off back-to-back refetches; the 15s polling cadence
    // is the source of freshness.
    staleTime: 10_000,
  });
}
