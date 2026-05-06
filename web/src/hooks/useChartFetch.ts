// useChartFetch — TanStack hooks for the Helm chart-fetch endpoints
// (#73). Two hooks because the underlying endpoints have different
// caching semantics:
//
//   useChartVersions — long stale time (5min); the version list
//                      moves slowly. Refetches only on explicit
//                      invalidate or `enabled` toggle.
//   useChartValues   — long stale time (effectively forever);
//                      a (ref, version) is immutable, so once
//                      fetched it doesn't change. Mutation hook
//                      below covers the "Fetch" button click +
//                      audit-row emission case.
//
// The install-dialog UI (sibling issue) drives both: as the
// operator types/pastes, useChartVersions auto-fires when the ref
// looks valid; once they pick a version and click Fetch, the
// values mutation runs.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type {
  ChartFetchRequest,
  ChartFetchResult,
  ChartVersionsResult,
} from "../lib/types";

export interface UseChartVersionsArgs {
  cluster: string;
  ref: string;
  chartName: string;
  /** Set false to keep the query installed but not fire — useful
   *  while the operator is still typing the URL. */
  enabled?: boolean;
}

export function useChartVersions(args: UseChartVersionsArgs) {
  return useQuery<ChartVersionsResult, ApiError | Error>({
    queryKey: queryKeys
      .cluster(args.cluster)
      .helm.chartVersions(args.ref, args.chartName),
    queryFn: ({ signal }) =>
      api.chartVersions(args.cluster, args.ref, args.chartName, undefined, signal),
    enabled: args.enabled !== false && args.ref.length > 0,
    staleTime: 5 * 60 * 1000, // 5min — registry tags move slowly
    retry: false,             // a typo in the ref shouldn't retry-storm the registry
  });
}

/** Mutation form of the values fetch — runs on Fetch-button click,
 *  emits the audited helm_chart_fetch row server-side. */
export function useChartValuesMutation(cluster: string) {
  const qc = useQueryClient();
  return useMutation<ChartFetchResult, ApiError | Error, ChartFetchRequest>({
    mutationFn: (req) => api.chartValues(cluster, req),
    onSuccess: (result, req) => {
      // Seed the immutable per-(ref,version) query so subsequent
      // useChartValues callers find it without a refetch.
      qc.setQueryData(
        queryKeys.cluster(cluster).helm.chartValues(req.ref, req.chart ?? "", req.version),
        result,
      );
    },
  });
}

/** Read form, paired with the mutation. Used by call sites that
 *  want to render the cached chart without re-issuing the audit row. */
export function useChartValues(cluster: string, req: ChartFetchRequest, enabled = true) {
  return useQuery<ChartFetchResult, ApiError | Error>({
    queryKey: queryKeys
      .cluster(cluster)
      .helm.chartValues(req.ref, req.chart ?? "", req.version),
    queryFn: ({ signal }) => api.chartValues(cluster, req, undefined, signal),
    enabled: enabled && req.ref.length > 0 && req.version.length > 0,
    // Once we have it, it doesn't change — Helm tags are immutable.
    staleTime: Infinity,
    retry: false,
  });
}

/** Cache-bust hook for the SPA's "refresh versions" button. Sends
 *  the same request with nocache=true so the server skips its TTL
 *  cache, then invalidates the React-Query cache so the hook re-fires. */
export function useChartVersionsRefresh() {
  const qc = useQueryClient();
  return useMutation<ChartVersionsResult, ApiError | Error, UseChartVersionsArgs>({
    mutationFn: (args) =>
      api.chartVersions(args.cluster, args.ref, args.chartName, { nocache: true }),
    onSuccess: (_result, args) => {
      qc.invalidateQueries({
        queryKey: queryKeys.cluster(args.cluster).helm.chartVersions(args.ref, args.chartName),
      });
    },
  });
}
