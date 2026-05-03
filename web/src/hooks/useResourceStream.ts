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
import { useQueryClient, type QueryClient } from "@tanstack/react-query";

import { useAuth } from "../auth/useAuth";
import {
  addRowToList,
  LIST_ITEMS_KEY,
  patchRowInList,
  removeRowFromList,
} from "../lib/listShape";
import { showToast } from "../lib/toastBus";
import { queryKeys } from "../lib/queryKeys";
import { KIND_REGISTRY } from "../lib/k8sKinds";
import { useIsWatchStreamEnabled } from "../lib/features";
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
//
// `kind` is only populated by the events stream (where each row's
// involvedObject Kind decides which detail/events caches to invalidate).
// Every other stream's row carries an implicit kind = the stream's
// `resource` prop and leaves the field undefined.
interface NamedRow {
  name: string;
  namespace?: string;
  kind?: string;
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

// POD_AGGREGATING_PARENTS lists the URL-plural kinds whose detail panel
// embeds child Pods (rendered as a sub-table). When a Pod delta arrives,
// any open detail for these kinds in the same namespace is potentially
// stale, so we invalidate them too. React Query refetches only ACTIVE
// queries, so closed panels cost nothing — at most one parent detail is
// mounted at a time today.
const POD_AGGREGATING_PARENTS: ReadonlyArray<string> = [
  "deployments",
  "statefulsets",
  "daemonsets",
  "replicasets",
  "jobs",
  "cronjobs",
  "services",
];

// KIND_TO_PLURAL inverts KIND_REGISTRY so events-stream deltas (which
// carry K8s singular Kind in the row's `kind` field, e.g. "Pod") can be
// mapped back to the SPA's URL plural ("pods") for query-key
// invalidation. Built once at module load; unrecognized kinds (custom
// resources) are silently skipped.
const KIND_TO_PLURAL: Record<string, string> = (() => {
  const out: Record<string, string> = {};
  for (const [plural, meta] of Object.entries(KIND_REGISTRY)) {
    out[meta.kind] = plural;
  }
  return out;
})();

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

// invalidateRelated marks the detail/events/meta caches that a watch
// delta could have made stale, so any mounted detail panel refetches.
//
// The list cache is patched directly by applyDeltas; this function
// handles the *related* caches that aren't visible from the list shape:
//
//   1. Direct: the row's own detail/events/meta. For non-events streams
//      the row's kind is the stream's `resource`; for events streams
//      each row carries the involvedObject Kind separately.
//   2. Aggregating parents (pod streams only): every workload kind
//      whose detail panel embeds child pods needs to refetch when one
//      of its pods changes. We invalidate every active parent-detail
//      query in the same namespace via a predicate.
//
// React Query's invalidateQueries refetches only active (mounted)
// queries by default, so closed panels cost nothing. The deltas array
// is already deduped by name+namespace upstream (the 50ms flush
// coalesces bursts), but we still dedupe here in case a parent kind
// has multiple Pod deltas in the same flush window — one invalidate
// per (parentKind, namespace) is enough.
function invalidateRelated(
  qc: QueryClient,
  cluster: string,
  resource: WatchStreamKind,
  rows: NamedRow[],
): void {
  // Per-row direct invalidation: detail / events / meta on the row's
  // own kind. For non-events streams, the kind is `resource`; for
  // events streams, the row's `kind` field gives us the involvedObject
  // Kind (e.g. "Pod"), which we map back to URL-plural via
  // KIND_TO_PLURAL.
  const seen = new Set<string>();
  for (const row of rows) {
    let kind: string;
    if (resource === "events") {
      const mapped = row.kind ? KIND_TO_PLURAL[row.kind] : undefined;
      if (!mapped) continue; // unknown / CRD involvedObject — skip
      kind = mapped;
    } else {
      kind = resource;
    }
    const ns = row.namespace ?? "";
    const tag = `${kind} ${ns} ${row.name}`;
    if (seen.has(tag)) continue;
    seen.add(tag);

    const k = queryKeys.cluster(cluster).kind(kind);
    void qc.invalidateQueries({ queryKey: k.detail(ns, row.name) });
    void qc.invalidateQueries({ queryKey: k.events(ns, row.name) });
    void qc.invalidateQueries({ queryKey: k.meta(ns, row.name) });
  }

  // Pod-only: invalidate aggregating-parent detail queries in the same
  // namespace. Pod deltas drive Deployment/StatefulSet/.../Service
  // detail panels because those panels embed child-pod rows.
  if (resource === "pods") {
    const namespaces = new Set<string>();
    for (const row of rows) namespaces.add(row.namespace ?? "");
    for (const parent of POD_AGGREGATING_PARENTS) {
      for (const ns of namespaces) {
        void qc.invalidateQueries({
          predicate: (q) => {
            const key = q.queryKey;
            return (
              Array.isArray(key) &&
              key[0] === "cluster" &&
              key[1] === cluster &&
              key[2] === "kind" &&
              key[3] === parent &&
              key[4] === "detail" &&
              key[5] === ns
            );
          },
        });
      }
    }
  }
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
      // The list cache is patched directly above. Detail / events /
      // meta caches live under separate query keys and don't auto-
      // refetch on watch deltas — without this call, a Deployment
      // detail panel stays at its initial snapshot while the list
      // page updates live.
      invalidateRelated(
        qc,
        cluster,
        resource,
        deltas.map((d) => d.row),
      );
      // First delta arrival on a resumed stream signals "live": the
      // backend skipped the Snapshot because it accepted our
      // Last-Event-ID, so the snapshot handler never fires. Treat
      // any arriving delta as proof the stream is healthy. Idempotent
      // when status is already "live".
      setStatus("live");
      reconnectAttempts = 0;
      lastErrorAt = 0;
    };

    const scheduleFlush = () => {
      if (flushTimer !== null) return;
      flushTimer = window.setTimeout(flush, FLUSH_INTERVAL_MS);
    };

    const open = () => {
      const url = buildWatchURL(cluster, resource, namespace);
      es = new EventSource(url);

      es.addEventListener("snapshot", (evt) => {
        if (closed) return;
        try {
          const data = JSON.parse((evt as MessageEvent).data) as SnapshotEvent;
          const wrapped = wrapSnapshot(resource, data.items);
          if (wrapped) qc.setQueryData(queryKey, wrapped);
          // After a reconnect / 410-Gone relist, the cluster state may
          // have moved on while we were disconnected. Invalidate every
          // active detail / events / meta query for this resource in
          // one predicate sweep — cheaper than walking the snapshot
          // items (which can be 500+ on a large cluster). React Query
          // refetches only ACTIVE queries, so closed panels no-op.
          void qc.invalidateQueries({
            predicate: (q) => {
              const k = q.queryKey;
              return (
                Array.isArray(k) &&
                k[0] === "cluster" &&
                k[1] === cluster &&
                k[2] === "kind" &&
                k[3] === resource &&
                (k[4] === "detail" || k[4] === "events" || k[4] === "meta")
              );
            },
          });
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
        if (closed) return;
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
        if (closed) return;
        setStatus("reconnecting");
      });

      // server_shutdown: backend SIGTERM during deploy. Native
      // EventSource auto-reconnect picks up the next pod within ~3s.
      es.addEventListener("server_shutdown", () => {
        if (closed) return;
        setStatus("reconnecting");
      });

      // backpressure: server kicked us; auto-reconnect will get a
      // fresh snapshot. Single toast so users in a tab know there was
      // a hiccup; no toast spam if it cycles.
      es.addEventListener("backpressure", () => {
        if (closed) return;
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
        if (closed) return;
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
          // Tab-resume is a fresh start: clear the error budget so a single
          // reconnect blip after a long hide does not escalate to fallback
          // off the back of stale errors from before the tab went hidden.
          reconnectAttempts = 0;
          lastErrorAt = 0;
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

// useChildPodWatch — auxiliary Pod SSE subscription used by detail
// hooks that embed child pods (Deployment, StatefulSet, DaemonSet,
// ReplicaSet, Job, CronJob, Service). Without it, a pod's state can
// transition (Pending → Running, Running → Terminating → gone)
// without the parent object's resourceVersion changing — meaning the
// parent's stream fires no delta, and the embedded child-pods table
// goes stale until the next manual reload.
//
// With this hook mounted, pod deltas in the namespace flow through
// useResourceStream's pod-aggregating-parent fan-out (see
// POD_AGGREGATING_PARENTS) which invalidates any active workload
// detail in the same namespace. React Query refetches only the
// active query, so opening one panel costs at most one extra
// apiserver watch and one Get per pod transition. Stream closes
// automatically when the panel closes (enabled flips false).
//
// `enabled` is the panel-is-open gate. Internally we additionally
// gate on the server-side feature flag, so a deployment opt-out
// (PERISCOPE_WATCH_STREAMS without "pods") cleanly skips the
// auxiliary stream.
export function useChildPodWatch(
  cluster: string,
  namespace: string,
  enabled: boolean,
): void {
  const podsStreamEnabled = useIsWatchStreamEnabled("pods");
  useResourceStream({
    cluster,
    resource: "pods",
    namespace,
    enabled: enabled && podsStreamEnabled && Boolean(namespace),
  });
}
