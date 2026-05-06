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
//
// Status-aware polling (issue #119, PR-2): when an addon is in a
// transient state (CREATING / UPDATING / DELETING), the hook
// switches to a 4s refetchInterval until the row settles. This
// lets the operator watch the install/upgrade/delete flip happen
// without manual refresh. Polling is bounded by transient-state
// detection — once everything is ACTIVE / *_FAILED the interval
// goes back to undefined and the hook stops polling.

import { skipToken, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type { AddonDetail, AddonsListResponse } from "../lib/types";

const STALE_MS = 60_000;
const TRANSIENT_POLL_MS = 4_000;

// Transient AWS add-on statuses: an SDK write just kicked the addon
// into one of these and AWS is provisioning. Polling continues until
// the status leaves the set.
const TRANSIENT_STATUSES = new Set([
  "CREATING",
  "UPDATING",
  "DELETING",
]);

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
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      const anyTransient = data.addons.some((a) =>
        TRANSIENT_STATUSES.has(a.status),
      );
      return anyTransient ? TRANSIENT_POLL_MS : false;
    },
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
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      return TRANSIENT_STATUSES.has(data.status) ? TRANSIENT_POLL_MS : false;
    },
    retry: retryUnless422,
  });
}
