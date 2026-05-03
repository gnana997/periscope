import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type { AuditQueryParams, AuditQueryResult } from "../lib/types";

/**
 * useAudit polls /api/audit on a 15s cadence with a 10s staleTime so
 * filter clicks don't immediately re-fetch.
 *
 * Visibility-aware behavior is the TanStack Query v5 default:
 * - refetchIntervalInBackground: false (default) → polling pauses when
 *   the tab is hidden
 * - refetchOnWindowFocus: true (default) → refetches on tab return
 *
 * The query key includes the full params so each unique filter
 * combination caches separately. Fast back/forward through pagination
 * hits cache instead of re-rolling the database.
 */
export function useAudit(params: AuditQueryParams, enabled = true) {
  return useQuery<AuditQueryResult>({
    queryKey: [...queryKeys.audit(), params],
    queryFn: ({ signal }) => api.audit(params, signal),
    refetchInterval: 15_000,
    staleTime: 10_000,
    enabled,
  });
}
