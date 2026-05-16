// Identity page hooks (#178). Four independent queries; the page
// renders each section based on its own loading/error state so a
// 403 on one (e.g. aws-auth visibility denied for the requesting
// user) doesn't blank the rest of the page.
//
// All four endpoints share the same error-status contract as
// useUpgradeInsights / useAddons: 422 = backend not EKS, 403 =
// missing IAM, 429 = AWS throttling, 503 = SA informer still
// syncing (sa-roles only).

import { skipToken, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";

import { api } from "../lib/api";
import type {
  AccessEntry,
  AwsAuthDiffResponse,
  CapabilitiesResponse,
  PodIdentityResponse,
  ReverseLookupResponse,
  SARoleIndexEntry,
  SensitiveCatalogResponse,
  WorkloadKind,
  WorkloadPermissionsResponse,
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

// ── Composed AWS Access surface (#188) ────────────────────────────

// useWorkloadPermissions powers the AWS Access tab on Pod / SA /
// Deployment / StatefulSet / DaemonSet detail panes. One round-
// trip returns the full composed response — identity chain,
// service-grouped permissions, warnings, affected pods. The SPA
// never joins on its own.
export function useWorkloadPermissions(
  cluster: string,
  kind: WorkloadKind | "",
  namespace: string,
  name: string,
) {
  const enabled = !!cluster && !!kind && !!name && (kind === "ServiceAccount" || !!namespace);
  return useQuery<WorkloadPermissionsResponse>({
    queryKey: queryKeys.cluster(cluster).identity.workloadPermissions(kind || "", namespace, name),
    queryFn: enabled
      ? ({ signal }) => api.iamWorkloadPermissions(cluster, kind as WorkloadKind, namespace, name, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: retryUnless422,
  });
}

// useReverseLookup fires only after the user submits the form.
// `enabled` is the gate so autocomplete keystrokes don't trigger
// the per-cluster role walk.
export function useReverseLookup(
  cluster: string,
  q: { action: string; resource?: string; namespace?: string },
  enabled: boolean,
) {
  const active = enabled && !!cluster && !!q.action.trim();
  return useQuery<ReverseLookupResponse>({
    queryKey: queryKeys
      .cluster(cluster)
      .identity.reverseLookup(q.action, q.resource ?? "", q.namespace ?? ""),
    queryFn: active
      ? ({ signal }) => api.iamReverseLookup(cluster, q, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: retryUnless422,
  });
}

// useSensitiveCatalog is the chip palette + autocomplete source.
// Cluster-agnostic, immutable across a process lifetime; treat as
// effectively infinite stale.
export function useSensitiveCatalog() {
  return useQuery<SensitiveCatalogResponse>({
    queryKey: queryKeys.sensitiveCatalog(),
    queryFn: ({ signal }) => api.identitySensitiveCatalog(signal),
    staleTime: Infinity,
    gcTime: Infinity,
  });
}

// useIdentityCapabilities backs the paywall pane. Returns the
// capabilities response plus a recheck() that bypasses the
// server-side cache and invalidates the local cache so the next
// render reflects fresh state (Re-check button after the user
// fixes an IAM perm or RBAC binding).
export function useIdentityCapabilities(cluster: string) {
  const qc = useQueryClient();
  const query = useQuery<CapabilitiesResponse>({
    queryKey: queryKeys.cluster(cluster).identity.capabilities(),
    queryFn: cluster
      ? ({ signal }) => api.identityCapabilities(cluster, { signal })
      : skipToken,
    staleTime: 5 * 60_000, // mirrors backend cache TTL
    retry: retryUnless422,
  });
  const recheck = useCallback(async () => {
    if (!cluster) return;
    const fresh = await api.identityCapabilities(cluster, { bypassCache: true });
    qc.setQueryData(queryKeys.cluster(cluster).identity.capabilities(), fresh);
  }, [cluster, qc]);
  return { ...query, recheck };
}
