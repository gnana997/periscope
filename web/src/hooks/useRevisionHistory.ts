// useRevisionHistory — fetches the rollout history + pre-flight
// metadata for a Deployment / StatefulSet / DaemonSet (issue #71).
//
// Intentionally not polling: the revision picker is opened by an
// explicit user action (clicking the Rollback button), so a one-shot
// fetch with 30s stale time covers the common case (operator clicks,
// reads, picks; total elapsed < 30s) and a manual refresh covers the
// rest. Polling here would mean fetching the full pod-template payload
// every 15s for a dialog that's open for thirty seconds — wasted bytes.

import { useQuery } from "@tanstack/react-query";
import { api, type RollbackableKind } from "../lib/api";
import { queryKeys } from "../lib/queryKeys";
import type { RevisionHistory } from "../lib/types";

export interface UseRevisionHistoryArgs {
  cluster: string;
  kind: RollbackableKind;
  namespace: string;
  name: string;
  /** Set false to keep the hook installed (TanStack reservation) but
   *  not actually fetch — used by the dialog so the query only fires
   *  once the dialog opens, avoiding a fetch on every workload row. */
  enabled?: boolean;
}

export function useRevisionHistory(args: UseRevisionHistoryArgs) {
  return useQuery<RevisionHistory>({
    queryKey: queryKeys
      .cluster(args.cluster)
      .kind(args.kind)
      .revisions(args.namespace, args.name),
    queryFn: ({ signal }) =>
      api.revisions(args.cluster, args.kind, args.namespace, args.name, signal),
    enabled: args.enabled !== false,
    staleTime: 30_000,
  });
}
