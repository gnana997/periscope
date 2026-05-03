// useCanI — gates write/delete actions in the SPA via the backend's
// SelfSubjectAccessReview / SelfSubjectRulesReview endpoint.
//
// Two flavours:
//
//   useCanI(cluster, check)              → CanIDecision
//   useCanIBatch(cluster, checks)        → CanIDecision[]   (ordered)
//
// The single-check form is the convenient one for one-off buttons
// (Open Shell, Reveal Secret). The batch form is for action toolbars
// (ResourceActions) that ask several questions at once and want them
// answered in one POST so the backend can SSRR-route them as a batch.
// Single-check usage is the legacy shape — same identifying tuple
// across components shares the TanStack Query cache entry.
//
// Failure model: matches the backend's fail-closed design. If the
// can-i endpoint errors, every check returns { allowed: false } with
// a classified reason the tooltip can specialise on. Cluster-down,
// session-expired, anonymous — all coerce to "disabled with reason."
//
// Cache: 30s staleTime mirrors the backend's cache TTL, so two
// components asking the same question in the same render pass don't
// double-fetch and the SPA only re-asks the apiserver after a minute
// of activity (long enough to span typical user dwell on a detail
// pane).

import { skipToken, useQuery } from "@tanstack/react-query";
import { ApiError, api, type CanICheck, type CanIResult } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import { formatCanIDeniedReason } from "../lib/canIReason";
import { useAuth } from "../auth/useAuth";

export type { CanICheck, CanIResult } from "../lib/api";

/**
 * CanIDecision is what the hooks return: the raw allowed/reason from
 * the backend plus a pre-formatted, mode-aware tooltip string the
 * caller can drop into a <Tooltip content={…}>.
 *
 * `loading` is true while the first query is in flight. While loading
 * we report `allowed: true` so the SPA doesn't briefly grey out every
 * action on first paint — the click would still be backend-gated.
 * Once resolved, `allowed` reflects the apiserver's answer and stays
 * stable for ≥30s.
 */
export interface CanIDecision {
  allowed: boolean;
  reason: string;
  tooltip: string;
  loading: boolean;
}

const DENIED_FALLBACK: CanIResult = { allowed: false, reason: "" };

/** 30s — matches the backend's authorization.canICacheTTL default. */
const CANI_STALE_MS = 30_000;

// canonicalKey produces the cache-shareable string for one or many
// checks. We sort the checks by their stringified tuple so two callers
// asking "{delete,pods,default} + {patch,deployments,default}" and
// "{patch,deployments,default} + {delete,pods,default}" share an
// entry. Doesn't matter for correctness — the backend returns results
// in the same order it receives them — only for cache locality.
function checkTupleString(c: CanICheck): string {
  return [
    c.verb,
    c.group ?? "",
    c.resource,
    c.subresource ?? "",
    c.namespace ?? "",
    c.name ?? "",
  ].join("|");
}

function canonicalKey(checks: CanICheck[]): string {
  return checks.map(checkTupleString).sort().join("\x1e");
}

/**
 * useCanIBatch issues one POST /can-i carrying every check, returns
 * decisions in the same order. ResourceActions uses this so its 4-7
 * questions land in one SSRR-routed backend call.
 *
 * Pass an empty cluster string (or empty checks list) and the hook
 * skips the network call and returns "loading=true, allowed=true"
 * placeholders. Use this to noop the hook before the cluster prop
 * settles.
 */
export function useCanIBatch(
  cluster: string,
  checks: CanICheck[],
): CanIDecision[] {
  const { user } = useAuth();
  const enabled = cluster !== "" && checks.length > 0;
  const key = enabled ? canonicalKey(checks) : "";

  const query = useQuery<CanIResult[], ApiError>({
    queryKey: queryKeys.cluster(cluster).canI(key),
    queryFn: enabled
      ? async ({ signal }) => {
          const resp = await api.canI(cluster, checks, signal);
          return resp.results;
        }
      : skipToken,
    staleTime: CANI_STALE_MS,
    // Don't retry — the backend fails closed and we want the disabled
    // tooltip to surface fast rather than spinning.
    retry: 0,
  });

  // While loading, render allowed=true so first paint isn't a sea of
  // greyed-out buttons. Backend is the authoritative gate; if the
  // user clicks, the action's own 403 path still catches misses.
  return checks.map((check, i) => {
    const r = (() => {
      if (!enabled) return DENIED_FALLBACK;
      if (query.isError) {
        // Whole-batch failure: classify so the tooltip can be helpful.
        const reason =
          query.error instanceof ApiError
            ? mapApiErrorToReason(query.error)
            : "apiserver_unreachable";
        return { allowed: false, reason };
      }
      return query.data?.[i] ?? DENIED_FALLBACK;
    })();
    const loading = query.isLoading;
    const tooltip = formatCanIDeniedReason({
      result: r,
      check,
      authzMode: user?.authzMode,
      tier: user?.tier,
    });
    return {
      allowed: loading ? true : r.allowed,
      reason: r.reason,
      tooltip: loading ? "" : tooltip,
      loading,
    };
  });
}

/**
 * useCanI is sugar for a one-element batch. Single-check call sites
 * stay readable (`const can = useCanI(cluster, {...})`). The cache
 * key is keyed on the single check's tuple, so two components asking
 * the same question collapse onto one entry even though one uses the
 * batch hook and the other uses this one — both serialise the same
 * canonical tuple.
 *
 * The legacy shape `useCanI({verb, resource, namespace})` returning
 * boolean is gone; callers must pass cluster explicitly. Migration
 * is one line per call site.
 */
export function useCanI(cluster: string, check: CanICheck): CanIDecision {
  const [decision] = useCanIBatch(cluster, [check]);
  return decision ?? DENIED_DECISION;
}

const DENIED_DECISION: CanIDecision = {
  allowed: false,
  reason: "",
  tooltip: "",
  loading: false,
};

function mapApiErrorToReason(err: ApiError): string {
  switch (err.status) {
    case 401:
      return "auth_failed";
    case 403:
      return "denied";
    case 504:
    case 408:
      return "timeout";
    default:
      return "apiserver_unreachable";
  }
}
