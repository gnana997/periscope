# Watch streams (real-time list updates)

Periscope's pod, event, replicaset, and job list pages update in
real time via Server-Sent Events (SSE) instead of polling. The SPA
falls back to polling for any kind whose stream cannot be opened,
so the bad case is graceful degradation rather than a hard failure.

This page is the operator guide: when watch streams are on, how to
opt out, how to restrict to a subset of kinds, the per-user
concurrency cap, and what to do when streams misbehave behind your
ingress / load balancer.

For the **contributor / architecture** view — how the
`watchKind[T,S]` primitive works, how to add a new kind, the
list-then-watch lifecycle — see
[`docs/architecture/watch-streams.md`](../architecture/watch-streams.md).

---

## 1. Default behavior

**On by default for all four shipped kinds:** pods, events,
replicasets, jobs. With no helm config, each list page in the SPA
opens an `EventSource` to `/api/clusters/{cluster}/{kind}/watch` and
reconciles deltas into the React Query cache as they arrive.

When a stream fails (404, network error, server tear-down), the SPA
automatically falls back to a polling `useResource` query for that
kind on that page. Both code paths return the same DTO shape, so
feature parity is automatic and a partial failure (e.g. only
`replicasets` 404s) doesn't break the rest of the page.

---

## 2. Helm configuration

```yaml
watchStreams:
  # Empty / "all" / "off" / "none" / comma-separated subset.
  # Leave empty to inherit the server default ("all on").
  kinds: ""

  # Concurrent SSE streams per OIDC subject. 0 disables the cap
  # entirely (not recommended).
  perUserLimit: 30
```

The `kinds` value accepts:

| Value | Meaning |
|---|---|
| `""` (unset) | Server default — all four kinds enabled |
| `"all"` | Same as unset; explicit form |
| `"off"` | Disable all SSE routes; UI uses polling everywhere |
| `"none"` | Same as `"off"` |
| `"pods,events"` | Comma-separated subset (any of: `pods`, `events`, `replicasets`, `jobs`) |

The schema rejects misspellings (`banana,pods` won't pass `helm
template`), so a typo fails at deploy time rather than silently
disabling a kind at runtime.

### Helm values ↔ env var mapping

The chart templates each `watchStreams.*` value to an env var on
the pod. Useful when debugging what's actually applied:

| Helm value | Env var | Notes |
|---|---|---|
| `watchStreams.kinds` | `PERISCOPE_WATCH_STREAMS` | Only rendered when non-empty (server default is "all on" when env is unset) |
| `watchStreams.perUserLimit` | `PERISCOPE_WATCH_PER_USER_LIMIT` | Only rendered when non-zero |

---

## 3. When to opt out

The opt-out exists for environments where SSE doesn't survive the
network path. Common triggers:

- **Buffering proxies.** An HTTP proxy / load balancer in front of
  Periscope that buffers responses (waiting for end-of-stream
  before forwarding) breaks SSE — events arrive in batches, or not
  at all. ALB and NLB are fine; some corporate forward-proxies are
  not.
- **Aggressive idle-connection timeouts.** A proxy that kills idle
  HTTP connections under ~30s. Periscope sends a heartbeat every
  15s on watch streams, but if the proxy ignores keep-alives or
  has a sub-15s ceiling, disconnects look like flapping streams.
- **WebSocket / SSE not allowed by policy.** Some regulated
  environments terminate long-lived connections by policy. The SPA
  copes by polling; opt out so the page state stays stable.

In each case, `watchStreams.kinds: "off"` is the right answer: the
SPA polls cleanly without the constant reconnect storm.

To restrict instead of disable (say, your proxy is fine for pods
but mangles event streams):

```yaml
watchStreams:
  kinds: "pods,replicasets,jobs"
```

---

## 4. Per-user concurrency cap

`watchStreams.perUserLimit` (default 30) caps concurrent SSE
streams per OIDC subject across all clusters and kinds. The cap
exists to stop a runaway SPA bug from opening hundreds of
EventSources and exhausting your apiservers' watch budget on the
user's behalf.

When a user hits the cap, the next watch request returns HTTP 429
and the SPA falls back to polling for the affected page. The 30
default reflects "10 list pages × 3 kinds at peak," which is well
above realistic usage.

Tune up if your team genuinely keeps lots of tabs open; tune down
if a misbehaving SPA build is hammering your apiservers and you
need a tighter circuit breaker. Setting it to `0` disables the cap
entirely (not recommended).

---

## 5. NetworkPolicy

Watch streams are entirely intra-cluster:

- **Inbound** to the Periscope pod from the SPA (browser → ingress
  → Periscope) — already covered by your ingress rules.
- **Outbound** from Periscope to each managed cluster's apiserver —
  the same TCP/443 you already need for non-watch reads.

No new egress rules. See
[`docs/setup/networkpolicy.md`](./networkpolicy.md) for the full
egress table.

---

## 6. Verifying

```sh
kubectl -n periscope port-forward svc/periscope 8080:8080

# 1. Stream is live for a kind:
curl -i -N http://localhost:8080/api/clusters/<cluster>/pods/watch
# expects: HTTP/1.1 200, Content-Type: text/event-stream, then a
# stream of `event: snapshot` followed by `event: added` / etc.

# 2. Stream is disabled for a kind (kinds: "off" or kind not in subset):
curl -i http://localhost:8080/api/clusters/<cluster>/replicasets/watch
# expects: HTTP/1.1 404 — the route literally doesn't exist; the SPA
# downgrades cleanly to polling.

# 3. Per-user cap is in effect:
curl -i http://localhost:8080/debug/streams
# JSON snapshot of every active stream this pod is serving;
# count rows for your actor to compare against perUserLimit.

# 4. Server-side defaults (kinds is unset):
kubectl -n periscope logs deploy/periscope | grep "watch streams"
# `watch streams enabled: pods=true events=true replicasets=true jobs=true`
```

---

## 7. Troubleshooting

### SSE connections drop every ~minute

Your proxy is killing idle connections faster than Periscope's
heartbeat. Two options:

1. **Tune the proxy.** ALB defaults to 60s idle timeout; bump it to
   300s+ for the Periscope target group. NLB defaults to 350s
   (fine). NGINX `proxy_read_timeout` defaults to 60s; bump to
   `1h` on the Periscope location block. The browser's reconnect
   keeps things working but the constant churn is wasteful.
2. **Opt out.** `watchStreams.kinds: "off"` and let polling carry
   the SPA. The fallback is tested and the UX difference at a 15s
   poll is small.

### Stream returns 404 immediately

The kind isn't enabled. Check `watchStreams.kinds` — is it `"off"`,
or a subset that doesn't include this kind? The SPA's behavior
here is correct (downgrade to polling); the 404 is by design when
a kind is gated off.

### `/debug/streams` shows the user at the cap

User has more than `perUserLimit` concurrent streams. Likely
causes: lots of open browser tabs, or a SPA build with a leak that
opens streams without closing them. Check whether the cap is
actually wrong for your deployment shape, or whether to file a SPA
bug.

### "Watch streams enabled: …=false" at startup despite leaving `kinds: ""`

Something else is setting `PERISCOPE_WATCH_STREAMS` (the `env:`
extra-env array, an operator-injected var, etc.). The helm
template only renders the var when `watchStreams.kinds` is
non-empty — but if another mechanism set it earlier, that wins.
Check `kubectl -n periscope describe pod` to see the resolved env.

### Page works but updates feel "polled" not "live"

EventSource opens, but the SPA never receives deltas. Most likely
a **buffering proxy** silently sitting on chunked responses. Check
your ingress controller config for response buffering / chunked
transfer support. Curl-test directly against the Service
(`kubectl port-forward`) to bypass the ingress and see if the
stream flows there — if it does, the ingress is the culprit.

---

## 8. Related docs

- [`docs/architecture/watch-streams.md`](../architecture/watch-streams.md) —
  contributor / architecture view: `watchKind[T,S]` primitive,
  list-then-watch lifecycle, adding a new kind.
- [`docs/setup/networkpolicy.md`](./networkpolicy.md) — egress
  table including watch-stream impact (none).
- [`docs/setup/deploy.md`](./deploy.md) §10.5 — feature summary in
  the deploy walkthrough.
