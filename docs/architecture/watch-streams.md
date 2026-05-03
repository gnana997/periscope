# Watch streams

This is a **contributor / architecture** doc. The audience is someone
extending Periscope's real-time list updates to a new resource kind,
or debugging an existing one. Operator-facing config is documented
inline in [`deploy/helm/periscope/values.yaml`](../../deploy/helm/periscope/values.yaml)
and the audit / pod-exec pages — there is no helm config here because
watch streams are on by default for the four shipped kinds.

For the SSE plumbing on the frontend (`useResourceStream`,
`StreamHealthBadge`, drop-in dispatch into React Query), see the
relevant `web/` files; this page covers only the server side.

---

## 1. What "watch stream" means here

Periscope's resource list pages (Pods, Events, ReplicaSets, Jobs)
need to update in near-real-time. The naive answer is "poll every 5s
and diff in the client." We do better:

1. The browser opens an `EventSource` to `/api/clusters/{cluster}/{kind}/watch`.
2. The server runs a single **list-then-watch** loop against the
   apiserver under the impersonated user's identity.
3. The server emits SSE frames: one `Snapshot` event with the initial
   list, then `Added` / `Modified` / `Deleted` deltas as the apiserver
   reports them.
4. The frontend's React Query cache patches itself from the deltas —
   no full re-fetch.
5. On disconnect the browser's `EventSource` reconnects automatically;
   the server uses `Last-Event-ID` to resume from the last
   `resourceVersion` without re-emitting the full snapshot.

When watch is disabled (or the kind isn't supported), the SPA falls
back to a polling `useResource` query. Both code paths return the
same DTO shape, so feature parity is automatic.

---

## 2. The four shipped kinds

| Kind | Path | DTO | Code |
|---|---|---|---|
| Pods | `/api/clusters/{cluster}/pods/watch` | `Pod` | `internal/k8s/watch.go: WatchPods` |
| Events | `/api/clusters/{cluster}/events/watch` | `ClusterEvent` | `internal/k8s/watch.go: WatchEvents` |
| ReplicaSets | `/api/clusters/{cluster}/replicasets/watch` | `ReplicaSet` | `internal/k8s/watch.go: WatchReplicaSets` |
| Jobs | `/api/clusters/{cluster}/jobs/watch` | `Job` | `internal/k8s/watch.go: WatchJobs` |

Each is a thin wrapper around the generic `watchKind[T, S]` primitive.

---

## 3. The `watchKind[T, S]` primitive

```
  T = the Kubernetes API type    (e.g. corev1.Pod)
  S = the list-view DTO          (e.g. internal/k8s.Pod)
```

A `watchSpec[T, S]` carries the per-kind plumbing:

```go
type watchSpec[T any, S any] struct {
    Kind     string
    List     func(ctx, opts) ([]T, resourceVersion, error)
    Watch    func(ctx, opts) (watch.Interface, error)
    Summary  func(*T) S        // T → list-view DTO; same fn for snapshot and deltas
    PostList func([]S) []S     // optional: sort/cap snapshot before sending
}
```

`watchKind` runs the canonical lifecycle:

1. **If `resumeFrom != ""`** (client reconnected with `Last-Event-ID`):
   skip List, open Watch directly at that resourceVersion. **Do not
   emit a Snapshot** — the client cache is preserved across the blip.
2. **Else**: List → emit `Snapshot` → Watch from the list's RV.
3. Handle apiserver events:
   - `ADDED` / `MODIFIED` / `DELETED` → emit the corresponding SSE event.
   - `BOOKMARK` → no emit; the next relist refreshes the RV.
   - Status `410 Gone` → emit `Relist`, restart the loop.
4. **Stale `Last-Event-ID`** that the apiserver rejects with `410 Gone`
   on Watch open → fall through to a fresh List+Watch one time. Stale
   resume IDs degrade gracefully; they never hard-fail the stream.
5. ctx cancelled or `sink.Send` returned `false` (backpressure) →
   return `nil`.

The same `Summary` function projects T → S for both the snapshot and
each delta, so the frontend's cache patcher always operates on
shape-identical objects.

---

## 4. Why `PostList` exists (Events-shaped resources)

For most resources, the snapshot is the apiserver's full list. Events
are different: there are typically thousands of stale events, and the
list page only shows the most recent ~200. The `PostList` hook lets
`WatchEvents` sort newest-first and cap the snapshot to match
`ListClusterEvents` (the polled path).

Delta events bypass `PostList` — they're emitted raw, and the
frontend's `patchRowInList` reconciler decides whether each delta
fits the visible window. A `MODIFIED` for an event outside the top-N
is treated as `ADDED` by the patcher (standard upsert semantics).

When you add a new kind, leave `PostList` nil unless the snapshot
needs a kind-specific sort or cap.

---

## 5. The handler layer (`resourceWatchHandler`)

`cmd/periscope/main.go: resourceWatchHandler` is a generic SSE handler
factored over the `Watch*` primitives. For a new kind, you don't
write a fresh handler — you instantiate the generic one with:

- the route path
- a closure that calls your `Watch*` function
- the per-kind authz pre-flight (e.g. `list pods` SAR)

The handler takes care of:

- WebSocket upgrade rejection on non-SSE clients
- `Last-Event-ID` parsing and threading into `WatchArgs.ResumeFrom`
- Heartbeat frames every 15s (keeps proxies from killing the socket)
- Backpressure: if the client falls behind, `sink.Send` returns
  `false` and the watch loop exits cleanly
- Stream lifecycle: register with the per-actor limiter and the
  `streamTracker`; deregister on close
- Closing the stream on session expiry (the auth layer broadcasts
  `event:server_shutdown` and per-session expiry signals)

---

## 6. Per-user concurrency cap

`PERISCOPE_WATCH_PER_USER_LIMIT` (default 30) caps concurrent watch
streams per OIDC subject across all clusters and kinds. Exceeding it
returns HTTP 429. The cap exists to stop a runaway SPA bug from
opening hundreds of EventSources and exhausting apiserver watch
budget on the user's behalf.

The 30-stream default reflects "10 list pages × 3 kinds at peak,"
which is well above realistic usage. Tune via the env var if your
deployment pattern is unusual.

---

## 7. `/debug/streams`

`GET /debug/streams` returns a JSON snapshot of every active watch
stream:

```json
[
  {
    "id": 17,
    "actor": "auth0|alice",
    "cluster": "prod-eu-west-1",
    "kind": "pods",
    "namespace": "default",
    "openedAt": "2026-05-03T16:21:00Z"
  }
]
```

The endpoint is gated by the same authz that gates `/api/audit` (the
admin scope). It exists for operator visibility — "is the per-user
cap close to limit?", "which kinds dominate stream count?", "is a
specific actor leaking streams?" — and as a debugging aid when stream
counts look wrong.

---

## 8. Adding a new kind

The minimal recipe to add `Foos`:

### 8.1 The DTO

In `internal/k8s/foos.go`, define the list-view DTO and a `fooSummary`
function:

```go
type Foo struct {
    Namespace string `json:"namespace"`
    Name      string `json:"name"`
    UID       string `json:"uid"`
    // … only the fields the list page renders
}

func fooSummary(f *foosv1.Foo) Foo {
    return Foo{
        Namespace: f.Namespace,
        Name:      f.Name,
        UID:       string(f.UID),
        // …
    }
}
```

The DTO must be **stable** — the SSE delta and the polled list both
emit it; changing field names is a breaking change for the SPA cache.

### 8.2 `WatchFoos`

In `internal/k8s/watch.go`, add a thin wrapper:

```go
func WatchFoos(ctx context.Context, p credentials.Provider, args WatchArgs, sink WatchSink) error {
    cs, err := newClientFn(ctx, p, args.Cluster)
    if err != nil {
        return fmt.Errorf("build clientset: %w", err)
    }
    return watchKind(ctx, watchSpec[foosv1.Foo, Foo]{
        Kind: "foos",
        List: func(ctx context.Context, opts metav1.ListOptions) ([]foosv1.Foo, string, error) {
            list, err := cs.FoosV1().Foos(args.Namespace).List(ctx, opts)
            if err != nil { return nil, "", err }
            return list.Items, list.ResourceVersion, nil
        },
        Watch: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
            return cs.FoosV1().Foos(args.Namespace).Watch(ctx, opts)
        },
        Summary: fooSummary,
    }, args.ResumeFrom, sink)
}
```

If the snapshot needs sorting or capping, add a `PostList` field. If
not, leave it nil.

### 8.3 The route

In `cmd/periscope/main.go`, instantiate the generic handler:

```go
router.Get("/api/clusters/{cluster}/foos/watch",
    resourceWatchHandler("foos", k8s.WatchFoos))
```

The handler takes care of authz, limiter, tracker, heartbeat, resume,
and shutdown.

### 8.4 The feature flag

Append `foos` to `/api/features` in `cmd/periscope/main.go` so the
SPA knows the kind is supported. Frontends use this to choose
`useResourceStream` vs `useResource` per kind.

### 8.5 The test

Add a `TestWatchFoos_*` block in `internal/k8s/watch_test.go`. The
existing tests cover the generic primitive (snapshot, deltas, ctx
cancellation, backpressure) — your job is to confirm the
`fooSummary` projection is correct and the wrapper plumbing works.

---

## 9. Backpressure model

`WatchSink.Send` returns `bool`:

- `true` → the consumer accepted the event; loop continues.
- `false` → backpressure detected (the SSE buffer is full because the
  client fell behind). Loop exits cleanly with `nil`.

The SSE handler implements `Send` with a bounded channel; if the
channel is full, it returns `false` rather than block. The browser
sees the connection close and reconnects with `Last-Event-ID`, which
either resumes from the last RV or falls through to a fresh List.

Watch loops **must not** block on slow consumers. The whole point of
the SSE primitive is to scale to many concurrent slow clients without
pinning watcher goroutines.

---

## 10. Auth integration

Watch streams close cleanly on:

- Session expiry (`event:auth_expired` broadcast from the auth layer)
- Server shutdown (`event:server_shutdown`, sent before the binary's
  graceful-shutdown deadline expires)
- ctx cancellation (the request's HTTP context)

The frontend listens for these and either prompts re-auth
(`auth_expired`) or marks the stream `disconnected` and falls back to
polling (`server_shutdown`). New kinds inherit this for free — the
auth layer broadcasts to every registered stream.

---

## 11. What's intentionally not here

- **No write semantics.** Watch streams are read-only; the dynamic
  PATCH / DELETE handlers are separate.
- **No per-cluster watch budget.** Apiserver watch budget is a
  cluster-level concern that Periscope can't enforce — we cap per
  user instead, which is what the apiserver actually meters.
- **No batching of deltas.** Deltas emit as-is; the frontend's React
  Query cache patcher is fast enough that batching server-side
  doesn't help for the kinds we ship. Revisit if a future kind has
  pathological churn.
- **No stream multiplexing.** One EventSource per kind per page. We
  considered a single multiplexed socket; the complexity didn't pay
  off for v1.

---

## 12. Related code

- `internal/k8s/watch.go` — `watchKind`, `watchSpec`, the four
  `Watch*` wrappers
- `internal/k8s/watch_test.go` — generic primitive tests
- `internal/sse/` — SSE writer, heartbeat, last-event-id parsing
- `cmd/periscope/main.go` — `resourceWatchHandler`, `streamTracker`,
  `userStreamLimiter`, `/debug/streams`, `/api/features`
- `web/src/hooks/useResourceStream.ts` — frontend SSE consumer
- `web/src/components/StreamHealthBadge.tsx` — stream lifecycle UI
