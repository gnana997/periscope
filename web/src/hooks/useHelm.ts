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

// ─── Helm preview + install + upgrade (#75 / #76) ───────────────────

import { useMutation, useQueryClient } from "@tanstack/react-query";
import type {
  HelmActionResult,
  HelmInstallPreviewRequest,
  HelmInstallRequest,
  HelmUpgradePreviewRequest,
  HelmUpgradeRequest,
  PreviewResponse,
} from "../lib/types";

/**
 * useHelmInstallPreview — POST install-preview as a TanStack mutation
 * because Preview is operator-triggered (button click), not auto-fired
 * on a query key. Returns rendered manifests + RBAC denial list.
 */
export function useHelmInstallPreview(cluster: string) {
  return useMutation<PreviewResponse, Error, HelmInstallPreviewRequest>({
    mutationFn: (body) => api.helmInstallPreview(cluster, body),
  });
}

/**
 * useHelmUpgradePreview — POST upgrade-preview. ns + releaseName come
 * from the URL on the backend; the SPA carries them via the mutation
 * arg shape (object with body + namespace + name) so callers don't
 * have to thread three args through.
 */
export function useHelmUpgradePreview(cluster: string) {
  return useMutation<
    PreviewResponse,
    Error,
    { namespace: string; name: string; body: HelmUpgradePreviewRequest }
  >({
    mutationFn: ({ namespace, name, body }) =>
      api.helmUpgradePreview(cluster, namespace, name, body),
  });
}

/**
 * useInstallHelmRelease — POST install action. Sync; mutation can take
 * 10-60s+ for slow charts. onSuccess invalidates the helm releases
 * list query so the new release appears in the SPA's release table
 * immediately (no manual refresh).
 */
export function useInstallHelmRelease(cluster: string) {
  const qc = useQueryClient();
  return useMutation<HelmActionResult, Error, HelmInstallRequest>({
    mutationFn: (body) => api.helmInstall(cluster, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.cluster(cluster).helm.list() });
    },
  });
}

/**
 * useUpgradeHelmRelease — POST upgrade action. onSuccess invalidates
 * the per-release detail + history queries so the SPA renders the new
 * revision immediately.
 */
export function useUpgradeHelmRelease(
  cluster: string,
  namespace: string,
  name: string,
) {
  const qc = useQueryClient();
  return useMutation<HelmActionResult, Error, HelmUpgradeRequest>({
    mutationFn: (body) => api.helmUpgrade(cluster, namespace, name, body),
    onSuccess: () => {
      qc.invalidateQueries({
        queryKey: ["cluster", cluster, "helm", "detail", namespace, name],
      });
      qc.invalidateQueries({
        queryKey: queryKeys.cluster(cluster).helm.history(namespace, name),
      });
    },
  });
}

/**
 * useUninstallHelmRelease — DELETE /helm/releases/{ns}/{name}. Sync.
 * On success invalidates the helm releases list (the SPA's release
 * table no longer shows the uninstalled release). The release detail
 * page navigates away on success since the underlying route 404s
 * once the release is gone — handled at the call site.
 */
export function useUninstallHelmRelease(
  cluster: string,
  namespace: string,
  name: string,
) {
  const qc = useQueryClient();
  return useMutation<
    import("../lib/types").HelmUninstallResult,
    Error,
    { keepHistory?: boolean; disableHooks?: boolean }
  >({
    mutationFn: (opts) => api.helmUninstall(cluster, namespace, name, opts),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.cluster(cluster).helm.list() });
    },
  });
}

// useRollbackHelmRelease (#77). Mutation wrapper around POST
// /helm/releases/{ns}/{name}/rollback. Caller passes { revision }
// (and optional knobs) to the .mutate() call. Cluster + namespace
// + release name are bound at hook-construction time since they're
// stable in the URL the hook is used from.
//
// Invalidates list (status badge), history (revision row state),
// and the detail tab on success. Errors bubble through useMutation's
// `error` so the caller can surface them — the modal in #77 keeps
// itself open until the mutation settles and shows the error inline.
export function useRollbackHelmRelease(
  cluster: string,
  namespace: string,
  name: string,
) {
  const qc = useQueryClient();
  return useMutation<
    import("../lib/types").HelmRollbackResult,
    Error,
    {
      revision: number;
      wait?: boolean;
      cleanupOnFail?: boolean;
      disableHooks?: boolean;
      timeoutSeconds?: number;
    }
  >({
    mutationFn: (body) => api.helmRollback(cluster, namespace, name, body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.cluster(cluster).helm.list() });
      qc.invalidateQueries({
        queryKey: queryKeys.cluster(cluster).helm.history(namespace, name),
      });
      // The detail key includes a revision, so invalidate the entire
      // helm subtree for this release rather than enumerating revs.
      qc.invalidateQueries({
        queryKey: ["cluster", cluster, "helm", "detail", namespace, name],
      });
    },
  });
}
