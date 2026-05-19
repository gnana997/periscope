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
    // Periodic refetch (12s) so a cold-start `metricsAvailable: false`
    // — the apiserver-proxy `/metrics` call can exceed the backend's
    // 15s budget on the very first call due to TLS handshake + cold
    // connection — automatically resolves on the next poll without
    // forcing the operator to refresh. 12s < staleTime so the cache
    // is consulted first; refetchInterval kicks in for active queries.
    refetchInterval: 12_000,
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

// useKarpenterAvailability — lightweight CRD-presence probe used by
// the sidebar to decide whether to render the Karpenter nav entry.
// Backed by /api/clusters/{c}/karpenter/availability, which is NOT
// audited (split off in v1.1.1 so per-page-mount sidebar probes
// don't flood the audit log with `karpenter_read` rows that don't
// reflect operator-intent action).
//
// Longer staleTime than useKarpenter — CRD presence flips rarely
// (chart install / uninstall), so the sidebar can trust a stale
// cache for minutes without missing operator-visible transitions.
const AVAILABILITY_STALE_MS = 5 * 60 * 1000;

export function useKarpenterAvailability(cluster: string) {
  return useQuery<{ available: boolean }>({
    queryKey: queryKeys.cluster(cluster).karpenterAvailability(),
    queryFn: cluster
      ? ({ signal }) => api.karpenterAvailability(cluster, signal)
      : skipToken,
    staleTime: AVAILABILITY_STALE_MS,
    // No refetchInterval — CRD presence is not a polling concern.
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
