// useCachedQueryData — read a value out of the React Query cache as a
// reactive subscription, WITHOUT registering as an observer of the
// underlying query.
//
// Why this exists: when multiple observers attach to the same queryKey,
// React Query stores ONE merged options object on the Query instance.
// In particular, `query.options.queryFn` is taken from the most recent
// observer to update its options. If a "passive subscriber" (one that
// only wants to read the cache) uses `useQuery({ queryFn: skipToken })`
// alongside the real fetcher hook, the queryFn can get clobbered to
// skipToken — at which point any cache invalidation triggers
// `ensureQueryFn` to log "Attempted to invoke queryFn when set to
// skipToken" and the refetch returns a rejecting promise. The detail
// panel then keeps its stale data because the refetch failed.
//
// useCachedQueryData sidesteps the issue by NOT being a useQuery
// observer at all. It uses useSyncExternalStore to subscribe to the
// QueryCache directly, filtered to the target queryHash, and returns
// the cached data via getQueryData. No observer, no options merge,
// no risk of poisoning the queryFn used by the real fetcher.
//
// Use cases: small UI bits (action toolbars, glyph badges, status
// chips) that want to react to cache updates produced by a sibling
// detail hook elsewhere in the tree.

import { useCallback, useMemo, useSyncExternalStore } from "react";
import { hashKey, useQueryClient, type QueryKey } from "@tanstack/react-query";

export function useCachedQueryData<T>(queryKey: QueryKey): T | undefined {
  const qc = useQueryClient();
  // hashKey is React Query's internal stable string representation.
  // Using it as the dependency for subscribe/getSnapshot keeps the
  // identity stable across renders even though queryKey arrays churn.
  const targetHash = useMemo(() => hashKey(queryKey), [queryKey]);

  const subscribe = useCallback(
    (notify: () => void) => {
      const cache = qc.getQueryCache();
      return cache.subscribe((event) => {
        if (event.query.queryHash === targetHash) {
          notify();
        }
      });
    },
    [qc, targetHash],
  );

  const getSnapshot = useCallback(
    () => qc.getQueryData<T>(queryKey),
    // queryKey identity is unstable across renders; targetHash is
    // the stable proxy that actually drives subscribe matching.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [qc, targetHash],
  );

  return useSyncExternalStore(subscribe, getSnapshot, () => undefined);
}
