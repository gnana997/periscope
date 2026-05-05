// useNodegroups — TanStack Query hooks for the EKS managed node
// group surface (issue #103, PR-2/3).
//
// Two hooks:
//   useNodegroups(cluster)              → list + counts
//   useNodegroup(cluster, name)         → detail
//
// staleTime is 60s (vs 5min for the backend cache) — the SPA pulls
// fresh on tab refocus / window mount so a cluster operator who
// just kicked off a node-group rotation sees the new state quickly.
// The 5min backend cache still bounds AWS-side cost across users.

import { skipToken, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type {
  NodegroupDetail,
  NodegroupsListResponse,
} from "../lib/types";

const STALE_MS = 60_000;

export function useNodegroups(cluster: string) {
  return useQuery<NodegroupsListResponse>({
    queryKey: queryKeys.cluster(cluster).nodegroups.list(),
    queryFn: cluster
      ? ({ signal }) => api.nodegroups(cluster, signal)
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

export function useNodegroup(cluster: string, name: string) {
  const enabled = Boolean(cluster && name);
  return useQuery<NodegroupDetail>({
    queryKey: queryKeys.cluster(cluster).nodegroups.detail(name),
    queryFn: enabled
      ? ({ signal }) => api.nodegroup(cluster, name, signal)
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
