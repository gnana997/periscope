# API reference

Periscope's HTTP API exists primarily to feed the embedded SPA. This
page documents what's covered by the v1.0 semver promise and what
isn't. It is **not** an exhaustive per-endpoint catalogue of every
list / detail / yaml / events route — those follow a small set of
patterns that this page documents once, rather than enumerating ~150
near-identical entries.

If you're looking for:

- **Operator basics** — verifying a deployment, writing health checks,
  granting audit-read access — the Tier 1 reference (3) and the
  authentication section (2) are what you want.
- **CLI / MCP integrators** — RFC 0001 (pod exec) and RFC 0002 (auth)
  describe the long-term contract those tools land against. Use this
  page to understand which HTTP surface is locked vs free to evolve.
- **SPA contributors** — the patterns in 4 are the contract; the
  generated TypeScript types in `web/src/api/` are the canonical
  field-by-field shape for SPA-internal endpoints.

---

## 1. Stability tiers

The v1.0 release promises semver on the HTTP API, but not every route
is the same kind of contract. Three tiers, each with different
guarantees:

| Tier | Coverage | Examples |
|---|---|---|
| **1 — Stable** | Path, method, request shape, response field names, and documented error classes are all covered by semver. Breaking changes require a major bump (v2). | `/healthz`, `/api/auth/*`, `/api/whoami`, `/api/features`, `/api/clusters`, `/api/fleet`, `/api/audit`, `/api/clusters/{c}/can-i` |
| **2 — SPA-coupled** | Path and method are stable. Response field **shapes can evolve in minor versions** (additive fields, new optional flags). The patterns in 4 are stable; specific field-level shapes track what the SPA needs. | The 130+ resource list / detail / yaml / events / logs / dashboard / search / CRD / customresources / helm / apply / delete / trigger / meta / secrets-data / openapi-proxy routes |
| **3 — Live channels** | Stream wire formats are stable (frozen and tested against the SPA). Path, transport (SSE / WebSocket), event names, and frame shape are all covered. Documented separately. | Watch streams (SSE), pod exec (WebSocket), pod and workload logs (SSE) |

**What is _not_ covered by semver in any tier:**

- `slog` field ordering on stdout (Go's `slog` does not promise this).
- Internal cache TTLs, fan-out concurrency, soft timeouts, retry
  backoffs.
- The `/debug/streams` page and any other path under `/debug/*`.
- Specific error wording in the human-readable `reason` / `message`
  fields. The error **classification** (HTTP status, code enum) is
  stable; the prose isn't, since most of it is `err.Error()` from
  `client-go`, which is upstream-defined.
- Anything not under `/api/*` or `/healthz`.

### URL versioning

Periscope does **not** prefix paths with `/v1/`. v1.0 ships routes at
`/api/...` directly. A future v2 with breaking changes will introduce
`/api/v2/...` alongside the existing `/api/...` so both can coexist
through a deprecation window. The unversioned form will keep working
through one major; v3 may finally drop it.

If you script against Periscope today, treat `/api/...` as "v1" and
plan for an additive migration when v2 ships, not a swap.

---

## 2. Authentication and sessions

### Modes

Periscope runs in one of two modes, set at startup via the auth
config file (`PERISCOPE_AUTH_FILE`):

- **`oidc`** — production. Authorization Code + PKCE, BFF pattern.
  The Go backend is the OAuth client; the SPA never sees a token.
  Tested against Auth0 and Okta; should work with any compliant
  IdP.
- **`dev`** — local development. No login screen; every request
  runs as a configured `dev.actor` identity. **Never enable in
  production**; it will be obvious from `/api/auth/config` if you do.

`GET /api/auth/config` is unauthenticated and returns just enough for
the SPA to render the login screen:

```json
{ "authMode": "oidc", "providerName": "Auth0" }
```

### OIDC login flow

```
SPA  →  GET /api/auth/login
     ←  302 → IdP /authorize (state + PKCE in short-lived periscope_login cookie)

User authenticates at IdP

IdP  →  GET /api/auth/callback?code=…&state=…
     ←  302 → /  (sets long-lived periscope_session cookie)
```

Endpoints:

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/auth/config` | Pre-auth config (mode, provider name). |
| GET | `/api/auth/login` | Begin OIDC. Sets `periscope_login`, redirects to IdP. |
| GET | `/api/auth/callback` | OIDC callback. Validates state + PKCE, exchanges code, sets `periscope_session`, redirects to `/`. |
| GET | `/api/auth/whoami` | Session introspection (subject, email, groups, mode, tier, audit scope, expiry). |
| GET | `/api/auth/logout` | Clear local session, redirect to IdP end-session. |
| GET | `/api/auth/logout/everywhere` | Same as above plus revoke all sessions for the same subject. |
| GET | `/api/auth/loggedout` | Post-IdP-logout landing page used by the SPA. |

### Cookies

| Name | Lifetime | Path | HttpOnly | Secure | SameSite | Purpose |
|---|---|---|---|---|---|---|
| `periscope_login` | 10 min | `/` | ✓ | (when HTTPS) | Lax | One-shot OIDC `state` + PKCE verifier. Cleared on callback. |
| `periscope_session` | configured (default 12 h) | `/` | ✓ | (when HTTPS) | Lax | Session id; lookup key into the in-memory session store. |

`Secure` is set automatically when the request reached the backend
over TLS, including via `X-Forwarded-Proto: https` from a trusted
reverse proxy. The cookie name is configurable; the default
`periscope_session` is documented here for grep/debugging.

The session value is a random opaque id, not a token. The store
holds a per-sub record with subject, email, groups, refresh token,
and absolute expiry; nothing sensitive lives in the cookie itself.

### Sessions are server-side and in-memory

v1.0 keeps the session record in process memory. Restarting the
pod **invalidates all sessions** — operators see a brief flash of
the login screen on first request after a deploy. This is also why
v1.0 supports a single replica when audit persistence is on (see
RFC 0003 3): session state has no shared store.

### Authorization on every API call

Every `/api/*` route except the seven `/api/auth/*` endpoints, the
SPA proxy, and `/healthz` runs through the auth middleware. An
unauthenticated request to a JSON endpoint gets `401 unauthenticated`
as plain text; an HTML request gets a `302` to `/api/auth/login`
(the SPA route guard relies on this).

Per-cluster Kubernetes authorization happens **inside** each handler
via the impersonating clientset built by `internal/credentials`.
The `Provider` carries the user's `Impersonate-User` and
`Impersonate-Group` headers; the apiserver evaluates RBAC against
the human, not the pod. This is what lets a Kubernetes denial show
up as `outcome: denied` in the audit log with the user's real
subject (RFC 0003 5).

### Bearer tokens / API keys

Not supported in v1.0. Periscope is a BFF: the SPA never holds a
token, so there's nothing to swap for an API key on the way out.
A future "service account" lane (machine identity + scoped
permissions) is post-v1 and will land alongside the CLI mentioned
in RFC 0002.

---

## 3. Tier 1 — stable endpoints

Endpoint paths, methods, request bodies, response field names, and
documented error classes are all covered by semver.

### `GET /healthz`

Liveness probe. Always returns `200 ok` once the server is accepting
connections. Does **not** authenticate cluster reachability — it's
a process liveness check, nothing more. Use the per-cluster
`status` field on `/api/fleet` for cluster reachability.

```
$ curl -s localhost:8080/healthz
ok
```

No request body. Plain-text response. No `Cache-Control`.

### `GET /api/auth/whoami`

Session introspection. The SPA calls this on first paint. Mirrors
what's used to render the user menu, audit nav gating, and tier
tooltips.

```json
{
  "subject":      "auth0|123",
  "email":        "alice@corp.example",
  "groups":       ["periscope-users", "Sec-Team"],
  "mode":         "oidc",
  "authzMode":    "tier",
  "tier":         "admin",
  "auditEnabled": true,
  "auditScope":   "all",
  "expiresAt":    1731000000
}
```

| Field | Notes |
|---|---|
| `subject` | OIDC `sub` claim. Stable across the user's lifetime at the IdP. |
| `email` | OIDC `email` claim, may be empty if the IdP doesn't ship it. |
| `groups` | Resolved IdP groups (config `authorization.groupsClaim`). |
| `mode` | Auth mode: `oidc` or `dev`. |
| `authzMode` | `shared`, `tier`, or `raw`. See `docs/setup/cluster-rbac.md`. |
| `tier` | Resolved tier name (tier mode only); empty otherwise. |
| `auditEnabled` | Whether `/api/audit` is registered. |
| `auditScope` | `self` or `all`. See RFC 0003 11. Only present when `auditEnabled`. |
| `expiresAt` | Unix seconds (UTC) of the session's absolute expiry. |

`401 unauthenticated` if no valid session. There is also a
`/api/whoami` route (no `auth` prefix) that returns a smaller actor
slice; both are stable, but the `/api/auth/whoami` form is
recommended for anything that needs the audit / tier fields.

### `GET /api/whoami`

Identity slice keyed off the impersonated `Provider`:

```json
{
  "actor":        "alice@corp.example",
  "auditEnabled": true,
  "auditScope":   "self",
  "mode":         "tier",
  "tier":         "triage"
}
```

`actor` is the `Provider.Actor()` string — usually the email, falling
back to the OIDC subject. Both forms exist for historical reasons;
`/api/auth/whoami` is the richer payload and what the SPA uses.

### `GET /api/features`

Reports the operator-controlled feature set the SPA should enable.
Used to gate UI without the SPA needing to know about
`PERISCOPE_*` env vars.

```json
{
  "watchStreams": ["pods", "events", "deployments", "..."]
}
```

The `watchStreams` array lists kinds for which the SSE watch route
is registered. The list is in registry order (stable across
restarts) and is the single source of truth for what the SPA can
subscribe to. Empty array means the operator opted out
(`PERISCOPE_WATCH_STREAMS=off`).

### `GET /api/clusters`

The cluster registry as the SPA sees it. No fan-out, no apiserver
reach — this is configuration introspection.

```json
{
  "clusters": [
    {
      "name":           "prod-eu",
      "backend":        "eks",
      "arn":            "arn:aws:eks:eu-west-1:1234567890:cluster/prod-eu",
      "region":         "eu-west-1",
      "execEnabled":    true
    },
    {
      "name":           "dev",
      "backend":        "kubeconfig",
      "kubeconfigPath": "/etc/periscope/kube/dev.yaml",
      "kubeconfigContext": "dev-admin",
      "execEnabled":    false
    }
  ]
}
```

`execEnabled` is the per-cluster derived flag — `false` when an
operator set `clusters[i].exec.enabled: false` in Helm values. The
SPA hides the "Open Shell" action when it's false; the API returns
`403 E_EXEC_DISABLED` if a client tries anyway.

### `GET /api/fleet`

Multi-cluster aggregator behind the home page. Fans out under the
caller's identity (impersonated calls per cluster), 2 s per-cluster
soft timeout, total budget capped at 8 s. 10 s server-side TTL
cache keyed by actor + impersonation groups.

Page-level `403` when the user has no tier at all (tier mode +
unmapped groups). Otherwise per-cluster errors are surfaced inline:

```json
{
  "rollup": {
    "totalClusters": 4,
    "byStatus":      { "healthy": 3, "unreachable": 1 },
    "byEnvironment": { "prod": 2, "stage": 2 },
    "generatedAt":   "2026-05-04T12:34:56Z"
  },
  "clusters": [
    {
      "name":        "prod-eu",
      "backend":     "eks",
      "region":      "eu-west-1",
      "environment": "prod",
      "status":      "healthy",
      "lastContact": "2026-05-04T12:34:55Z",
      "summary": {
        "nodes":         { "ready": 18, "total": 20 },
        "pods":          { "running": 412, "pending": 3, "failed": 0, "total": 415 },
        "namespaces":    24,
        "stuckOrFailed": 3
      },
      "hotSignals": [{ "kind": "ImagePullBackOff", "count": 2 }]
    },
    {
      "name":   "prod-us",
      "status": "unreachable",
      "error":  { "code": "apiserver_unreachable", "message": "..." }
    }
  ]
}
```

Status enum (stable, additions are additive):
`healthy` · `degraded` · `unreachable` · `unknown` · `denied`.

Per-cluster error codes — the same enum used elsewhere (6).

### `GET /api/audit`

Persisted audit query. **Registered only when SQLite is enabled and
opened successfully** (otherwise 404). Full contract — request shape,
response shape, retention semantics, RBAC, semver coverage — lives
in [RFC 0003 11](rfcs/0003-audit-log.md). One-line summary here:

```
GET /api/audit?
    actor=<sub>&verb=<v>&outcome=<o>&cluster=<c>
    &namespace=<ns>&name=<n>&request_id=<id>
    &from=<RFC3339Nano>&to=<RFC3339Nano>
    &limit=1..500&offset=N
```

Returns `{ items, total, limit, offset }` with a stable `Row` shape
documented in RFC 0003 6. `X-Audit-Scope: self` or `all` header
indicates whether the server hard-overrode the actor filter to the
caller's own subject.

### `POST /api/clusters/{cluster}/can-i`

Pre-flight RBAC check. The SPA uses this to grey out actions the
user cannot perform (replacing the click → 403 → red banner UX with
a disabled button + tooltip). Hits `SelfSubjectAccessReview` /
`SelfSubjectRulesReview` under the user's impersonated identity.

```json
POST /api/clusters/prod-eu/can-i
{
  "checks": [
    { "verb": "delete", "group": "apps", "resource": "deployments", "namespace": "platform" },
    { "verb": "create", "group": "",     "resource": "pods/exec",   "namespace": "platform", "subresource": "exec" }
  ]
}

→ 200 OK
{
  "results": [
    { "allowed": true,  "reason": "" },
    { "allowed": false, "reason": "no RBAC rule grants \"create\" on \"pods/exec\"" }
  ]
}
```

`results[i]` corresponds positionally to `checks[i]`. Maximum 64
checks per request (returns `400` if exceeded). 30 s per-actor TTL
cache. Anonymous callers and apiserver errors fail closed
(`allowed: false`).

---

## 4. Tier 2 — SPA-coupled patterns

The remaining ~130 endpoints follow eight patterns. Specific
field-level shapes track the SPA's needs and may gain additive
fields in minor versions; the path patterns and verbs below are
stable.

### Pattern: list

```
GET /api/clusters/{cluster}/{plural}
GET /api/clusters/{cluster}/{plural}?namespace={ns}
```

Where `{plural}` is one of: `nodes` · `namespaces` · `pods` ·
`deployments` · `statefulsets` · `daemonsets` · `replicasets` ·
`services` · `ingresses` · `configmaps` · `secrets` · `jobs` ·
`cronjobs` · `pvcs` · `pvs` · `storageclasses` · `roles` ·
`clusterroles` · `rolebindings` · `clusterrolebindings` ·
`serviceaccounts` · `horizontalpodautoscalers` ·
`poddisruptionbudgets` · `networkpolicies` · `endpointslices` ·
`resourcequotas` · `limitranges` · `ingressclasses` ·
`priorityclasses` · `runtimeclasses`.

Cluster-scoped kinds (nodes, namespaces, pvs, storageclasses,
clusterroles, clusterrolebindings, ingressclasses, priorityclasses,
runtimeclasses) ignore the `?namespace=` query param.

Response shape: `{ "items": [<DTO>...], ... }` where `<DTO>` is the
trimmed projection of the corresponding kind. Field names are
stable; new fields may be added in minor versions.

### Pattern: detail

```
GET /api/clusters/{cluster}/{plural}/{ns}/{name}      # namespaced
GET /api/clusters/{cluster}/{plural}/{name}           # cluster-scoped
```

Returns the same `<DTO>` shape as the list endpoint, possibly with
extra detail fields the list doesn't carry. Use the list shape as
the contract; detail-only fields are best-effort additions.

### Pattern: yaml

```
GET /api/clusters/{cluster}/{plural}/{ns}/{name}/yaml
```

Returns `Content-Type: application/yaml` (raw YAML, not JSON-wrapped).
Used by the Monaco editor as the canonical edit source. SSA
field-ownership annotations are preserved.

### Pattern: events

```
GET /api/clusters/{cluster}/{plural}/{ns}/{name}/events
GET /api/clusters/{cluster}/events                      # cluster-wide
```

Returns `{ "items": [<ClusterEvent>...] }`. Each event carries a
stable `uid` field for SPA cache identity (added in 1.x; pre-uid
DTOs are not produced by v1.0+).

### Pattern: logs (SSE)

```
GET /api/clusters/{cluster}/pods/{ns}/{name}/logs?container=&follow=true&tailLines=100
GET /api/clusters/{cluster}/{workload}/{ns}/{name}/logs?...
```

Server-Sent Events stream. See 5 for the live-channel contract.
`workload` ∈ `deployments`, `statefulsets`, `daemonsets`, `jobs`.

### Pattern: apply (Server-Side Apply)

```
PATCH /api/clusters/{cluster}/resources/{group}/{version}/{resource}/{ns}/{name}
PATCH /api/clusters/{cluster}/resources/{group}/{version}/{resource}/{name}    # cluster-scoped
?dryRun=true&force=true
```

Body is YAML (`Content-Type: application/yaml`), sent through
Kubernetes Server-Side Apply with `application/apply-patch+yaml`.
Returns the applied object on success. `dryRun=true` validates
without mutating; `force=true` claims field ownership over
conflicts.

Audit-emitted as verb `apply`. Conflicts return `409` with a
`metav1.Status` body whose `details.causes[]` carries per-field
conflict info — the SPA uses this for the conflict resolver.

`group=core` is rewritten to the empty string server-side so
core-API resources can use the same URL pattern.

### Pattern: delete

```
DELETE /api/clusters/{cluster}/resources/{group}/{version}/{resource}/{ns}/{name}
DELETE /api/clusters/{cluster}/resources/{group}/{version}/{resource}/{name}     # cluster-scoped
```

Audit-emitted as verb `delete`. `204` on success. `404` is treated
as success at the API level (idempotent delete).

### Pattern: meta

```
GET /api/clusters/{cluster}/resources/{group}/{version}/{resource}/{ns}/{name}/meta
GET /api/clusters/{cluster}/resources/{group}/{version}/{resource}/{name}/meta
```

Lightweight metadata-only fetch. Used by the SPA before opening the
editor to populate field-ownership glyphs and conflict resolution
without re-fetching the whole object.

### One-off endpoints (Tier 2, not pattern)

| Method | Path | Notes |
|---|---|---|
| GET | `/api/clusters/{c}/dashboard` | Per-cluster summary (counts + hot signals). Same shape as `/api/fleet` per-cluster summary. |
| GET | `/api/clusters/{c}/search?q=&kinds=&limit=` | Cmd+K palette. Returns up to N matches per kind. |
| GET | `/api/clusters/{c}/crds` | List CRDs. |
| GET | `/api/clusters/{c}/customresources/{group}/{version}/{plural}[/...]` | List / detail / yaml / events of CRs (mirrors built-in patterns). |
| GET | `/api/clusters/{c}/secrets/{ns}/{name}/data/{key}` | Decoded secret value. Audit-emitted as `secret_reveal`. |
| GET | `/api/clusters/{c}/openapi/v3` and `.../openapi/v3/*` | Proxy to apiserver `/openapi/v3` for the editor's schema-aware autocomplete. |
| POST | `/api/clusters/{c}/cronjobs/{ns}/{name}/trigger` | One-shot Job from a CronJob. Audit-emitted as `trigger`. |
| GET | `/api/clusters/{c}/nodes/{name}/metrics`, `/api/clusters/{c}/pods/{ns}/{name}/metrics` | metrics.k8s.io passthrough. |
| GET | `/api/clusters/{c}/helm/releases` | List. `{ releases, truncated }`. Cap 200; `truncated: true` when the cluster has more. |
| GET | `/api/clusters/{c}/helm/releases/{ns}/{name}?revision=N` | Per-revision detail (values, manifest, parsed resources). 5 MiB cap. |
| GET | `/api/clusters/{c}/helm/releases/{ns}/{name}/history?max=N` | Revision metadata list. Default `max=10`, range `1..100`. |
| GET | `/api/clusters/{c}/helm/releases/{ns}/{name}/diff?from=N&to=M` | `dyff`-based structured diff between revisions. |

Helm write operations (rollback / upgrade / install / uninstall)
are deliberately **not** in v1.0 — they need the compound SAR
fan-out layer to land first. v1.x.

---

## 5. Tier 3 — live channels

### Watch streams (SSE)

```
GET /api/clusters/{cluster}/{kind}/watch[?namespace={ns}][&Last-Event-ID=...]
```

Where `{kind}` is one of the names returned by
`GET /api/features.watchStreams`. Wire format is **frozen** — the
SPA depends on it:

```
event: snapshot
id: <resourceVersion>
data: {"resourceVersion":"<rv>","items":[<DTO>...]}

event: added | modified | deleted
id: <resourceVersion>
data: {"object":<DTO>}

event: relist
data: {"reason":"gone_410"}

event: backpressure
data: {}

event: server_shutdown | auth_expired
data: {}

event: error
data: {"message":"..."}
```

`<DTO>` is the same shape returned by the matching list endpoint, so
the SPA cache patches against type-identical objects.

`Last-Event-ID` (standard SSE header, also accepted as a query
param of the same name) lets a transient disconnect resume from the
last seen `resourceVersion` rather than re-listing.

A per-user concurrency cap (`PERISCOPE_WATCH_PER_USER_LIMIT`,
default 60) bounds open streams per OIDC subject. When a user is
at the cap, opening a 61st stream returns the `error` event with
`{"message":"watch stream cap reached"}` and closes; the SPA falls
back to polling for that view.

Operator opt-out via `PERISCOPE_WATCH_STREAMS` (subset, group
aliases, `off`). See `docs/setup/watch-streams.md` for the full
operator guide and `docs/architecture/watch-streams.md` for the
push-model design.

### Pod logs (SSE)

```
GET /api/clusters/{c}/pods/{ns}/{name}/logs?container=&follow=true&tailLines=100&previous=false
GET /api/clusters/{c}/{workload}/{ns}/{name}/logs?... (deployment/sts/ds/job)
```

SSE with `event: log` frames carrying timestamped lines. Aborts
when the client closes the connection; respects context-cancel.

Workload-level routes auto-fan-out across the workload's child pods
and tag each line with the source pod.

A future `log_open` audit verb (RFC 0003 4) will be emitted here;
not yet wired.

### Pod exec (WebSocket)

```
GET /api/clusters/{c}/pods/{ns}/{name}/exec?container=&command=&tty=true
   ↑ HTTP 101 Upgrade → WebSocket
```

Bidirectional WebSocket bridging the browser terminal to the
apiserver `/exec` stream (`FallbackExecutor` — WebSocket v5 with
SPDY fallback). Full protocol — frame schema, channel multiplexing,
idle / visibility timers, reconnect semantics, audit shape — lives
in [RFC 0001](rfcs/0001-pod-exec.md). One paragraph here for
context:

- Identity is per-user via impersonation. The audit row names the
  human who opened the shell, not the pod identity.
- Two audit emissions per session: `exec_open` immediately after
  the apiserver accepts, `exec_close` once the stream returns. The
  `Reason` field carries the close disposition (`completed` /
  `idle_timeout` / `abort` / `server_error`). See RFC 0003 4.
- Concurrent sessions per user are bounded; the cap message lists
  active sessions with disconnect controls.
- Stdin payloads never appear in logs or audit fields — only the
  byte counts (`bytes_stdin` / `bytes_stdout`).

---

## 6. Conventions

### JSON

`Content-Type: application/json; charset=utf-8` on all JSON
responses. Field names use lowerCamelCase. Empty / absent optional
fields are omitted (`omitempty`); arrays are emitted as `[]` rather
than `null`.

Times are RFC3339 with nanosecond precision in UTC
(`2026-05-04T12:34:56.789Z`). The audit reader accepts the same
format on `?from=` / `?to=`. Unix-second integers appear only on
`/api/auth/whoami.expiresAt` for legacy reasons.

### Request id

Every request gets a chi-generated request id, returned in
`X-Request-Id` and threaded into both access-log and audit-log
lines. Clients may pass `X-Request-Id` to override; it's preserved
end-to-end. The same id appears in audit rows under
`requestId` / `request_id` so a user-visible error can be tied
back to one persisted audit row.

### Errors

For Kubernetes errors, the response body is the upstream
`metav1.Status` JSON shape:

```json
{
  "kind":    "Status",
  "status":  "Failure",
  "message": "deployments.apps \"foo\" already exists",
  "reason":  "AlreadyExists",
  "details": {
    "name":   "foo",
    "group":  "apps",
    "kind":   "deployments",
    "causes": [ { "field": "spec.replicas", "message": "...", "reason": "..." } ]
  },
  "code":    409
}
```

The `details.causes[]` array drives the apply-conflict resolver in
the SPA (per-field "keep mine / take theirs"). Non-Kubernetes
errors fall back to plain text.

The HTTP status mapping (`cmd/periscope/errors.go::httpStatusFor`) is:

| `client-go` classifier | HTTP status |
|---|---|
| `IsForbidden` | 403 |
| `IsUnauthorized` | 401 |
| `IsNotFound` | 404 |
| `IsConflict` | 409 |
| `IsTimeout` / `IsServerTimeout` | 504 |
| `IsTooManyRequests` | 429 |
| `IsBadRequest` | 400 |
| _other_ | 500 |

### Aggregator error codes

`/api/fleet` (and any future aggregator) returns a stable enum on
each per-cluster error rather than raw `client-go` strings:

| Code | When |
|---|---|
| `denied` | Forbidden (403). |
| `auth_failed` | Unauthorized (401) — typically the pod's IRSA / Pod Identity binding broken. |
| `timeout` | Per-cluster soft timeout or `context deadline exceeded`. |
| `apiserver_unreachable` | Network error, dial failure, generic 5xx. |
| `unknown` | Anything else. |

Treat the set as **additive**: new codes may be added in minor
versions; existing codes are stable.

### CSRF

Periscope's CSRF posture rests on three layers, not on a synchronizer
token (none is issued in v1.0):

1. **`periscope_session` is `SameSite=Lax`.** Cross-site `POST`,
   `PATCH`, `DELETE`, and the WebSocket upgrade do *not* receive the
   cookie at all, so a malicious page cannot drive a state-changing
   request as the user. Lax (rather than Strict) is required so the
   cookie is sent on the post-OIDC-callback redirect to `/`; Strict
   would silently break sign-in. The cookie is also `HttpOnly`, so
   it is unreadable from page JS even on same-origin contexts.
2. **State-changing endpoints accept JSON or YAML only.** `apply` is
   `application/yaml`; `trigger` and other POSTs are `application/json`.
   The two body types a `<form>` can submit cross-site without a
   preflight (`application/x-www-form-urlencoded` and
   `multipart/form-data`) are not parsed by any state-changing handler.
   A cross-site attacker would need to issue a true XHR, which is
   blocked by CORS — Periscope sets no permissive
   `Access-Control-Allow-Origin` headers.
3. **The exec WebSocket checks `Origin`.** Same-origin in production;
   `PERISCOPE_DEV_ALLOW_ORIGINS` widens the allowlist for local dev
   (Vite proxy on `:5173` → backend on `:8088`).

If you front Periscope with a proxy that strips `SameSite` or rewrites
request bodies into form encoding, evaluate your CSRF posture
separately.

### Pagination

Only `/api/audit` paginates today (`?limit=&offset=`). List endpoints
return the full result set up to a server-side cap (200 helm
releases; ~1000 namespace scopes for the cluster-wide search; full
list otherwise — Kubernetes pagination is not yet exposed). A
future minor version may add `?continue=` token pagination on list
endpoints; that's additive and won't break callers that ignore it.

---

## 7. SPA, dev, and debug

These exist but are **not part of the API contract**:

- **SPA static assets** — `GET /` and any non-API path served by
  `internal/spa.Handler()` when the embedded SPA is built in. May
  be replaced with `index.html` on a SPA-native rewrite. Don't
  script against any specific path; treat `/` as opaque.
- **`GET /debug/streams`** — JSON snapshot of currently-open watch
  streams. Useful for diagnosing "did this user blow the per-user
  cap." Format may change between versions.

---

## 8. Forward roadmap

| When | What |
|---|---|
| v1.x | Helm write paths (rollback / upgrade) once the compound SAR layer lands. Additive: new methods on existing helm paths. |
| v1.x | `log_open` audit emission for the SSE log streams. Additive: new audit verb (RFC 0003 4 reserves it). |
| v1.x | `periscope-rbac` CLI (RFC 0002). Will use the existing `/api/clusters/*` and `/api/auth/whoami` surfaces. |
| v2 | Anything that breaks the contracts in 3 or 4 (path moves, removed fields, renamed enums). Expect `/api/v2/...` alongside `/api/...` through one major's deprecation window. |
| v3 | RFC 0001 3 — MCP tool exposure. Will reuse the per-cluster typed function layer; HTTP API stays as the human-facing surface. |

---

## 9. References

- [RFC 0001 — Pod exec support](rfcs/0001-pod-exec.md) — exec
  WebSocket frame schema, identity propagation, acceptance criteria.
- [RFC 0002 — Authentication (OIDC + per-user K8s authz)](rfcs/0002-auth.md) —
  the three authz modes, group resolution, impersonation contract.
- [RFC 0003 — Audit log: schema and retention semantics](rfcs/0003-audit-log.md) —
  full `/api/audit` reference and event shape.
- [`docs/setup/audit.md`](setup/audit.md) — operator-facing audit
  configuration and RBAC.
- [`docs/setup/watch-streams.md`](setup/watch-streams.md) — operator
  guide for the SSE watch surface.
- [`docs/setup/cluster-rbac.md`](setup/cluster-rbac.md) — in-cluster
  RBAC the backend needs and the three authz modes.
- [`docs/architecture/watch-streams.md`](architecture/watch-streams.md) —
  push-model design behind Tier 3 watch streams.
