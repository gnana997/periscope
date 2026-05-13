// Identity page hooks (#178). Four independent queries; the page
// renders each section based on its own loading/error state so a
// 403 on one (e.g. aws-auth visibility denied for the requesting
// user) doesn't blank the rest of the page.
//
// All four endpoints share the same error-status contract as
// useUpgradeInsights / useAddons: 422 = backend not EKS, 403 =
// missing IAM, 429 = AWS throttling, 503 = SA informer still
// syncing (sa-roles only).

import { skipToken, useQuery } from "@tanstack/react-query";

import { api } from "../lib/api";
import type {
  AccessEntry,
  AwsAuthDiffResponse,
  PodIdentityResponse,
  SARoleIndexEntry,
} from "../lib/identity";
import { queryKeys } from "../lib/queryKeys";

// AWS-aware reads change at the rate of operator clicks, not pod
// churn. Treat them as fresh for 30s — refreshing the page or
// switching tabs picks up new entries without forcing a refetch on
// every render of every section.
const STALE_MS = 30_000;

// Helper: 422 (E_BACKEND_NOT_EKS) is structural — never worth
// retrying. Other transient errors get the default two retries.
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

// Helper: sa-roles can briefly return 503 while the manager's
// SA informer is doing its initial cache sync. The backend sets a
// Retry-After header; we keep the default two retries plus a short
// retry-delay so the second attempt usually succeeds.
function retryUnless422OrServerError(count: number, err: unknown): boolean {
  if (
    err &&
    typeof err === "object" &&
    "status" in err
  ) {
    const status = (err as { status: number }).status;
    if (status === 422) return false;
    if (status === 503) return count < 3;
  }
  return count < 2;
}

export function useAccessEntries(cluster: string) {
  return useQuery<AccessEntry[]>({
    queryKey: queryKeys.cluster(cluster).identity.accessEntries(),
    queryFn: cluster
      ? ({ signal }) => api.identityAccessEntries(cluster, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: retryUnless422,
  });
}

export function useAwsAuthDiff(cluster: string) {
  return useQuery<AwsAuthDiffResponse>({
    queryKey: queryKeys.cluster(cluster).identity.awsAuthDiff(),
    queryFn: cluster
      ? ({ signal }) => api.identityAwsAuthDiff(cluster, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: retryUnless422,
  });
}

export function useSARoles(cluster: string) {
  return useQuery<SARoleIndexEntry[]>({
    queryKey: queryKeys.cluster(cluster).identity.saRoles(),
    queryFn: cluster
      ? ({ signal }) => api.identitySARoles(cluster, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: retryUnless422OrServerError,
    retryDelay: (attempt) => Math.min(2000 * Math.pow(2, attempt), 8000),
  });
}

export function usePodIdentity(cluster: string) {
  return useQuery<PodIdentityResponse>({
    queryKey: queryKeys.cluster(cluster).identity.podIdentity(),
    queryFn: cluster
      ? ({ signal }) => api.identityPodIdentity(cluster, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: retryUnless422,
  });
}
