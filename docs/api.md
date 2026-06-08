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
| **1 — Stable** | Path, method, request shape, response field names, and documented error classes are all covered by semver. Breaking changes require a major bump (v2). | `/healthz`, `/api/auth/*`, `/api/whoami`, `/api/features`, `/api/clusters`, `/api/fleet`, `/api/audit`, `/api/clusters/{c}/can-i`, `/api/agents/*` |
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
      "name":                 "prod-eu",
      "backend":              "eks",
      "arn":                  "arn:aws:eks:eu-west-1:1234567890:cluster/prod-eu",
      "region":               "eu-west-1",
      "execEnabled":          true,
      "clusterShellEnabled":  true,
      "clusterShellMode":     "bash"
    },
    {
      "name":                 "dev",
      "backend":              "kubeconfig",
      "kubeconfigPath":       "/etc/periscope/kube/dev.yaml",
      "kubeconfigContext":    "dev-admin",
      "execEnabled":          false,
      "clusterShellEnabled":  false
    }
  ]
}
```

`execEnabled` is the per-cluster derived flag — `false` when an
operator set `clusters[i].exec.enabled: false` in Helm values. The
SPA hides the "Open Shell" action when it's false; the API returns
`403 E_EXEC_DISABLED` if a client tries anyway.

`clusterShellEnabled` and `clusterShellMode` mirror
`PERISCOPE_CLUSTER_SHELL_ENABLED` and `PERISCOPE_CLUSTER_SHELL_MODE`
on the server (issue #104). When `false`, the SPA hides the
**shell** button in the cluster page header; the API returns
`403 E_CLUSTER_SHELL_DISABLED` if a client tries anyway.
`clusterShellMode` is omitted when shell is disabled and is one of
`bash` or (future) `kubectl-only` otherwise. The shell toggle is
currently server-wide — the per-cluster shape lets a future release
add per-cluster overrides without changing the wire format.

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
### `POST /api/agents/tokens`

Mint a single-use bootstrap token for registering a `backend: agent`
cluster. **Admin tier only** — non-admin sessions get `403`. Agent
endpoints are documented in detail in
[`docs/architecture/agent-tunnel.md`](architecture/agent-tunnel.md).

```json
POST /api/agents/tokens
{ "cluster": "prod-eu" }

→ 200 OK
{
  "token":     "abc123...",
  "cluster":   "prod-eu",
  "expiresAt": "2026-05-04T12:49:56.789Z"
}
```

| Field | Notes |
|---|---|
| `cluster` | Cluster name. Must match the DNS-1123-ish shape: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 1-63 chars. The token is bound to this name; an agent claiming a different name on registration burns the token. |
| `token` | 32 random bytes, base64url-encoded (URL-safe, no padding). Single-use. Show to the operator immediately; not retrievable later. |
| `expiresAt` | RFC3339Nano UTC. Default TTL 15 minutes. After expiry, the token is reaped from the server-side store. |

Errors:

- `400 Bad Request` — invalid cluster name (fails the DNS-1123 regex)
- `401 Unauthorized` — no session
- `403 Forbidden` — session present but not admin tier (`admin tier required`)

The token is stored in process memory on the server. Single-replica
deployments are supported in v1.0; multi-replica with shared
persistence is a post-1.0 follow-up.

### `POST /api/agents/register`

Agent-side endpoint. Validates the bootstrap token, signs the
agent's CSR, returns the cert + the server's CA bundle. **Not
authenticated** — the bootstrap token IS the proof of authorization.
Mounted unauthenticated specifically because the agent has not yet
obtained its long-lived mTLS identity at this point in the bootstrap
flow — the redeemed token is the only proof of authorization it can
present until the CSR is signed.

```json
POST /api/agents/register
{
  "token":   "abc123...",
  "cluster": "prod-eu",
  "csr":     "<base64-encoded DER form>"
}

→ 200 OK
{
  "cert":      "-----BEGIN CERTIFICATE-----\n...",
  "caBundle":  "-----BEGIN CERTIFICATE-----\n...",
  "expiresAt": "2026-08-02T05:14:00Z"
}
```

| Field | Notes |
|---|---|
| `token` | The opaque token from `POST /api/agents/tokens`. Atomic redeem — succeeds at most once. |
| `cluster` | Must match the cluster name the token was minted for; mismatch **burns the token** and returns 401. |
| `csr` | Base64-encoded DER. The agent generates the keypair locally; only the public key + name claim cross the wire. The CN inside the CSR is informational — the server overwrites it with the cluster name from the token at signing time. |
| `cert` | PEM-encoded signed client cert. CN = cluster name, EKU = clientAuth. Default validity 90 days. |
| `caBundle` | PEM-encoded server CA cert. The agent uses this to validate the server's TLS cert on every reconnect to the tunnel listener. |
| `expiresAt` | When the client cert expires. Operators currently re-register manually; auto-rotation is a post-1.0 follow-up. |

Errors are deliberately uniform:

- `400 Bad Request` — body parse failure, missing required field, malformed CSR
- `401 Unauthorized` — token-related failure with body `"registration rejected"`. The four real failure modes (`unknown` / `expired` / `consumed` / `cluster mismatch`) all collapse to this one response so a probing attacker can't distinguish them. Server-side log carries the actual reason for forensics.
- `500 Internal Server Error` — sign failure (CSR parses but signing errors)

### `WS /api/agents/connect`

WebSocket upgrade endpoint for the long-lived tunnel. **Hosted on a
separate TLS listener** from the rest of the API (default `:8443`,
configurable via `agent.listenAddr` Helm value) because the listener
runs `ClientAuth: RequireAndVerifyClientCert` against the
per-deployment CA — it must NOT be fronted by an HTTP-terminating
load balancer (ALB strips client certs).

The agent presents its mTLS client cert (obtained at
`POST /api/agents/register`); the cert's CN is the cluster name and
becomes the session key in the server's tunnel. Wire format is
`rancher/remotedialer` (Apache-2.0; multiplexes arbitrary TCP over
the WebSocket). Direct API consumers are not expected — this is the
contract between server and `periscope-agent` only.

`tunnel.MTLSAuthorizer` validates:
1. The cert chain (handled by the TLS layer's `ClientAuth`).
2. `ExtKeyUsageClientAuth` is present (defense in depth).
3. `NameAllowed(name)` returns true — i.e. the cluster is in the
   registry as `backend: agent`. Deregistered clusters get rejected
   even with a still-valid cert.


### `/api/clusters/{cluster}/cve/*` — Inspector v2 CVE surface (v1.1+)

Seven endpoints. All reads serve from the per-cluster local store
(populated by the background Inspector v2 scanner, see
[`docs/setup/cluster-rbac.md`](setup/cluster-rbac.md#aws-inspector-v2-optional-v11));
the SPA never hits Inspector directly. Cold first-read blocks on the
~10-30s hydrate; subsequent reads are O(1) map lookups.

**Empty-state contract.** When the operator has `inspector.enabled:
false` in Helm, OR the AWS account doesn't have Inspector v2 enabled
/ the IAM grant is missing, every endpoint returns HTTP **200** with
`{"inspectorEnabled": false, "hydrated": true, ...}`. There is no
error envelope for this state — the SPA reads `inspectorEnabled` and
renders the "Inspector v2 not enabled" hint. Don't script around
transport errors for this case; check the flag.

**Caching.** Read endpoints set:
- `Cache-Control: no-store` — the SPA's TanStack Query layer handles
  client-side cache.
- `ETag: W/"<lastHydrate-nanos>-<digestCount>-<instanceCount>"` —
  weak validator. Clients sending matching `If-None-Match` get **304
  Not Modified**. The ETag changes when a hydrate or eviction
  shifts the store; per-entry delta refreshes (a single digest
  re-fetch from the watch hook) do NOT bump it — chips don't need
  real-time accuracy. Operators who want immediate confirmation of a
  fix should POST `/refresh` and re-read.

#### `GET /api/clusters/{cluster}/cve/status`

Cache state. Does NOT trigger a cold hydrate — the SPA polls this
during the spinner state without forcing 30s of Inspector traffic.

```json
{
  "inspectorEnabled": true,
  "hydrated": true,
  "lastHydrate": "2026-05-11T08:14:32Z",
  "entryCounts": { "digests": 412, "instances": 47 }
}
```

`lastHydrate` is omitted when `hydrated: false`.

#### `GET /api/clusters/{cluster}/cve/by-instance`

Per-instance severity counts joined to the instance's owner
(managed nodegroup / Karpenter NodeClaim / unmanaged) and the
underlying AMI. Ordered by `instanceId`.

```json
{
  "instances": [
    {
      "instanceId": "i-0abc",
      "owner": { "kind": "karpenter-nodeclaim", "name": "default-9f3kz" },
      "ami": "ami-0xyz",
      "severityCounts": { "critical": 2, "high": 5, "medium": 12, "low": 3, "informational": 0 },
      "lastFetchedAt": "2026-05-11T08:14:32Z"
    }
  ],
  "inspectorEnabled": true,
  "hydrated": true
}
```

#### `GET /api/clusters/{cluster}/cve/by-instance/{instanceID}`

Full Inspector findings for one instance. Each `finding` carries the
pre-built `inspectorUrl` deep-link for the AWS console.

```json
{
  "findings": [
    {
      "resourceId": "i-0abc",
      "cve": "CVE-2026-12345",
      "severity": "HIGH",
      "cvssV3Score": 7.5,
      "packageName": "openssl",
      "packageVersion": "1.0.0",
      "fixedVersion": "1.0.1",
      "title": "openssl vulnerability ...",
      "firstObservedAt": "2026-04-01T00:00:00Z",
      "lastObservedAt": "2026-05-10T12:00:00Z",
      "inspectorUrl": "https://us-east-1.console.aws.amazon.com/inspector/v2/home?region=us-east-1#/findings?findingArn=...",
      "description": "Buffer-overflow in libfoo lets a remote attacker crash the process. ...",
      "remediation": "Upgrade openssl to 1.0.1 or later.",
      "remediationUrl": "https://nvd.nist.gov/vuln/detail/CVE-2026-12345",
      "epssScore": 0.87,
      "exploitAvailable": "YES",
      "fixAvailable": "YES"
    }
  ],
  "lastFetchedAt": "2026-05-11T08:14:32Z",
  "inspectorEnabled": true,
  "hydrated": true
}
```

Operator-actionable detail beyond the chip surface:

- `description` — long-form prose from Inspector ("what is this CVE").
- `remediation` / `remediationUrl` — vendor-supplied "how to fix" guidance + link, when Inspector ships one.
- `epssScore` — Exploit Prediction Scoring System probability (0.0-1.0). 0 when Inspector did not report one.
- `exploitAvailable` — `YES` / `NO` / empty (unset).
- `fixAvailable`     — `YES` / `NO` / `PARTIAL` / empty. Operators get the categorical flag alongside the concrete `fixedVersion` string.

These fields are part of the cached Finding so the SPA detail drawer renders inline without a second Inspector round-trip.

Returns **404** if the instance isn't in the cache (typo, terminated,
or not yet hydrated).

#### `GET /api/clusters/{cluster}/cve/by-digest/{digest}`

Full findings for one ECR image digest. The `{digest}` segment is the
bare `sha256:abc...` hash; chi unescapes the colon. Same response
shape as `by-instance/{instanceID}` with the resource ID set to the
digest. Returns **404** when the digest isn't in the cache.

#### `GET /api/clusters/{cluster}/cve/pods?cursor=<b64>`

Per-pod aggregate. Walks the long-lived pod informer's index;
returns 100 pods per page (no override knob — frontend pages further
locally if needed). `next` is `base64(namespace/podname)` of the last
pod on the page; pass it back as `?cursor=...` for the next page.
Returned `next` is empty when the last page is exhausted.

```json
{
  "pods": [
    {
      "namespace": "payments",
      "name": "checkout-7b9-xyz",
      "containers": [
        { "name": "app",     "image": "...dkr.ecr...amazonaws.com/app:v1", "digest": "sha256:abc", "scanState": "scanned", "severityCounts": { "critical": 0, "high": 3, "medium": 7, "low": 1, "informational": 0 } },
        { "name": "sidecar", "image": "docker.io/foo:1.2",                                          "scanState": "non-ecr" }
      ],
      "rolledUpSeverityCounts": { "critical": 0, "high": 3, "medium": 7, "low": 1, "informational": 0 },
      "scanCoverage": "partial"
    }
  ],
  "next": "cGF5bWVudHMvY2hlY2tvdXQtN2I5LXh5eg",
  "inspectorEnabled": true,
  "hydrated": true
}
```

**Container `scanState`**:
- `scanned` — ECR image with a resolved digest; findings looked up.
- `non-ecr` — image isn't in ECR (docker.io, ghcr.io, etc.); Inspector
  v2 doesn't cover it.
- `pending` — ECR image but `containerStatus.imageID` is empty (pod
  mid-pull). A later poll resolves to `scanned`.

**Pod `scanCoverage`**:
- `full` — every container scanned.
- `partial` — at least one scanned, at least one non-ecr/pending.
- `none` — zero scanned.

**Cursor stability.** A pod created between page 1 and page 2 can
shift the lex order; under churn an operator may see a single
skip/duplicate during paging. This is acceptable for v1.1.

#### `GET /api/clusters/{cluster}/cve/pods/{namespace}/{pod}`

Single pod, full per-container findings. Returns the same `PodRow`
shape as a single entry in `/cve/pods`; returns **404** if the
pod isn't in the informer cache, **503** if the informer hasn't
started yet (rare cold-path race).

#### `GET /api/clusters/{cluster}/cve/by-workload/{kind}/{namespace}/{name}`

Owner-aware aggregation: returns every pod owned (directly or
transitively) by the named workload, plus the workload-wide
rolled-up severity and scan coverage. The reason this exists as
a separate endpoint instead of a `?ownerKind=` filter on
`/cve/pods`: in production, operators reason in Deployments /
StatefulSets / DaemonSets — the Pod is ephemeral, the workload
is the stable identity. The SPA detail-pane Security tab calls
this on workload selection.

Supported `kind` values: `Deployment`, `StatefulSet`,
`DaemonSet`, `ReplicaSet`, `Job`. `CronJob` is intentionally
omitted (Pod → Job → CronJob is a three-hop ownerRef walk that
would need a Job informer too; revisit in v1.2 if needed).
Unsupported kinds return **400**.

Ownership resolution:
- Direct: pod ownerRef matches `(kind, name)`. Covers
  StatefulSet, DaemonSet, ReplicaSet, Job.
- Two-hop via ReplicaSet: pod owned by ReplicaSet R; R owned
  by `(Deployment, name)`. Covers the Deployment case, since
  the Deployment controller spawns a ReplicaSet which spawns
  pods.

```json
{
  "workload": { "kind": "Deployment", "namespace": "payments", "name": "checkout" },
  "pods": [
    { /* PodRow shape — same as /cve/pods entries */ },
    ...
  ],
  "rolledUpSeverityCounts": { "critical": 0, "high": 3, "medium": 7, "low": 1, "informational": 0 },
  "scanCoverage": "partial",
  "inspectorEnabled": true,
  "hydrated": true
}
```

**No backend dedup.** A 20-replica Deployment with identical
image digests across replicas returns 20 PodRow entries; the
SPA collapses duplicate digests client-side via `useMemo` so
the detail pane renders one canonical container row per
digest with a "× 20 pods" annotation. (v1.1 design choice —
if it bites at scale we can add `?dedup=true` server-side.)

Same ETag + empty-state contract as the other read endpoints.

#### `POST /api/clusters/{cluster}/cve/refresh`
#### `ContainerRow.packages[]` — server-side package grouping (v1.1, rc2)

`/cve/pods/{ns}/{name}` and `/cve/by-workload/{kind}/{ns}/{name}`
populate `packages[]` on each scanned `ContainerRow`. The
`/cve/pods` paged listing endpoint **omits** the field to keep page
payloads small (chips only need the rolled-up counts).

A typical container with 200+ raw Inspector findings collapses to
~5-20 package groups, because most CVEs cluster in the same
upstream package. Each entry:

```json
{
  "packageName": "go/stdlib",
  "currentVersion": "1.16.1",
  "suggestedFix": "1.26.3",
  "counts": { "critical": 1, "high": 24, "medium": 87, "low": 4, "informational": 0 },
  "exploitCount": 4,
  "fixableCount": 116,
  "findings": [
    { /* sorted Finding[]: exploits desc → severity desc → CVSS desc → EPSS desc → CVE asc */ }
  ]
}
```

- `packageName` is the canonical first non-empty token of Inspector's
  `packageName` (Inspector sometimes emits `"go/stdlib, go/stdlib"`
  for the same package matched twice via CPE; we collapse to one
  group).
- `currentVersion` is the first non-empty `packageVersion` seen in
  the group. Inspector reports the same version on every CVE in a
  group, so first-non-empty is sufficient.
- `suggestedFix` is the **maximum** `fixedVersion` across the
  group — upgrading to it closes every CVE in the group. Empty
  string when no fix is published for any finding.
- `counts` mirrors `SeverityCounts` (already used elsewhere in the
  API).
- `exploitCount` and `fixableCount` are pre-computed so the SPA
  doesn't have to walk `findings` to render the group header.

**Group ordering** is worst-finding-first: severity rank desc, then
exploit count desc, then `severityScore`, then top CVSS, then
package name (stable tiebreaker). The first group an operator sees
is the one they should triage first.

**Why server-side, not SPA-side.** The grouping logic lives in
`internal/cve/findings_group.go` so it serves both the SPA and a
future MCP / AI-agent tool layer (v1.2 epic #151). An LLM calling
the same `/cve/by-workload/...` endpoint receives a pre-grouped,
pre-sorted, pre-prioritized representation — no second
"agent-friendly" shape to maintain, and the LLM gets a tractable
view (5-20 packages) instead of 200 raw rows.


Force-fetch the listed digests/instances from Inspector, bypassing
TTL. Synchronous: returns **200** when the refresh completes.

```json
{
  "digests":     ["sha256:abc", "sha256:def"],
  "instanceIds": ["i-0abc"]
}
```

Both fields optional. An empty body is accepted (logs the operator's
"I checked" intent without forcing a fetch).

Returns **202** with `Next-Poll: 2` (seconds) when the cluster's cold
hydrate is still in flight — the SPA polls `/cve/status` until
`hydrated: true` and resubmits. The 202 response also emits the
audit row.

**Audit.** Each call emits exactly one audit row:

```
verb=cve_refresh outcome=success
extra={ digests: [...], instanceIds: [...] }
```

Reads of the CVE surface do NOT emit audit rows — they are internal
metadata reads. AWS CloudTrail records the underlying Inspector API
calls against the periscope-server's role; that's the auditable
trail for "what did the server fetch."


### `/api/clusters/{cluster}/identity/*` and `/api/clusters/{cluster}/iam/*` — AWS Access surface (v1.1+)

Eight endpoints power the v1.1 AWS Access surface: the **Cluster
Access page** (Access Entries + aws-auth ConfigMap diff + unified
SA → Role index + Pod Identity view), the per-workload **AWS
Access tab**, the **reverse-lookup** page, and the shared
**capabilities probe** + **sensitive-permissions catalog**.

IAM grant for the periscope-server role is documented in
[`docs/setup/cluster-rbac.md`](setup/cluster-rbac.md#aws-access--cluster-access-page-per-workload-tab-reverse-lookup-v11);
operator-facing usage is in
[`docs/usage/aws-access.md`](usage/aws-access.md).

**Not-EKS contract.** Every `/identity/*` and `/iam/*` endpoint
returns HTTP **422** with `{"code":"E_BACKEND_NOT_EKS","message":"…"}`
when the cluster is not EKS-backed. The SPA's Cluster Access page
uses this signal to render a single page-level "not EKS" empty
state instead of repeating the error on each of the four sections.
Don't treat 422 here as a transport error — branch on the code.

**Audit verbs.** The four cluster-identity endpoints
(`access-entries`, `aws-auth-diff`, `sa-roles`, `pod-identity`)
emit `aws_identity_read`. The composed forward-view + reverse-
lookup + capabilities + sensitive-catalog endpoints emit
`aws_iam_read`. The catalog endpoint is cluster-agnostic but
still audited. See `docs/setup/cluster-rbac.md#audit` for the
full verb / `extra.op` table.

#### `GET /api/clusters/{cluster}/identity/access-entries`

Raw `eks:DescribeAccessEntry` rollup — one entry per principal
returned by `ListAccessEntries`, each enriched with its associated
access-policy bindings (`ListAssociatedAccessPolicies`). Returns a
top-level JSON array.

```json
[
  {
    "principalArn": "arn:aws:iam::000000000000:user/alice",
    "type": "STANDARD",
    "kubernetesGroups": ["platform-admins"],
    "accessPolicies": [
      {
        "policyArn": "arn:aws:eks::aws:cluster-access-policy/AmazonEKSAdminPolicy",
        "accessScope": "cluster"
      }
    ],
    "modifiedAt": "2026-04-19T09:14:22Z"
  }
]
```

- `accessPolicies` is omitted when the principal has no policy
  associations (or when `ListAssociatedAccessPolicies` denied for
  that principal — soft-failed; the entry still renders).
- Per-principal describe calls fan out concurrently (server-side
  semaphore caps inflight); one describe-level error fails the
  whole response, while one list-associated-policies error per
  entry soft-fails to `accessPolicies: null`.

**Errors.** 422 not-EKS · 502 / 403 / 429 mapped from AWS SDK
errors with stable codes (`E_AWS_FORBIDDEN` / `E_AWS_NOT_FOUND` /
`E_AWS_THROTTLED`) in the error envelope.

**Audit.** One `aws_identity_read{op:list_access_entries}` row for
the listing call + one `op:describe_access_entry` row per
principal + one `op:list_associated_policies` row per principal.

#### `GET /api/clusters/{cluster}/identity/aws-auth-diff`

Reconciles the legacy `kube-system/aws-auth` ConfigMap with the
modern Access Entries surface. Powers the **migration-health**
chip + entry table on the Cluster Access page. A missing aws-auth
ConfigMap (404 from the K8s API) is the desired
migration-complete signal — the response renders with an empty
`aws-auth` side, NOT a 404.

```json
{
  "entries": [
    {
      "in": "both",
      "principalArn": "arn:aws:iam::000000000000:role/general-eks-node-group-…",
      "kubernetesGroups": ["system:bootstrappers", "system:nodes"]
    },
    {
      "in": "aws-auth",
      "principalArn": "arn:aws:iam::000000000000:user/demo-legacy-admin",
      "kubernetesGroups": ["system:masters"]
    },
    {
      "in": "access-entries",
      "principalArn": "arn:aws:iam::000000000000:user/demo-finops"
    }
  ],
  "health": {
    "awsAuthOnly": 1,
    "dual": 1,
    "accessEntriesOnly": 1
  }
}
```

- `in` is one of `aws-auth` / `access-entries` / `both`. The
  three buckets are mutually exclusive and sum to the distinct
  principal-ARN count across both sources.
- `kubernetesGroups` is the union across both sources when
  `in: both`.
- Principal-ARN comparison is **case-insensitive on the IAM
  user/role segment** (AWS normalizes inconsistently across these
  two surfaces); the response uses the EKS-side casing when both
  sides match. A pure case difference renders as `in: both`, not
  as two separate rows.

**Errors.** 422 not-EKS · 502 K8s API error (other than 404 on
the ConfigMap) · 502 / 403 / 429 AWS SDK errors.

**Audit.** `aws_identity_read{op:read_aws_auth}` for the
ConfigMap read + `op:list_access_entries` + per-principal
`op:describe_access_entry` rows.

#### `GET /api/clusters/{cluster}/identity/sa-roles`

Unified ServiceAccount → IAM Role index. Joins:
- IRSA annotations on every SA in the cluster
  (`eks.amazonaws.com/role-arn`), from a long-lived SA informer.
- Pod Identity associations
  (`eks:ListPodIdentityAssociations`).
- IAM role-existence probe (`iam:GetRole`) so the SPA can render
  a red "role not found" caption for stale annotations and
  orphan PI associations.

Returns a top-level JSON array, one entry per (namespace, SA)
that has at least one binding.

```json
[
  {
    "cluster": "periscope-demo",
    "namespace": "prod",
    "saName": "payments-worker",
    "bindings": [
      {
        "source": "PodIdentity",
        "roleArn": "arn:aws:iam::000000000000:role/periscope-demo-payments-pi-role",
        "roleExists": true,
        "podIdentityAssociationId": "a-y4wb6pficbn57xg32"
      },
      {
        "source": "IRSA",
        "roleArn": "arn:aws:iam::000000000000:role/periscope-demo-payments-irsa-role",
        "roleExists": true,
        "irsaAnnotationValue": "arn:aws:iam::000000000000:role/periscope-demo-payments-irsa-role"
      }
    ],
    "dualSource": true
  },
  {
    "cluster": "periscope-demo",
    "namespace": "staging",
    "saName": "metrics-collector",
    "bindings": [
      {
        "source": "IRSA",
        "roleArn": "arn:aws:iam::000000000000:role/periscope-demo-metrics-collector-role",
        "roleExists": false,
        "irsaAnnotationValue": "arn:aws:iam::000000000000:role/periscope-demo-metrics-collector-role"
      }
    ],
    "dualSource": false
  }
]
```

- `source` is one of `IRSA` / `PodIdentity` / `Both`. A single
  SA with both an annotation and a PI association emits two
  rows (one per binding) AND `dualSource: true` on the parent
  entry — Pod Identity wins at runtime, the IRSA annotation is
  shadowed dead config.
- `roleExists: false` means `iam:GetRole` returned NoSuchEntity.
  When `iam:GetRole` is **denied**, the response sets
  `roleExists: false` as a conservative default with an
  `X-Identity-Stale: true` header indicating partial trust;
  operators should add the permission to disambiguate.
- A 503 with `Retry-After: 3` is returned during cold informer
  start (typically < 3s on a fresh cluster).
- The handler tolerates a partial `Ensure()` failure: if the
  underlying manager returns both a stale snapshot AND an
  error, the stale entries render with the `X-Identity-Stale`
  header instead of a 5xx.

**Errors.** 422 not-EKS · 503 informer warming
(`E_IDENTITY_WARMING`) · 500 setup error
(`E_IDENTITY_SETUP`) · 502 / 403 AWS SDK errors.

**Audit.** Single `aws_identity_read{op:ensure_sa_roles}` row
per call (the inner SDK fan-out emits per-call rows under their
own ops).

#### `GET /api/clusters/{cluster}/identity/pod-identity`

Role-centric pivot of Pod Identity associations: one map entry
per role ARN, all associations of that role underneath. Powers
the "Pod Identity view" section of the Cluster Access page.

```json
{
  "groups": {
    "arn:aws:iam::000000000000:role/periscope-demo-data-team-runner-role": [
      {
        "associationId": "a-6bwbcbafesphxrdct",
        "roleArn": "arn:aws:iam::000000000000:role/periscope-demo-data-team-runner-role",
        "namespace": "team-data",
        "serviceAccount": "data-team-runner",
        "clusterName": "periscope-demo"
      }
    ]
  }
}
```

- Map keys are role ARNs; values are arrays so the SPA can render
  one-role-many-SAs cases (data-team role bound to 3 SAs is one
  map entry with 3 association objects).
- `clusterName` is repeated in every association object so the
  SPA can render a role-pivoted view across multiple clusters
  without re-correlating; under this endpoint it always matches
  the path parameter.

**Errors.** 422 not-EKS · 502 / 403 AWS SDK errors.

**Audit.** One `aws_identity_read{op:list_pod_identity}` row
plus one `op:describe_pod_identity` row per association.

#### `GET /api/clusters/{cluster}/identity/workload-permissions?kind=…&namespace=…&name=…`

Composed forward-view: one round-trip from the SPA returns the
entire per-workload **AWS Access tab**. Resolves the workload's
SA, every IAM role bound to that SA, every inline + managed
policy attached to those roles, expands and groups every
statement by AWS service, classifies sensitive permissions, and
returns the running pods this composition applies to.

Required query params: `kind` (one of `Pod`, `ServiceAccount`,
`Deployment`, `StatefulSet`, `DaemonSet`), `namespace`, `name`.
Other kinds return **400** with `code: E_UNSUPPORTED_KIND`.

```json
{
  "cluster": "periscope-demo",
  "kind": "Pod",
  "namespace": "staging",
  "name": "cron-rotator-75d59c798d-97h92",
  "identityChain": {
    "serviceAccount": "cron-rotator",
    "bindings": [
      {
        "source": "IRSA",
        "roleArn": "arn:aws:iam::000000000000:role/periscope-demo-cron-rotator-role",
        "roleExists": true,
        "irsaAnnotationValue": "arn:aws:iam::000000000000:role/periscope-demo-cron-rotator-role"
      }
    ],
    "dualSource": false
  },
  "groups": [
    {
      "service": "*",
      "sensitive": true,
      "count": 1,
      "permissions": [
        {
          "action": "*",
          "service": "*",
          "resource": "*",
          "effect": "Allow",
          "policyName": "periscope-demo-cron-rotator-role-inline",
          "policySource": "inline",
          "statementSid": "Antipattern1FullAdmin",
          "statementIdx": 2,
          "sensitive": true,
          "sensitiveReason": "wildcard",
          "hasCondition": false,
          "wildcard": true
        }
      ]
    }
  ],
  "rawStatements": [],
  "warnings": [
    { "code": "DUAL_SOURCE_IRSA_SHADOWED", "message": "…", "roleArn": "…" }
  ],
  "affectedPods": [
    { "namespace": "staging", "name": "cron-rotator-75d59c798d-97h92", "nodeName": "ip-10-0-28-199.ec2.internal" }
  ],
  "affectedPodCount": 1,
  "policyFetchPartial": false,
  "truncated": false,
  "totalCount": 13,
  "catalogVersion": "1.0.0",
  "fetchedAt": "2026-05-16T13:36:42.118Z"
}
```

- `groups[]` is **pre-sorted** server-side: sensitive-first, then
  alphabetical by `service`. The SPA does no re-bucketing.
- `groups[].permissions[]` is **pre-sorted**: sensitive-first,
  then by `wildcard` (true first), then by `action`.
- `service: "*"` is the wildcard-action bucket (statements with
  `Action: "*"`); it always sorts first because every wildcard
  is sensitive.
- `sensitiveReason` is one of `privilege-escalation` /
  `data` / `cross-account` / `destructive` / `cluster` /
  `wildcard` — same categories as the sensitive-catalog
  endpoint. Empty string for non-sensitive permissions.
- `policySource` is one of `inline` / `managed` / `aws-managed`.
- `rawStatements[]` is non-empty when a policy contains
  `NotAction` / `NotResource` / `NotPrincipal` — the engine
  cannot expand these to (action, resource) tuples cleanly, so
  it surfaces the raw statement separately for SPA "review by
  hand" rendering.
- `warnings[].code` is one of `DUAL_SOURCE_IRSA_SHADOWED` /
  `ROLE_NOT_FOUND` / `POLICY_FETCH_PARTIAL` / `NO_BINDINGS`.
- `affectedPods[]` is truncated to 5 entries by default;
  `affectedPodCount` is the untruncated total.
- `truncated: true` + `totalCount` signal a soft cap on
  permission rows per role (default 10000); the SPA renders a
  "showing N of M" banner.
- `catalogVersion` is the embedded sensitive-permissions catalog
  version (`internal/awseks/iam/sensitive.yaml`'s `version:`
  field) — included on every response so operators can trace
  "why is this flagged?" to a specific catalog version.

**Errors.** 422 not-EKS · 400 unsupported kind · 404 workload
not found · 502 / 403 / 429 AWS SDK errors.

**Audit.** One `aws_iam_read{op:workload_permissions}` row per
call. Inner SDK calls (`iam:GetRole`, `iam:GetRolePolicy`,
`iam:GetPolicy`, `iam:GetPolicyVersion`) emit one
`aws_iam_read{op:get_role_policy|...}` row each — chatty by
design so a forensic reviewer can attribute every SDK call.

#### `GET /api/clusters/{cluster}/iam/reverse-lookup?action=…&resource=…`

Answers "which workloads can perform action X on resource Y?"
across every SA-bound IAM role in the cluster. Powers the
top-level **Reverse lookup** page and the one-click chip-pre-fill
on the AWS Access tab.

Required query: `action` (case-insensitive; e.g. `s3:DeleteBucket`,
`iam:PassRole`, or `*` for any wildcard match). Optional:
`resource` (defaults to `*` — match any resource ARN the
statement grants).

```json
{
  "action": "s3:DeleteBucket",
  "scope": {},
  "rows": [
    {
      "pod": { "namespace": "staging", "name": "cron-rotator-…", "nodeName": "ip-10-0-28-199.ec2.internal" },
      "saName": "cron-rotator",
      "namespace": "staging",
      "roleArn": "arn:aws:iam::000000000000:role/periscope-demo-cron-rotator-role",
      "permission": {
        "action": "s3:*",
        "service": "s3",
        "resource": "*",
        "effect": "Allow",
        "statementSid": "Antipattern1FullAdmin",
        "wildcard": true,
        "sensitive": true,
        "sensitiveReason": "destructive"
      },
      "source": "IRSA"
    }
  ],
  "truncated": false,
  "totalPods": 4
}
```

- One row per **matched pod**, not per SA. A 20-replica
  Deployment with one role grant emits 20 rows. A dual-source
  SA (IRSA + Pod Identity both grant the action) emits TWO rows
  per pod — one per binding — so the SPA renders the honest
  dual-source story.
- `pod.name` / `pod.nodeName` / `saName` may be null if pod
  enrichment is unavailable for that match (no live pod with
  the binding — known v1.1.x follow-up: row enrichment from
  the SA informer). The match itself is still surfaced.
- `permission.wildcard: true` means the underlying policy
  statement uses a wildcard action / resource that covers the
  query — operators see those rows as red chips on the SPA.
- `scope` is reserved for future per-cluster / per-namespace
  filtering; v1.1 returns `{}` (whole cluster).
- `truncated: true` + `totalPods` signal a server-side cap on
  rows returned (10000 default).

**Wire-shape note.** The v1.0 `matches[]` field (one entry per
SA, with embedded `podRefs[]`) was renamed to `rows[]` (one
entry per matched pod) in v1.1. See the CHANGELOG `Changed`
section.

**Errors.** 422 not-EKS · 400 missing/invalid `action` · 502 /
403 / 429 AWS SDK errors.

**Audit.** One `aws_iam_read{op:reverse_lookup}` row per call.

#### `GET /api/clusters/{cluster}/identity/capabilities`

Per-feature availability probe for the AWS Access surfaces.
Powers the **locked-feature pane** on every AWS Access tab and
the reverse-lookup page — instead of a 403 on first use, the SPA
renders a structured "you can't use this because X, here's
exactly what's missing" panel.

```json
{
  "cluster": "periscope-demo",
  "features": {
    "awsAccessTab": { "available": true, "docsUrl": "/docs/usage/aws-access" },
    "reverseLookup": { "available": true, "docsUrl": "/docs/usage/aws-access" },
    "sensitiveCatalog": { "available": true }
  },
  "fetchedAt": "2026-05-16T13:31:36.805Z"
}
```

When a feature is **locked**, the entry carries a stable reason
code, the exact missing IAM actions, and a docs URL:

```json
{
  "awsAccessTab": {
    "available": false,
    "reason": "MISSING_IAM_PERMS",
    "message": "Periscope's IAM role is missing 2 permission(s) required for the AWS Access tab.",
    "missing": ["iam:GetPolicy", "iam:GetPolicyVersion"],
    "docsUrl": "/docs/setup/cluster-rbac"
  }
}
```

- `reason` is one of:
  `NOT_EKS` · `RBAC_DENIED` · `MISSING_IAM_PERMS` ·
  `NO_IDENTITY_CONFIGURED` · `INFORMER_WARMING` ·
  `IAM_PROBE_DISABLED`. MCP tools should branch on the code,
  not parse `message`.
- `missing[]` is populated when the server's
  `iam:SimulatePrincipalPolicy` probe (default enabled, off via
  `PERISCOPE_AWS_ACCESS_IAM_PROBE=false`) can prove which
  actions are denied. When the probe itself is denied, the
  endpoint falls back to optimistic `available: true` with a
  top-level `note` explaining the limitation.
- Response is cached server-side for 5 minutes per (cluster,
  actor). The SPA's **Re-check** button on the locked pane
  sends `Cache-Control: no-cache` to bypass.

**Errors.** 422 not-EKS only when the cluster is non-EKS; the
endpoint deliberately swallows IAM / RBAC errors into the
`features[].reason` field instead of erroring out.

**Audit.** One `aws_iam_read{op:capabilities}` row per fresh
probe; `op:capabilities:cache_hit` when served from cache.

#### `GET /api/identity/sensitive-catalog`

Cluster-agnostic catalog of sensitive IAM actions Periscope
recognizes. Used by the SPA for chip metadata and by MCP /
agent tools for "what does this chip mean?" lookups without a
TypeScript side table.

```json
{
  "version": "1.0.0",
  "entries": [
    {
      "action": "iam:PassRole",
      "category": "privilege-escalation",
      "pattern": false,
      "reverseQuery": { "action": "iam:PassRole" }
    },
    {
      "action": "s3:Delete*",
      "category": "destructive",
      "pattern": true,
      "reverseQuery": { "action": "s3:DeleteBucket" }
    },
    {
      "action": "*",
      "category": "wildcard",
      "pattern": true,
      "reverseQuery": { "action": "*" }
    }
  ]
}
```

- 17 named actions in v1.1 plus the literal `*` wildcard
  (handled in classification code, not the YAML), for an
  effective 18-chip catalog.
- `category` is one of `privilege-escalation` / `data` /
  `cross-account` / `destructive` / `cluster` / `wildcard`.
- `pattern: true` means the `action` field is a glob (e.g.
  `s3:Delete*`); the classifier matches statements against the
  glob, not just exact equality.
- `reverseQuery` is the pre-canned `(action, resource)` pair
  the SPA pre-fills into the reverse-lookup form when a chip
  is clicked. For glob patterns the canonical action is a
  representative (`s3:DeleteBucket` for `s3:Delete*`).
- `version` matches the `catalogVersion` field on every
  workload-permissions response — bumped on catalog edits so
  consumers can diff what changed across releases.

**Audit.** One `aws_iam_read{op:sensitive_catalog}` row per
call. Cluster-agnostic (the path has no `{cluster}` param) so
the audit row has an empty cluster field.


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

The pattern handles **create and update transparently**:
Server-Side Apply against a name that does not yet exist
creates the resource; against an existing name it updates per
field-manager semantics. The SPA's "Apply YAML" flow reuses
this endpoint for both cases — there is no separate `/create`
route. `dryRun=true` still emits an audit row with
`extra.dryRun=true` so the action is visible in the trail
while remaining filterable from real applies.

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
fan-out layer to land first. Targeted for the v1.x train.

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

### Cluster shell (WebSocket)

```
GET /api/clusters/{c}/shell?mode=bash
   ↑ HTTP 101 Upgrade → WebSocket
```

Issue [#104](https://github.com/gnana997/periscope/issues/104). Wire
protocol is **byte-identical to pod exec** (same hello / stdin /
stdout / closed / error / idle_warn frame shape) — the SPA shares
the `ExecClient` instance across both flavors. The only differences
are at the handler boundary:

- Periscope main provisions a per-session ephemeral pod in
  `clusterShell.namespace` (default `periscope-system`) on the
  target cluster. The pod's image is a debian-slim runtime carrying
  `bash` + `kubectl` + `helm`. The handler attaches to that pod
  via the same `k8s.ExecPod` plumbing pod-exec uses.
- Identity is per-user via impersonation: the kubeconfig delivered
  to the pod has `as: <operator-sub>` + `as-groups:
  [periscope-tier:<tier>]` + audit-extras (`session-id` + `actor`)
  baked into a tier-narrow ServiceAccount's bearer token.
- The handler returns `403 E_CLUSTER_SHELL_DISABLED` when the
  server-wide toggle is off, `403 E_FORBIDDEN` when the operator's
  tier isn't on the `clusterShell.tiers` allow-list,
  `400 E_NOT_IMPLEMENTED` for `?mode=kubectl-only` (REPL ships in
  a later release), `429 E_CAP_USER` / `429 E_CAP_CLUSTER` on cap
  exhaustion (body carries `activeSessions`), and
  `500 E_SHELL_POD_TIMEOUT` when the pod doesn't reach `Ready`
  within `clusterShell.podStartTimeoutSeconds` (default 30s).
- Two audit emissions per session: `cluster_shell_open` immediately
  after cap checks pass and before the WebSocket upgrade, and
  `cluster_shell_close` after the session ends. The close envelope
  carries `duration_ms`, `exit_code`, `bytes_in`, `bytes_out`,
  `close_reason`, and `commands: [{timestamp, argv, pid}]` read
  from the in-pod audit file via a final `exec cat` during
  teardown. Both rows carry the same `session_id` for cross-log
  joins against apiserver audit (which sees the same id in the
  `audit.periscope.io/session-id` user-extra).

Operator guide: [`docs/setup/cluster-shell.md`](setup/cluster-shell.md).

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
