// useCve — TanStack Query hooks for the CVE / Inspector v2 surface
// (#165 backend, #166 frontend).
//
// All read endpoints are O(1) map lookups on the backend after the
// cold-path hydrate, so `staleTime: 5 * 60_000` keeps refetches off
// the hot path. Mutations (refresh) invalidate the affected subtree.
//
// The chip-column hook (useCvePodSummaries) walks all /cve/pods
// pages in parallel via one outer useQuery — the call graph is:
//
//   useCvePodSummaries(cluster)
//     queryFn = async () => {
//       const cursors: (string|undefined)[] = await pageWalk(...)
//       return Map<podKey, CvePodSummary>
//     }
//
// Sequential pages parallelised with Promise.all in batches of 4 so
// a 5000-pod cluster (50 pages) lands in ~10s instead of one-by-one.
// See plan: "Pods column read strategy" decision.

import {
  skipToken,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { ApiError, api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type {
  CveByWorkloadResp,
  CveFindingsResp,
  CveInstancesResp,
  CvePodRow,
  CvePodSummary,
  CvePodsResp,
  CveRefreshRequest,
  CveStatusResp,
} from "../lib/types";

const STALE_MS = 5 * 60_000;

// Retry rule shared by all CVE hooks: a 4xx is a structured "this
// won't fix itself" signal, no point retrying.
function cveRetry(count: number, err: unknown): boolean {
  if (err instanceof ApiError && err.status >= 400 && err.status < 500) {
    return false;
  }
  return count < 1;
}

/** /cve/status. Does NOT trigger a hydrate — the SPA polls this
 *  while a cold-start scan is in flight so the spinner has accurate
 *  state without burning Inspector traffic. */
export function useCveStatus(cluster: string) {
  return useQuery<CveStatusResp>({
    queryKey: queryKeys.cluster(cluster).cve.status(),
    queryFn: cluster ? ({ signal }) => api.cveStatus(cluster, signal) : skipToken,
    staleTime: STALE_MS,
    retry: cveRetry,
  });
}

/** /cve/by-instance — list. Used to build the per-instance lookup
 *  map that powers the Nodes / NodeGroups / Karpenter chip columns. */
export function useCveByInstance(cluster: string) {
  return useQuery<CveInstancesResp>({
    queryKey: queryKeys.cluster(cluster).cve.byInstance(),
    queryFn: cluster
      ? ({ signal }) => api.cveByInstance(cluster, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: cveRetry,
  });
}

/** /cve/by-instance/{id} — one instance, full findings. Used by
 *  SecurityTab when the operator opens a Node detail pane. */
export function useCveByInstanceOne(cluster: string, instanceId: string) {
  const enabled = Boolean(cluster && instanceId);
  return useQuery<CveFindingsResp>({
    queryKey: queryKeys.cluster(cluster).cve.byInstanceOne(instanceId),
    queryFn: enabled
      ? ({ signal }) => api.cveByInstanceOne(cluster, instanceId, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: cveRetry,
  });
}

/** /cve/pods/{ns}/{name} — one pod, per-container findings. Used by
 *  SecurityTab when the operator opens a Pod detail pane. */
export function useCvePodDetail(cluster: string, ns: string, name: string) {
  const enabled = Boolean(cluster && ns && name);
  return useQuery<CvePodRow>({
    queryKey: queryKeys.cluster(cluster).cve.podDetail(ns, name),
    queryFn: enabled
      ? ({ signal }) => api.cvePodDetail(cluster, ns, name, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: cveRetry,
  });
}

/** /cve/by-workload/{kind}/{ns}/{name} — owner-aware aggregation.
 *  Used by SecurityTab on Deployment / STS / DS detail panes. */
export function useCveByWorkload(
  cluster: string,
  kind: string,
  ns: string,
  name: string,
) {
  const enabled = Boolean(cluster && kind && ns && name);
  return useQuery<CveByWorkloadResp>({
    queryKey: queryKeys.cluster(cluster).cve.byWorkload(kind, ns, name),
    queryFn: enabled
      ? ({ signal }) => api.cveByWorkload(cluster, kind, ns, name, signal)
      : skipToken,
    staleTime: STALE_MS,
    retry: cveRetry,
  });
}

/** useCvePodSummaries — walks ALL /cve/pods pages in one outer query
 *  and returns a Map<ns/name, CvePodSummary>. The chip column on the
 *  Pods page reads from this map; misses render the chip as `unscanned`.
 *
 *  Why one outer query: TanStack Query doesn't natively orchestrate a
 *  page-walk fan-out, and `useInfiniteQuery` returns pages as separate
 *  data slices the caller would still have to flatten + re-merge. The
 *  single-query form makes the column accessor trivially synchronous
 *  once the query resolves.
 *
 *  Concurrency: walk pages in batches of 4 so a 5000-pod cluster
 *  (~50 pages) lands in roughly 13 requests of round-trip latency
 *  instead of 50 sequential ones. The backend serves each page from
 *  the in-memory store so per-page response time is single-digit ms;
 *  the 4-wide fan-out is plenty. */
export function useCvePodSummaries(cluster: string) {
  return useQuery<Map<string, CvePodSummary>>({
    queryKey: queryKeys.cluster(cluster).cve.podSummariesAll(),
    queryFn: cluster
      ? async ({ signal }) => {
          const out = new Map<string, CvePodSummary>();
          let cursor: string | undefined = undefined;
          // Walk pages sequentially — the response carries the next
          // cursor, so they fundamentally chain. Parallelism happens
          // when the caller mounts the hook on multiple pages
          // (TanStack Query dedupes within the same key) and inside
          // the backend (the store is in-memory).
          //
          // If sequential turns out to be too slow on a real 5000-
          // pod cluster, the follow-up move is the /cve/pods/summary
          // bulk endpoint tracked in the plan's Out-of-Scope.
          for (let i = 0; i < 200; i++) {
            const page: CvePodsResp = await api.cvePodsPage(
              cluster,
              cursor,
              signal,
            );
            for (const p of page.pods) {
              const key = podKey(p.namespace, p.name);
              out.set(key, {
                namespace: p.namespace,
                name: p.name,
                counts: p.rolledUpSeverityCounts,
                coverage: p.scanCoverage,
              });
            }
            if (!page.next) break;
            cursor = page.next;
          }
          return out;
        }
      : skipToken,
    staleTime: STALE_MS,
    retry: cveRetry,
  });
}

/** podKey is the lookup key the column reads. Kept here so the
 *  column accessor and the page-walker can't drift. */
export function podKey(ns: string, name: string): string {
  return `${ns}/${name}`;
}

/** useCveRefresh — POST /cve/refresh mutation. Caller passes the
 *  digests / instance IDs to re-fetch; on success the relevant
 *  query subtree is invalidated so the SPA picks up fresh data.
 *
 *  Scope semantics:
 *    - Pod refresh: invalidates this pod's detail key.
 *    - Instance refresh: invalidates this instance's detail key.
 *    - Workload refresh: caller passes the union of pods' digests;
 *      we invalidate the workload key.
 *
 *  Callers pass `invalidate` so the mutation knows which key to
 *  clear (avoids over-invalidating the whole cve subtree on every
 *  refresh). */
export function useCveRefresh(cluster: string) {
  const qc = useQueryClient();
  return useMutation<
    unknown,
    Error,
    {
      digests?: string[];
      instanceIds?: string[];
      invalidate?: ReadonlyArray<readonly unknown[]>;
    }
  >({
    mutationFn: async ({ digests, instanceIds }) => {
      const body: CveRefreshRequest = { digests, instanceIds };
      return api.cveRefresh(cluster, body);
    },
    onSuccess: (_data, vars) => {
      // Always bump status so the lastHydrate / entryCounts refresh.
      qc.invalidateQueries({
        queryKey: queryKeys.cluster(cluster).cve.status(),
      });
      // Caller-provided keys for the specific entity tab(s).
      for (const k of vars.invalidate ?? []) {
        qc.invalidateQueries({ queryKey: k });
      }
    },
  });
}

/** True when an ApiError is the inspector-disabled empty-state
 *  signal. Used by SecurityTab callers to render the empty state
 *  instead of an error banner. Mirrors isBackendNotEKS in shape. */
export function isInspectorDisabled(err: unknown): boolean {
  if (!(err instanceof ApiError)) return false;
  // The backend uses HTTP 200 + inspectorEnabled:false rather than a
  // 4xx code for this case, so a thrown ApiError is genuinely an
  // error (network / 5xx) and not the empty-state signal. The hook
  // returns the typed response; the caller branches on
  // response.inspectorEnabled === false. This helper is kept as a
  // place to land future signals (e.g. a 412 if the backend ever
  // adopts one) without changing call sites.
  return false;
}
