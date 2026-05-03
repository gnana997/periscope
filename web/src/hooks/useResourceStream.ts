// useResourceStream — opens a watch SSE for one (cluster, kind, namespace)
// and writes the resulting snapshot + deltas into the React Query cache
// at the same key useResource reads from. The hook only manages the
// side effect; it does not read the cache itself. The parent useResource
// owns the data interface (UseQueryResult) and dispatches between
// streaming and polling based on the returned streamStatus.
//
// Lifecycle:
//
//   1. EventSource opened on (cluster, resource, namespace) change.
//   2. snapshot   -> qc.setQueryData(key, wrapped) and status -> "live"
//   3. added/modified/deleted -> buffer 50ms, flush as one setQueryData
//      patching the cached list via lib/listShape primitives.
//   4. relist     -> ignore (next snapshot replaces cache; UI flicker
//                    is preferable to clearing then re-fetching).
//   5. server_shutdown / backpressure -> status "reconnecting"; native
//      EventSource auto-reconnect handles the actual reconnect.
//   6. auth_expired -> close stream and redirect to login via auth ctx.
//   7. error event with body -> log + status "reconnecting".
//   8. native onerror x4 within ~12s -> close, status "polling_fallback",
//      parent useResource picks up polling.
//   9. document.visibilityState='hidden' for >60s -> close stream.
//      Resume on 'visible' (will get a fresh snapshot).
//  10. Unmount -> close stream, abort buffer flush, drop visibility
//      listener.
//
// Reconnect uses native EventSource auto-reconnect for transport blips.
// The backend currently ignores Last-Event-ID and always replies with
// a fresh List+Watch on every connect — slight bandwidth cost vs. RV-
// resume, but it Just Works and avoids a backend change. Worth adding
// later as bandwidth optimization.

import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { useAuth } from "../auth/useAuth";
import {
  addRowToList,
  LIST_ITEMS_KEY,
  patchRowInList,
  removeRowFromList,
} from "../lib/listShape";
import { showToast } from "../lib/toastBus";
import { queryKeys } from "../lib/queryKeys";
import type { ResourceListResponse } from "../lib/types";
import type { WatchStreamKind } from "../lib/api";

export type StreamStatus =
  | "connecting"
  | "live"
  | "reconnecting"
  | "polling_fallback";

interface UseResourceStreamArgs {
  cluster: string | undefined;
  resource: WatchStreamKind;
  namespace?: string;
  // enabled lets the parent useResource skip the side effect entirely
  // (e.g. when the feature flag is off, or before features have loaded).
  enabled: boolean;
}

// FLUSH_INTERVAL_MS batches deltas into one setQueryData per window so
// a churn burst (e.g. node drain emitting 50 pod transitions/sec) costs
// one re-render of the consuming component, not 50.
const FLUSH_INTERVAL_MS = 50;

// MAX_RECONNECT_ATTEMPTS triggers the polling-fallback handoff. With
// debounced onerror counting, this works out to ~4 retry cycles before
// giving up — i.e. ~12s of failures.
const MAX_RECONNECT_ATTEMPTS = 4;

// VISIBILITY_HIDE_GRACE_MS: streams stay open this long after the tab
// goes hidden so a quick alt-tab doesn't close + relist + re-render.
const VISIBILITY_HIDE_GRACE_MS = 60_000;

// Each snapshot/delta from the backend carries a kind-specific DTO under
// `object` (single) or `items` (array). We don't type these any tighter
// than `unknown` — the backend is the source of truth and the React
// Query consumer (DataTable rows) already type-asserts via cast.
interface NamedRow {
  name: string;
  namespace?: string;
}

interface SnapshotEvent {
  resourceVersion: string;
  items: NamedRow[];
}

interface DeltaEvent {
  object: NamedRow;
}

type Delta =
  | { type: "added" | "modified"; row: NamedRow }
  | { type: "deleted"; row: NamedRow };

// wrapSnapshot constructs the kind-specific list-response shape (PodList
// = { pods: [...] }, ClusterEventList = { events: [...] }, etc.) using
// the existing LIST_ITEMS_KEY map. Using the same map that
// patchRowInList/removeRowFromList consume guarantees byte-shape parity
// with polled list responses.
function wrapSnapshot(
  kind: WatchStreamKind,
  items: NamedRow[],
): ResourceListResponse | undefined {
  const field = LIST_ITEMS_KEY[kind];
  if (!field) return undefined;
  return { [field]: items } as unknown as ResourceListResponse;
}

// applyDeltas reduces a batch of deltas onto the current cached list.
// Returns the same reference when the cache is empty (snapshot hasn't
// landed yet) — letting the next snapshot establish the baseline rather
// than constructing a partial list from individual deltas.
function applyDeltas(
  current: ResourceListResponse | undefined,
  kind: WatchStreamKind,
  deltas: Delta[],
): ResourceListResponse | undefined {
  if (!current) return current;
  let next: ResourceListResponse | undefined = current;
  for (const d of deltas) {
    switch (d.type) {
      case "added":
        next = addRowToList(next, kind, d.row);
        break;
      case "modified":
        next = patchRowInList(next, kind, d.row, () => d.row);
        break;
      case "deleted":
        next = removeRowFromList(next, kind, d.row);
        break;
    }
  }
  return next;
}

function buildWatchURL(
  cluster: string,
  resource: WatchStreamKind,
  namespace?: string,
): string {
  const base = `/api/clusters/${encodeURIComponent(cluster)}/${resource}/watch`;
  return namespace
    ? `${base}?namespace=${encodeURIComponent(namespace)}`
    : base;
}

export function useResourceStream(
  args: UseResourceStreamArgs,
): { streamStatus: StreamStatus } {
  const { cluster, resource, namespace, enabled } = args;
  const qc = useQueryClient();
  const auth = useAuth();
  const [status, setStatus] = useState<StreamStatus>("connecting");

  // Stable ref so the effect doesn't re-run on every signIn identity
  // change. Updated in a tiny effect of its own (auth.signIn is
  // memoized inside AuthProvider but still — refs from props need to
  // be assigned in an effect, not during render).
  const signInRef = useRef(auth.signIn);
  useEffect(() => {
    signInRef.current = auth.signIn;
  }, [auth.signIn]);

  useEffect(() => {
    if (!enabled || !cluster) return;

    // Reset status when args change so a stale "live" from the prior
    // (cluster, namespace) doesn't leak across the transition. Lint
    // warns about setState-in-effect; this is the canonical case
    // where it's correct — status tracks the EventSource lifecycle
    // which only the effect can observe.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setStatus("connecting");

    const queryKey = queryKeys
      .cluster(cluster)
      .kind(resource)
      .list(namespace ?? "");

    let closed = false;
    let es: EventSource | null = null;
    let buffer: Delta[] = [];
    let flushTimer: number | null = null;
    let visTimer: number | null = null;
    let reconnectAttempts = 0;
    let lastErrorAt = 0;

    setStatus("connecting");

    const flush = () => {
      flushTimer = null;
      if (closed || buffer.length === 0) return;
      const deltas = buffer;
      buffer = [];
      qc.setQueryData<ResourceListResponse | undefined>(queryKey, (current) =>
        applyDeltas(current, resource, deltas),
      );
    };

    const scheduleFlush = () => {
      if (flushTimer !== null) return;
      flushTimer = window.setTimeout(flush, FLUSH_INTERVAL_MS);
    };

    const open = () => {
      const url = buildWatchURL(cluster, resource, namespace);
      es = new EventSource(url);

      es.addEventListener("snapshot", (evt) => {
        try {
          const data = JSON.parse((evt as MessageEvent).data) as SnapshotEvent;
          const wrapped = wrapSnapshot(resource, data.items);
          if (wrapped) qc.setQueryData(queryKey, wrapped);
          // Fresh snapshot resets every error counter — by definition the
          // stream is healthy now.
          reconnectAttempts = 0;
          lastErrorAt = 0;
          setStatus("live");
        } catch {
          // Malformed snapshot — let onerror handle it on the next event.
        }
      });

      const enqueueDelta = (
        type: Delta["type"],
        evt: MessageEvent,
      ): void => {
        try {
          const data = JSON.parse(evt.data) as DeltaEvent;
          if (!data.object || typeof data.object.name !== "string") return;
          buffer.push({ type, row: data.object });
          scheduleFlush();
        } catch {
          // Malformed delta — drop silently; cache stays consistent.
        }
      };

      es.addEventListener("added", (evt) =>
        enqueueDelta("added", evt as MessageEvent),
      );
      es.addEventListener("modified", (evt) =>
        enqueueDelta("modified", evt as MessageEvent),
      );
      es.addEventListener("deleted", (evt) =>
        enqueueDelta("deleted", evt as MessageEvent),
      );

      // relist: server says the resource version is gone. Don't clear
      // the cache; the next snapshot will replace it. Briefly degraded
      // status until snapshot lands.
      es.addEventListener("relist", () => {
        setStatus("reconnecting");
      });

      // server_shutdown: backend SIGTERM during deploy. Native
      // EventSource auto-reconnect picks up the next pod within ~3s.
      es.addEventListener("server_shutdown", () => {
        setStatus("reconnecting");
      });

      // backpressure: server kicked us; auto-reconnect will get a
      // fresh snapshot. Single toast so users in a tab know there was
      // a hiccup; no toast spam if it cycles.
      es.addEventListener("backpressure", () => {
        setStatus("reconnecting");
      });

      // auth_expired: terminal. Close stream, redirect to login.
      es.addEventListener("auth_expired", () => {
        closed = true;
        es?.close();
        es = null;
        showToast("Session expired — signing in again", "warn", 4000);
        signInRef.current();
      });

      // Server-side error event with a body. Native onerror handles
      // transport-level; this is for explicit server-emitted errors.
      es.addEventListener("error", (evt) => {
        const me = evt as MessageEvent;
        if (typeof me.data === "string" && me.data.length > 0) {
          // Server-emitted error event with a JSON body.
          try {
            const body = JSON.parse(me.data) as { message?: string };
            console.warn(
              `[useResourceStream] ${resource}: ${body.message ?? "stream error"}`,
            );
          } catch {
            // Ignored; native onerror still runs.
          }
        }
        // Either way, status reflects the disruption.
        setStatus("reconnecting");

        // Debounced count: one increment per second, regardless of
        // burst frequency. Native onerror also fires through this
        // listener; tracking here avoids double counting.
        const now = Date.now();
        if (now - lastErrorAt < 1000) return;
        lastErrorAt = now;
        reconnectAttempts += 1;
        if (reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
          closed = true;
          es?.close();
          es = null;
          setStatus("polling_fallback");
        }
      });
    };

    const close = () => {
      if (es) {
        es.close();
        es = null;
      }
    };

    const onVisibility = () => {
      if (document.visibilityState === "hidden") {
        // Defer close so a quick alt-tab doesn't churn the stream.
        if (visTimer === null) {
          visTimer = window.setTimeout(() => {
            visTimer = null;
            close();
          }, VISIBILITY_HIDE_GRACE_MS);
        }
      } else {
        if (visTimer !== null) {
          window.clearTimeout(visTimer);
          visTimer = null;
        }
        if (!es && !closed && status !== "polling_fallback") {
          setStatus("connecting");
          open();
        }
      }
    };

    open();
    document.addEventListener("visibilitychange", onVisibility);

    return () => {
      closed = true;
      if (flushTimer !== null) {
        window.clearTimeout(flushTimer);
        flushTimer = null;
      }
      if (visTimer !== null) {
        window.clearTimeout(visTimer);
        visTimer = null;
      }
      document.removeEventListener("visibilitychange", onVisibility);
      close();
    };
    // status intentionally omitted: it's set by handlers in the same
    // effect closure; reading it here would re-run on every transition
    // and rebuild the EventSource, defeating the whole point.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled, cluster, resource, namespace, qc]);

  return { streamStatus: status };
}
