// _useOptimistic — shared lifecycle for the three Lane 2 mutation hooks
// (scale, label edit, delete). Wraps useMutation with the canonical
// react-query optimistic shape:
//
//   onMutate     cancel in-flight queries, snapshot the affected
//                cache entries, apply the optimistic update.
//   onError      restore the snapshot, surface an error toast.
//   onSuccess    invalidate detail + meta + list (in that order) so the
//                next render reflects the server's authoritative state.
//                The await on meta is load-bearing: drift detection
//                polls every 15s and filters writes by manager
//                "periscope-spa" (lib/drift.ts:62) — we need the meta
//                query to refetch before the next drift evaluation, or
//                a false-positive banner can flash for fields the user
//                themselves just updated.
//
// We do NOT setQueryData(detailKey, applyResponse.object): the apply
// endpoint returns the raw K8s object, but the *Detail types in the
// cache are pre-flattened by the backend. The shapes are incompatible.
// Invalidation forces a refetch through the flattening pipeline.

import {
  useMutation,
  useQueryClient,
  type QueryKey,
  type UseMutationResult,
} from "@tanstack/react-query";
import { showToast } from "../../lib/toastBus";

interface OptimisticArgs<TVars, TSnap, TRes, TError> {
  /** All keys to invalidate in onSuccess and to restore on rollback. */
  detailKey: QueryKey;
  metaKey: QueryKey;
  listKey?: QueryKey;
  /** Apply the optimistic update; return a snapshot for rollback. */
  applyOptimistic: (qc: ReturnType<typeof useQueryClient>, vars: TVars) => TSnap;
  rollback: (qc: ReturnType<typeof useQueryClient>, snap: TSnap) => void;
  mutationFn: (vars: TVars) => Promise<TRes>;
  successToast: (vars: TVars) => string;
  errorToast: (err: TError, vars: TVars) => string;
  /** Suppress success toast (used by delete which has its own messaging). */
  successToneOverride?: { message: (v: TVars) => string; durationMs?: number };
}

export function useOptimisticMutation<
  TVars,
  TSnap,
  TRes = unknown,
  TError = Error,
>({
  detailKey,
  metaKey,
  listKey,
  applyOptimistic,
  rollback,
  mutationFn,
  successToast,
  errorToast,
}: OptimisticArgs<TVars, TSnap, TRes, TError>): UseMutationResult<
  TRes,
  TError,
  TVars,
  { snap: TSnap }
> {
  const qc = useQueryClient();

  return useMutation<TRes, TError, TVars, { snap: TSnap }>({
    mutationFn,
    onMutate: async (vars) => {
      // Cancel everything that could land mid-mutation and clobber
      // our optimistic write.
      await qc.cancelQueries({ queryKey: detailKey });
      if (listKey) await qc.cancelQueries({ queryKey: listKey });
      const snap = applyOptimistic(qc, vars);
      return { snap };
    },
    onError: (err, vars, ctx) => {
      if (ctx) rollback(qc, ctx.snap);
      showToast(errorToast(err, vars), "error", 5000);
    },
    onSuccess: async (_res, vars) => {
      // Order matters: detail first (visible cache), then meta (drift
      // gating), then list (cleanup). All awaited — the success toast
      // should not fire until the cache is reconciled.
      await qc.invalidateQueries({ queryKey: detailKey });
      await qc.invalidateQueries({ queryKey: metaKey });
      if (listKey) await qc.invalidateQueries({ queryKey: listKey });
      showToast(successToast(vars), "success", 2000);
    },
  });
}
