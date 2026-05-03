// useHelm — TanStack Query hooks for the Helm release browser.
//
// Four hooks, one per backend endpoint:
//   useHelmReleases(cluster)            → list
//   useHelmRelease(cluster, ns, name, revision)  → unified detail
//   useHelmHistory(cluster, ns, name)   → revision metadata
//   useHelmDiff(cluster, ns, name, from, to)     → structured diff
//
// staleTime mirrors the backend cache where applicable. Detail /
// history / diff are per-revision immutable so we cache aggressively
// (5 minutes) — a tab that flips between values/manifest/history for
// the same release pays one fetch.

import { skipToken, useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type {
  HelmDiffResponse,
  HelmHistoryResponse,
  HelmReleaseDetail,
  HelmReleasesResponse,
} from "../lib/types";

const LIST_STALE_MS = 30_000;
const DETAIL_STALE_MS = 5 * 60_000;

export function useHelmReleases(cluster: string) {
  return useQuery<HelmReleasesResponse>({
    queryKey: queryKeys.cluster(cluster).helm.list(),
    queryFn: cluster
      ? ({ signal }) => api.helmReleases(cluster, signal)
      : skipToken,
    staleTime: LIST_STALE_MS,
  });
}

/**
 * Pass revision=0 (or undefined) for the latest revision. The query
 * key includes the resolved revision so navigation between revisions
 * via the history tab cache-hits cleanly.
 */
export function useHelmRelease(
  cluster: string,
  namespace: string,
  name: string,
  revision: number,
) {
  const enabled = Boolean(cluster && namespace && name);
  return useQuery<HelmReleaseDetail>({
    queryKey: queryKeys.cluster(cluster).helm.detail(namespace, name, revision),
    queryFn: enabled
      ? ({ signal }) =>
          api.helmRelease(cluster, namespace, name, revision || undefined, signal)
      : skipToken,
    staleTime: DETAIL_STALE_MS,
  });
}

export function useHelmHistory(cluster: string, namespace: string, name: string) {
  const enabled = Boolean(cluster && namespace && name);
  return useQuery<HelmHistoryResponse>({
    queryKey: queryKeys.cluster(cluster).helm.history(namespace, name),
    queryFn: enabled
      ? ({ signal }) => api.helmHistory(cluster, namespace, name, signal)
      : skipToken,
    staleTime: DETAIL_STALE_MS,
  });
}

export function useHelmDiff(
  cluster: string,
  namespace: string,
  name: string,
  fromRev: number,
  toRev: number,
) {
  const enabled =
    Boolean(cluster && namespace && name) && (fromRev > 0 || toRev > 0);
  return useQuery<HelmDiffResponse>({
    queryKey: queryKeys.cluster(cluster).helm.diff(namespace, name, fromRev, toRev),
    queryFn: enabled
      ? ({ signal }) => api.helmDiff(cluster, namespace, name, fromRev, toRev, signal)
      : skipToken,
    staleTime: DETAIL_STALE_MS,
  });
}
