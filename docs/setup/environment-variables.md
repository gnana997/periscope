# Environment variables

Centralized reference for every environment variable the Periscope
binary reads. The Helm chart renders most of these from
`values.yaml`; this page is the source of truth when something
doesn't match what you expect.

For Helm-side configuration that **doesn't** map to an env var
(volumes, RBAC, ingress, image pull policy, the cluster registry
shape itself), see [`docs/setup/deploy.md`](deploy.md).

## At a glance

| Variable | Default | Purpose | Helm value |
|---|---|---|---|
| `PORT` | `8080` | HTTP listen port | `service.port` |
| `PERISCOPE_AUTH_MODE` | _(inferred from file)_ | `oidc` or `dev` | _(none — set via auth file)_ |
| `PERISCOPE_AUTH_FILE` | _(unset = dev mode)_ | Path to auth config YAML | _(fixed: `/etc/periscope/auth.yaml`)_ |
| `PERISCOPE_CLUSTERS_FILE` | _(unset = empty registry)_ | Path to cluster registry YAML | _(fixed: `/etc/periscope/clusters.yaml`)_ |
| `PERISCOPE_AUDIT_ENABLED` | `false` | Open the SQLite audit sink at startup | `audit.enabled` |
| `PERISCOPE_AUDIT_DB_PATH` | `/var/lib/periscope/audit/audit.db` | SQLite file path | _(fixed by chart)_ |
| `PERISCOPE_AUDIT_RETENTION_DAYS` | `30` | Age cap (`0` disables) | `audit.retentionDays` |
| `PERISCOPE_AUDIT_MAX_SIZE_MB` | `1024` | Size cap (`0` disables) | `audit.maxSizeMB` |
| `PERISCOPE_AUDIT_VACUUM_INTERVAL` | `24h` | Prune+VACUUM loop period | `audit.vacuumInterval` |
| `PERISCOPE_EXEC_IDLE_SECONDS` | `600` | Server-side exec idle timeout | `exec.serverIdleSeconds` |
| `PERISCOPE_EXEC_IDLE_WARN_SECONDS` | `30` | Warn-before-tear-down lead | `exec.idleWarnSeconds` |
| `PERISCOPE_EXEC_HEARTBEAT_SECONDS` | `20` | Exec WebSocket ping period | `exec.heartbeatSeconds` |
| `PERISCOPE_EXEC_MAX_SESSIONS_PER_USER` | `5` | Concurrent exec sessions per user | `exec.maxSessionsPerUser` |
| `PERISCOPE_EXEC_MAX_SESSIONS_TOTAL` | `50` | Concurrent exec sessions across all users | `exec.maxSessionsTotal` |
| `PERISCOPE_WATCH_STREAMS` | `all` | Which kinds get an SSE watch route | `watchStreams.kinds` |
| `PERISCOPE_WATCH_PER_USER_LIMIT` | `60` | Concurrent watch streams per user | `watchStreams.perUserLimit` |
| `PERISCOPE_PROBE_CLUSTERS_ON_BOOT` | _(unset)_ | `1` to seed exec circuit breakers at boot | `exec.probeClustersOnBoot` |
| `PERISCOPE_DEV_ALLOW_ORIGINS` | _(unset)_ | Extra WebSocket origins (dev only) | _(no Helm value — see 6)_ |

Empty / unset values fall back to the documented default. Negative or
non-numeric values where a positive integer is expected fall back to
the default and emit a `slog.Warn` line at startup.

---

## 1. Server / runtime

### `PORT`

The HTTP listening port. Default `8080`. Set by the Helm chart from
`service.port` so the container port and the K8s `Service` stay in
sync.

This is the only non-`PERISCOPE_*` env var the binary reads; the
`PORT` name is conventional for Twelve-Factor / containerized apps.

---

## 2. Authentication

See [`docs/setup/auth0.md`](auth0.md) / [`docs/setup/okta.md`](okta.md)
for the IdP wiring; [RFC 0002](../rfcs/0002-auth.md) for the design
contract.

### `PERISCOPE_AUTH_FILE`

Path to the auth YAML file. The Helm chart always renders the file
at `/etc/periscope/auth.yaml` and sets this env var to that path —
operators don't normally touch it. Only override when running
locally with a hand-rolled file:

```sh
PERISCOPE_AUTH_FILE=$(pwd)/examples/config/auth.yaml.okta make backend
```

When unset and `PERISCOPE_AUTH_MODE` is empty / `dev`, the binary
runs in dev mode with the built-in `dev@local` actor — useful for
SPA development and tests, **never for production**.

### `PERISCOPE_AUTH_MODE`

Optional. Forces the auth mode regardless of what the auth file
implies. Two values:

- `dev` — runs every request as the `Default()` dev actor
  (`subject: dev@local`, `groups: [dev]`). The login screen is
  skipped.
- `oidc` — requires `PERISCOPE_AUTH_FILE` to point at a valid file
  with a populated `oidc:` block; otherwise the binary fails to
  start with a clear error.

Resolution when both are unset: if the file has an `oidc.issuer`,
the mode is `oidc`; otherwise `dev`.

---

## 3. Cluster registry

### `PERISCOPE_CLUSTERS_FILE`

Path to the cluster registry YAML. The Helm chart always renders
the file at `/etc/periscope/clusters.yaml` and sets this env var to
that path. When unset, the binary boots with an empty registry and
logs a warning — the SPA renders the empty-state fleet view.

The file shape is documented in
[`examples/config/clusters.yaml`](../../examples/config/clusters.yaml)
and [`docs/setup/deploy.md`](deploy.md).

---

## 4. Audit log

Full operator guide: [`docs/setup/audit.md`](audit.md). Formal
contract: [RFC 0003](../rfcs/0003-audit-log.md).

### `PERISCOPE_AUDIT_ENABLED`

`true` opens the SQLite audit sink at startup. Anything else
(empty, `false`, `1`, `yes`) leaves audit at stdout-only. The
exact-string match is intentional: a malformed value defaults to
off rather than silently enabling persistence.

### `PERISCOPE_AUDIT_DB_PATH`

Path to the SQLite DB file. Default
`/var/lib/periscope/audit/audit.db`. The directory is created at
startup if missing. The Helm chart pins this path and mounts the
PVC (or `emptyDir`) at the parent directory.

### `PERISCOPE_AUDIT_RETENTION_DAYS`

Age-based prune cap. Default `30`. Set to `0` to disable
time-based pruning (the size cap still applies). Setting **both**
`RETENTION_DAYS` and `MAX_SIZE_MB` to `0` is the unbounded-growth
footgun; the startup validator emits a `slog.Warn` line.

### `PERISCOPE_AUDIT_MAX_SIZE_MB`

Size-based prune cap. Default `1024`. Set to `0` to disable. The
prune loop estimates `(size − target) / size × rowCount × 1.1` rows
to drop, removes them in one DELETE, then VACUUMs.

### `PERISCOPE_AUDIT_VACUUM_INTERVAL`

How often the prune+VACUUM loop runs. Go duration syntax (`24h`,
`6h`, `30m`). Default `24h`. Below `1m` the validator warns about
hammering disk.

The retention and prune algorithms are specified in
[RFC 0003 10](../rfcs/0003-audit-log.md).

---

## 5. Pod exec

Full operator guide: [`docs/setup/pod-exec.md`](pod-exec.md). Design:
[RFC 0001](../rfcs/0001-pod-exec.md).

These five variables set the **global defaults** applied to every
cluster's sessions. Per-cluster overrides live in
`clusters[i].exec.*` (see `examples/config/clusters.yaml`) and
merge on top of these globals — the cluster value wins when set
to a positive number.

### `PERISCOPE_EXEC_IDLE_SECONDS`

Server-side idle timeout. Default `600` (10 minutes). After this
many seconds with no I/O, the server tears the session down. The
SPA shows a warning `IDLE_WARN_SECONDS` before the tear-down so
the user can keep typing to reset the timer.

### `PERISCOPE_EXEC_IDLE_WARN_SECONDS`

Lead time for the idle warning banner. Default `30`. Must be
smaller than `IDLE_SECONDS` to do anything useful.

### `PERISCOPE_EXEC_HEARTBEAT_SECONDS`

WebSocket ping period. Default `20`. Lets the server detect
half-open connections and keeps intermediaries (proxies, load
balancers) from tearing the connection down for inactivity.

### `PERISCOPE_EXEC_MAX_SESSIONS_PER_USER`

Per-user concurrent exec session cap. Default `5`. The 6th open
attempt returns `HTTP 429 E_CAP_USER` with a JSON body listing the
user's active sessions for the SPA to surface as "you have 5
shells open; close one before opening another."

### `PERISCOPE_EXEC_MAX_SESSIONS_TOTAL`

Process-wide concurrent exec session cap. Default `50`.
Cluster-aware (see RFC 0001 PR4 — the per-cluster split is
`E_CAP_CLUSTER`).

---

## 6. Watch streams (SSE)

Full operator guide: [`docs/setup/watch-streams.md`](watch-streams.md).
Design: [`docs/architecture/watch-streams.md`](../architecture/watch-streams.md).

### `PERISCOPE_WATCH_STREAMS`

Selects which resource kinds get an SSE watch route registered.
Grammar:

```
""           # unset = "all"
"all"        # every registered kind (~22)
"off"        # no SSE; SPA falls back to polling everywhere
"none"       # synonym for "off"
"pods"       # one kind
"workloads"  # group alias (deployments, statefulsets, daemonsets, replicasets, jobs, cronjobs, hpas, pdbs)
"core,workloads,networking"   # multiple groups
"pods,workloads"              # mixed kinds and groups
```

Group aliases: `core`, `workloads`, `networking`, `storage`,
`cluster`. The kind ↔ group mapping is the `watchKinds` registry
in `cmd/periscope/main.go`. Unknown tokens are silently dropped;
operators see a startup `slog` line summarizing what's enabled, so
typos surface immediately.

Default is "all on" because the SSE plumbing has a per-user stream
cap and a tested polling-fallback path — restrictive proxies that
mishandle long-lived connections degrade gracefully, but if you
know yours doesn't, set `off` to skip the upgrade dance.

### `PERISCOPE_WATCH_PER_USER_LIMIT`

Caps concurrent watch streams per OIDC subject across all clusters
and kinds. Default `60` (≈ 10 tabs × 6 list views). Exceeding the
cap returns `HTTP 429`; the SPA falls back to polling for that
view.

The cap protects the apiserver's watch quota from a runaway
client. As more kinds gain SSE streams, this grows linearly with
realistic usage; bump if `/debug/streams` shows users hitting the
ceiling.

---

## 7. Development and debug

These exist for development convenience and are **not** part of the
v1.0 public configuration surface — treat them as breakable.

### `PERISCOPE_PROBE_CLUSTERS_ON_BOOT`

Set to `1` to fire a one-shot `/bin/true` exec against every
registered cluster at boot, seeding the per-cluster transport
circuit breaker before the first user click. Useful in environments
where the EKS apiserver's WebSocket-vs-SPDY behavior is known
ahead of time. Off by default; the breaker self-recovers from a
cold start within the first user session anyway.

Helm rendering: `exec.probeClustersOnBoot: true` emits the env var
with value `"1"`.

### `PERISCOPE_DEV_ALLOW_ORIGINS`

Comma-separated list of WebSocket Origin headers to allow on the
exec endpoint, beyond the same-origin default. Used for local
dev (Vite proxy on `:5173` → backend on `:8088`) and embedded
scenarios. v1.0 default is **same-origin only**; set this only
when the backend's bind origin and the SPA's serve origin
genuinely differ and you accept the implications.

```sh
PERISCOPE_DEV_ALLOW_ORIGINS=localhost:5173,127.0.0.1:5173 make backend
```

There is **no Helm value** for this — production ingress should
serve the SPA and the backend from the same origin, making the
allowlist unnecessary.

---

## 8. Secret references inside config files

The auth YAML and clusters YAML support env-var interpolation for
sensitive fields (OIDC client secret, optionally a kubeconfig
path). Syntax:

```yaml
oidc:
  clientSecret: ${OIDC_CLIENT_SECRET}     # env-var lookup
  # or:
  clientSecret: file:///run/secrets/oidc  # file read
  # or:
  clientSecret: aws-secretsmanager://...  # AWS Secrets Manager
  clientSecret: aws-ssm://...              # AWS SSM Parameter Store
```

For `${VAR}` references, the resolver calls `os.LookupEnv(VAR)` at
startup. The most common name is `OIDC_CLIENT_SECRET`, which the
Helm chart wires up automatically when `secrets.mode != native` —
the chart mounts a K8s Secret as that env var so the auth YAML's
`${OIDC_CLIENT_SECRET}` resolves cleanly.

Secret-resolver implementation:
[`internal/secrets/resolver.go`](../../internal/secrets/resolver.go).

---

## 9. Helm chart vs raw env vars

The Helm chart renders most env vars from `values.yaml`. Two
exceptions worth knowing:

- **Always-set, fixed**: `PERISCOPE_AUTH_FILE`, `PERISCOPE_CLUSTERS_FILE`,
  and `PERISCOPE_AUDIT_DB_PATH` are pinned to specific paths the
  chart mounts files at. Don't try to override these from
  `values.yaml.env`; the chart's own env list wins.
- **Conditionally rendered**: `PERISCOPE_WATCH_STREAMS` and
  `PERISCOPE_WATCH_PER_USER_LIMIT` are only emitted when their
  values.yaml fields are non-empty / non-zero. The server defaults
  to "all on" / `60` when unset, so leaving them out of `values.yaml`
  is the right choice for most deployments.
- **Escape hatch**: `values.yaml.env` is appended after the
  rendered env list, so an operator can add an arbitrary
  `name: value` pair (e.g. for a future `PERISCOPE_*` var that
  doesn't have a values.yaml field yet) without forking the chart.

---

## 10. Compatibility and forward evolution

Env var names that appear in 1–6 are **part of the v1.0 public
configuration surface**. They are covered by the same semver
promise as the HTTP API:

- Renaming a variable is a breaking change → next major.
- Removing a variable is a breaking change → next major.
- Changing the default value of a variable is **not** a breaking
  change provided the new default is "more conservative" (smaller
  cap, shorter retention, etc.) and is documented in the
  `CHANGELOG.md` under the corresponding release.
- Adding a new variable is additive (minor / patch).
- Tightening parsing (rejecting a previously-tolerated value) is
  breaking.

Variables in 7 are explicitly **not covered** — they're for
development workflows and may change shape between releases.

---

## 11. Forward roadmap

Likely additions in v1.x:

- `PERISCOPE_SESSION_STORE=redis` plus `PERISCOPE_SESSION_DSN=...`
  when the multi-replica session store lands.
- `PERISCOPE_LOG_LEVEL` for runtime log-level tuning. Today the
  binary uses `slog.Default()` at info; level overrides require a
  rebuild.

These will be additive — operators who don't set them get the
SQLite / in-memory / info-level defaults that v1.0 ships with.
