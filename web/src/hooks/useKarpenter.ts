// useKarpenter — TanStack Query hook for the Karpenter dashboard
// surface (issue #118).
//
// One hook covers the whole page: a single GET returns NodePools +
// NodeClaims + pending pods + metrics availability + cost summary
// in one shape. Components destructure what they need.
//
// staleTime is kept short (15s) because Karpenter state changes by
// the second — a stale cache is worse than the cost of a refetch.
// The backend is also not allowed to cache (issue #118 acceptance);
// this just bounds how often React refetches when the user pings
// between the sidebar entry and the page.
//
// The sidebar entry uses the same hook so we share one network call
// across the nav-and-page render. When `available` is false the
// sidebar hides itself; the page renders an empty state with a one-
// line link to Karpenter docs.

import { skipToken, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type { KarpenterDashboard } from "../lib/types";

const STALE_MS = 15_000;

export function useKarpenter(cluster: string) {
  return useQuery<KarpenterDashboard>({
    queryKey: queryKeys.cluster(cluster).karpenter(),
    queryFn: cluster
      ? ({ signal }) => api.karpenter(cluster, signal)
      : skipToken,
    staleTime: STALE_MS,
    // 502 → backend reached the cluster but Karpenter list call
    // failed (apiserver returned an error). Worth retrying once;
    // not pointless like a 4xx.
    retry: (count, err) => {
      if (
        err &&
        typeof err === "object" &&
        "status" in err &&
        ((err as { status: number }).status >= 400 &&
          (err as { status: number }).status < 500)
      ) {
        return false;
      }
      return count < 1;
    },
  });
}
