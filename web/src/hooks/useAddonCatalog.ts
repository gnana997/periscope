// useAddonCatalog — TanStack Query hook for the EKS add-on catalog
// (issue #119, PR-1).
//
// Mirrors useAddons exactly — same retry policy (no retry on the
// 422 E_BACKEND_NOT_EKS branch, capped retries on transient errors)
// and 60s staleTime. The backend cache (6h, keyed by k8sVersion) is
// shared across users; the SPA staleTime keeps a freshly-opened tab
// from holding stale data when an operator just changed cluster
// state.

import { skipToken, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type { AddonCatalogResponse } from "../lib/types";

const STALE_MS = 60_000;

export function useAddonCatalog(cluster: string) {
  return useQuery<AddonCatalogResponse>({
    queryKey: queryKeys.cluster(cluster).addons.catalog(),
    queryFn: cluster
      ? ({ signal }) => api.addonCatalog(cluster, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: (count, err) => {
      if (
        err &&
        typeof err === "object" &&
        "status" in err &&
        (err as { status: number }).status === 422
      ) {
        return false;
      }
      return count < 2;
    },
  });
}
