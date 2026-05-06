// useAddons — TanStack Query hooks for the EKS managed add-ons
// surface (issue #117).
//
// Two hooks:
//   useAddons(cluster)              → list + counts + cluster k8s ver
//   useAddon(cluster, name)         → detail blob with version history
//
// staleTime is 60s (vs 1h for the backend cache) — same cadence as
// useNodegroups. The SPA pulls fresh on tab refocus / window mount
// so a cluster operator who just kicked off an add-on update sees
// the new state quickly. The 1h backend cache still bounds AWS-side
// cost across users.

import { skipToken, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type { AddonDetail, AddonsListResponse } from "../lib/types";

const STALE_MS = 60_000;

// retryUnless422 is the shared retry predicate. The backend returns
// 422 + E_BACKEND_NOT_EKS for non-EKS clusters; that's a permanent
// branch (no amount of retrying turns a kind cluster into EKS) so
// short-circuit the retry budget on it. All other errors get the
// default budget of two retries.
function retryUnless422(count: number, err: unknown): boolean {
  if (
    err &&
    typeof err === "object" &&
    "status" in err &&
    (err as { status: number }).status === 422
  ) {
    return false;
  }
  return count < 2;
}

export function useAddons(cluster: string) {
  return useQuery<AddonsListResponse>({
    queryKey: queryKeys.cluster(cluster).addons.list(),
    queryFn: cluster
      ? ({ signal }) => api.addons(cluster, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: retryUnless422,
  });
}

export function useAddon(cluster: string, name: string) {
  const enabled = Boolean(cluster && name);
  return useQuery<AddonDetail>({
    queryKey: queryKeys.cluster(cluster).addons.detail(name),
    queryFn: enabled
      ? ({ signal }) => api.addon(cluster, name, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: retryUnless422,
  });
}
