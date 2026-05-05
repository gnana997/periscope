// useUpgradeInsights — TanStack Query hooks for the EKS Upgrade
// Insights surface (issue #103, PR-1).
//
// Two hooks, one per backend endpoint:
//   useUpgradeInsights(cluster)            → list + bucket counts
//   useUpgradeInsight(cluster, insightId)  → detail with affected resources
//
// staleTime is generous (5 min) because the backend already caches
// for an hour and AWS itself only refreshes daily — extra invalidations
// from the SPA don't shorten the AWS-side latency, they just burn
// React renders. Errors keep their default React Query lifecycle so
// callers can branch on `isBackendNotEKS(error)` to show the empty
// state.

import { skipToken, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type {
  UpgradeInsightDetail,
  UpgradeInsightsListResponse,
} from "../lib/types";

const STALE_MS = 5 * 60_000;

export function useUpgradeInsights(cluster: string) {
  return useQuery<UpgradeInsightsListResponse>({
    queryKey: queryKeys.cluster(cluster).upgradeInsights.list(),
    queryFn: cluster
      ? ({ signal }) => api.upgradeInsights(cluster, signal)
      : skipToken,
    staleTime: STALE_MS,
    // 422 is a structured "this isn't an EKS cluster" signal, not a
    // transient error. No point retrying.
    retry: (count, err) => {
      if (err && typeof err === "object" && "status" in err && (err as { status: number }).status === 422) {
        return false;
      }
      return count < 2;
    },
  });
}

export function useUpgradeInsight(cluster: string, insightId: string) {
  const enabled = Boolean(cluster && insightId);
  return useQuery<UpgradeInsightDetail>({
    queryKey: queryKeys.cluster(cluster).upgradeInsights.detail(insightId),
    queryFn: enabled
      ? ({ signal }) => api.upgradeInsight(cluster, insightId, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: (count, err) => {
      if (err && typeof err === "object" && "status" in err && (err as { status: number }).status === 422) {
        return false;
      }
      return count < 2;
    },
  });
}
