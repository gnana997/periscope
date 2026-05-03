// Server feature flags fetched once at app boot.
//
// Exposed via the standard React Query cache so any component can read
// the current state without a separate Context provider. The fetch is
// keyed on a top-level "features" key (no cluster scope — features are
// server-wide) and treated as effectively static for the session: long
// staleTime, no refetch on focus.
//
// useResource (Phase 6) calls isWatchStreamEnabled(kind) synchronously
// to decide whether to dispatch to the streaming or polling path. Until
// the features query resolves, the answer is `false` and the page polls;
// once it lands, the hook re-evaluates and switches in place.

import { useQuery } from "@tanstack/react-query";

import { api, type Features, type WatchStreamKind } from "./api";

const FEATURES_KEY = ["features"] as const;

export function useFeatures() {
  return useQuery<Features>({
    queryKey: FEATURES_KEY,
    queryFn: ({ signal }) => api.features(signal),
    // Features are server-side capability flags driven by env vars at
    // process start. They never change for the lifetime of the SPA
    // session, so cache effectively forever and never re-poll.
    staleTime: Infinity,
    gcTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: 2,
  });
}

// useIsWatchStreamEnabled returns true only when the features fetch has
// resolved AND the named kind is in the server's watchStreams list.
// While the fetch is pending or fails, callers default to polling.
export function useIsWatchStreamEnabled(kind: WatchStreamKind): boolean {
  const { data } = useFeatures();
  if (!data) return false;
  return data.watchStreams.includes(kind);
}
