# RFC 0003 — Audit Log: Schema and Retention Semantics

| | |
|---|---|
| **Status** | Accepted (shipped in v1.0) |
| **Owner** | @gnana997 |
| **Started** | 2026-05-04 |
| **Targets** | v1.0 (formalize what shipped), v2 (signing / external SIEM contract) |
| **Related** | RFC 0001 (pod exec — section 10 — audit-event shape), RFC 0002 (auth — actor identity) |

---

## 1. Summary

Periscope writes one structured `audit.Event` for every privileged action a
human takes through the dashboard: pod exec, secret reveal, resource apply,
resource delete, cronjob trigger. Events flow through an in-process
`audit.Emitter` that fans out to one or more `audit.Sink`s. v1.0 ships two
sinks — `StdoutSink` (always on) and `SQLiteSink` (opt-in, persisted) — and
one `Reader` (`SQLiteSink` again, behind `GET /api/audit`).

This RFC pins the contract: the closed verb set, the field shape, the
on-disk schema, the retention semantics, the read-side RBAC, and the
guarantees the pipeline does and does not make. Operators reading audit
rows downstream (SIEM ingest, compliance review, forensics) and contributors
adding new privileged actions should both treat this document as the
source of truth.

The key claim is **schema stability** (the verb set and field names are
covered by semver) paired with an **explicit non-claim around tamper
resistance** (v1.0 is operator-trust, not cryptographic). v1.x narrows the
non-claim further; v2 introduces signing for the operator-tampering case.

---

## 2. Motivation

Periscope's positioning is "the dashboard whose audit log names a real
human." Two forces flow from that:

- **Every privileged action must be captured exactly once, with a stable
  shape.** Pre-refactor the same logical event was emitted as ad-hoc
  `slog` calls at each handler with three different field shapes and no
  row at all on the failure path. A SIEM query that worked for
  `apply` did not work for `delete`. Pinning a single schema in
  `internal/audit/event.go` and routing every emission through one
  `Emitter` lets downstream consumers index on stable column names.

- **The audit pipeline cannot become a SPOF for the dashboard.** A stuck
  PVC, a slow disk, or a transient SQLite error must never block a
  privileged request. The pipeline is therefore **fail-open**: errors
  log and the action proceeds, with `StdoutSink` always attached so
  nothing is lost from the operator-visible log stream.

The combination of "exact shape, recorded reliably" and "never blocks the
request" is what RFC 0001 10 implicitly relied on. RFC 0003 elevates
that contract to a first-class spec so v1.x and v2 evolutions are
confined edits rather than rewrites.

---

## 3. Goals and non-goals

### Goals (v1.0)

- A closed, semver-covered taxonomy of audit verbs.
- A wire-stable JSON shape on both stdout and `/api/audit`.
- A SQLite-backed persistent store with bounded retention and a
  documented schema-migration story.
- Read-side RBAC (`X-Audit-Scope`) that lets non-admin users self-audit
  without exposing colleagues' actions.
- Fail-open behavior at every step (sink open failure, sink write
  failure, retention loop failure).

### Non-goals (v1.0)

- **Cryptographic tamper resistance.** Rows are insert-only at the
  application layer; an operator with disk access can edit `audit.db`.
  v1.0 trusts the operator. Hash-chain signing is in scope for v2.
- **Compliance-grade retention (HIPAA, PCI, SOC2 multi-year).** SQLite
  is a local cache; the `Validate` warning at startup says so out loud
  for `retentionDays > 365`. Operators with a multi-year requirement
  ship `StdoutSink` JSON to an external SIEM and treat that as the
  system of record.
- **HA (multi-replica) audit.** SQLite is single-writer. v1.0 deploys
  Periscope as one replica when audit persistence is on.
- **Free-text or `LIKE` search.** All filters are exact-match or
  time-range on indexed columns; full-text search would degrade past
  the retention cap.

### Out of scope forever

- Tracking every read. List/describe/log-view are not audited; only
  privileged or identity-sensitive actions are. (`log_open` is reserved
  in the taxonomy but not yet wired — see 4.)
- Storing apply payloads. Diffs of YAML being applied are not part of
  the audit record. The audit row tells you "alice@corp applied
  Deployment foo at T," not what changed in the manifest.

---

## 4. Verb taxonomy

The verb set is **closed**. New verbs land via PR that edits
`internal/audit/event.go` and (almost always) this RFC. Free-string verbs
are not allowed — every emission site uses a `audit.Verb*` constant.

| Verb | Emitted by | Outcome classes | `Extra` fields |
|---|---|---|---|
| `apply` | `applyResourceHandler` (PUT/POST `/api/clusters/{c}/customresources/.../apply` and the typed apply route) | `success` / `failure` / `denied` | `dryRun: bool`, `force: bool` |
| `delete` | `deleteResourceHandler` | `success` / `failure` / `denied` | — |
| `trigger` | `triggerCronJobHandler` | `success` / `failure` / `denied` | on success: `jobName: string` |
| `secret_reveal` | `secretRevealHandler` | `success` / `failure` / `denied` | `key: string`; on success also `size: int` (bytes of the decoded value) |
| `exec_open` | `execHandler` immediately after session admit | `success` only | `session_id`, `container`, `tty`, `command`, `k8s_identity`, `started_at` |
| `exec_close` | `execHandler` once `execsess.Run` returns | `success` / `failure` | all `exec_open` fields plus `transport`, `ended_at`, `duration_ms`, `exit_code`, `bytes_stdin`, `bytes_stdout`, `err`. `Reason` carries the close disposition: `completed` / `idle_timeout` / `abort` / `server_error` |
| `log_open` | _reserved_ | _reserved_ | _reserved_ |

`apply` is intentionally a single verb covering both create and update.
Periscope's mutation surface is `PATCH application/apply-patch+yaml`
(Server-Side Apply); the Kubernetes API does not split the two and we do
not synthesize the distinction. The forensic question "did this row
create or modify the resource?" is answerable by joining audit rows: the
first successful `apply` for a given `(cluster, namespace, group,
version, resource, name)` tuple is the create; everything after is an
update.

`log_open` is declared but not yet emitted. Stream opens on
`/api/clusters/{c}/pods/{ns}/{name}/logs` are out of scope for v1.0
because the access pattern (long-lived SSE) needs ratelimit-aware
emission to avoid flooding the audit table. Adding it is a self-contained
follow-up; the constant exists so the taxonomy is visible.

---

## 5. Outcome taxonomy

`Outcome` is a closed three-value enum:

| Value | When |
|---|---|
| `success` | The action completed. For `exec_close`, "completed" means the apiserver stream returned without `runErr`. |
| `denied` | The action was rejected by Kubernetes RBAC — `k8serrors.IsForbidden` or `k8serrors.IsUnauthorized`. Denials are forensically the most interesting class and get their own outcome so an operator can query "who tried X and got blocked" with one filter. |
| `failure` | Any other error: validation, conflict, server error, network, dry-run rejection. |

`outcomeFor` in `cmd/periscope/errors.go:48` is the single mapping from
`error` to `Outcome`. Adding a new error class that should classify as
`denied` rather than `failure` happens in that one place.

Cancellations propagate as `context.Canceled` and **do not produce an
audit row** — the action neither completed nor was rejected by policy;
it was abandoned by the client. Handlers explicitly `return` early in
that case.

---

## 6. Event schema (wire shape)

The Go struct is `audit.Event` in `internal/audit/event.go`. Both sinks
and the `/api/audit` reader produce the same field set; only the
serialization differs (slog kv vs SQLite columns vs JSON).

```jsonc
{
  "id":          12345,                             // /api/audit only — DB row id
  "timestamp":   "2026-05-04T12:34:56.789Z",        // RFC3339Nano UTC; Emitter stamps if zero
  "requestId":   "abc123",                          // chi request id; ties to access logs
  "actor": {
    "sub":    "auth0|123",                          // OIDC subject — required, non-empty
    "email":  "alice@corp.example",                 // optional
    "groups": ["periscope-users", "Sec-Team"]       // optional, IdP claim
  },
  "verb":        "apply",                           // closed set, see 4
  "outcome":     "success",                         // closed set, see 5
  "cluster":     "prod-eu",                         // registry name, optional for some events
  "resource": {
    "group":     "apps",                            // omitted for core
    "version":   "v1",
    "resource":  "deployments",                     // plural lowercase
    "namespace": "platform",                        // empty for cluster-scoped
    "name":      "checkout"                         // empty for batch operations
  },
  "reason":      "deployments.apps \"checkout\" already exists", // err.Error() on failure/denied; close-reason on exec_close; empty on plain success
  "extra": {                                        // verb-specific; see 4
    "dryRun": false,
    "force":  true
  }
}
```

**Stability rules:**

- **Verb names** (`apply`, `delete`, …) are part of the public API.
  Renames are breaking changes and bump the major.
- **Outcome names** are part of the public API.
- **Top-level field names** (`actor.sub`, `verb`, `outcome`, `cluster`,
  `resource.namespace`, …) are part of the public API.
- **`extra` field names** for already-shipped verbs are part of the
  public API. Adding new keys to `extra` for an existing verb is
  additive (minor/patch).
- The `Route` field exists on the Go struct and `StdoutSink` writes it
  as `route`; it is **not** persisted to SQLite (no column) and is
  therefore not returned by `/api/audit`. Treat `route` as a debug aid
  on the stdout stream, not a queryable dimension.

**Field-absence rules:**

- Empty strings on optional top-level fields (`actor.email`,
  `cluster`, every `resource.*` field, `reason`) are **omitted** in
  both stdout slog output and `/api/audit` JSON.
- In SQLite they are stored as `NULL`, not empty string, so
  `WHERE namespace IS NULL` queries are honest about cluster-scoped
  rows.

**Required fields** (every event must have these set or the row is
malformed):

- `timestamp` (Emitter stamps if caller leaves it zero)
- `actor.sub` (Emitter falls back to the per-request `RequestContext`
  Actor planted by `httpx.AuditBegin` + `auth.Middleware`)
- `verb`
- `outcome`

---

## 7. Identity propagation

Actor identity flows from one writer:

```
auth.Middleware  ──►  audit.PatchActor(ctx, Actor{Sub, Email, Groups})
                              │
                              ▼
                  *RequestContext on ctx (planted by httpx.AuditBegin)
                              │
                              ▼
            audit.Emitter.Record reads it when handler leaves Actor zero
```

The single-writer invariant is documented in
`internal/audit/context.go:53–65`: `PatchActor` mutates a shared
`*RequestContext` and **must** be called from the request goroutine,
never from a handler-spawned goroutine. The race detector catches
violations on first run with `go test -race`.

Handlers in `cmd/periscope/` populate `Actor` explicitly via
`actorFromContext(ctx)` (cmd/periscope/errors.go:61) which reads the
already-resolved `credentials.Session`. The Emitter's
"fall back to request context" path in
`internal/audit/emitter.go:56–58` exists for handlers that emit before
`credentials.Wrap` resolution (none today, but the lane is open).

---

## 8. Sink contract

`Sink.Record(ctx, evt)` is **infallible by signature**. A sink that
encounters a transient error must log it and return; it must not block
the calling handler. Rationale: a privileged action that returned 200
to the user must not be reverted because the audit DB was slow. The
unconditional `StdoutSink` ensures nothing is lost from the operator's
log stream even when a buffered sink drops.

Sinks are called **synchronously** from the request goroutine, in the
order they were attached to the Emitter. Today that means stdout first,
SQLite second. Re-ordering is a non-breaking change.

**No buffering, no batching, no async** in v1.0. A future
`KafkaSink` or `OpenTelemetrySink` that needs persistence guarantees
implements its own buffering — the Emitter is deliberately thin so
sinks don't share a single failure mode.

### `StdoutSink`

One `slog.InfoContext(ctx, "audit", attrs...)` call per event. Field
names match the pre-refactor handler conventions
(`category=audit`, `event=<verb>`, `actor.sub`, `cluster`,
`session_id`, `bytes_stdin`, …) so existing scrapers and SIEM queries
keep matching. `outcome`, `request_id`, `route`, `reason` are the
additive fields introduced by the refactor.

### `SQLiteSink`

Persisted, queryable, retention-bounded. Fully specified in 9–11.

---

## 9. SQLite schema (v1)

Schema version is tracked in `PRAGMA user_version`. v1.0 is at v1.

```sql
CREATE TABLE audit_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    ts_unix_nano  INTEGER NOT NULL,
    request_id    TEXT,
    route         TEXT,
    actor_sub     TEXT NOT NULL,
    actor_email   TEXT,
    actor_groups  TEXT,    -- JSON array
    verb          TEXT NOT NULL,
    outcome       TEXT NOT NULL,
    cluster       TEXT,
    res_group     TEXT,
    res_version   TEXT,
    res_type      TEXT,
    res_namespace TEXT,
    res_name      TEXT,
    reason        TEXT,
    extra         TEXT     -- JSON object
);

CREATE INDEX idx_audit_ts        ON audit_events (ts_unix_nano);
CREATE INDEX idx_audit_actor_ts  ON audit_events (actor_sub, ts_unix_nano);
CREATE INDEX idx_audit_verb_ts   ON audit_events (verb, ts_unix_nano);
CREATE INDEX idx_audit_outcome_ts ON audit_events (outcome, ts_unix_nano);
CREATE INDEX idx_audit_scope_ts  ON audit_events (cluster, res_namespace, ts_unix_nano);
```

Indexes match the `/api/audit` filter set: every queryable dimension
either has its own composite-with-`ts` index or is a leading column of
one. `ORDER BY ts_unix_nano DESC, id DESC` gives deterministic
pagination across rows that share a nanosecond timestamp.

PRAGMAs set at open time:

| PRAGMA | Value | Why |
|---|---|---|
| `journal_mode` | `WAL` | Reads (the `/api/audit` query) don't block writes. |
| `busy_timeout` | `5000` (ms) | Burst writes from concurrent handlers wait briefly rather than failing. |
| `synchronous` | `NORMAL` | Trades a microscopic durability window for throughput; appropriate for an audit cache where stdout is the hard backstop. |
| `wal_autocheckpoint` | `1000` (pages, ~4 MiB) | Caps WAL growth between explicit checkpoints. |

### Schema evolution

- Migrations are append-only entries in `migrations` in
  `internal/audit/sqlite_sink.go:194`. **Never edit a shipped
  migration** — it may have already run on production DBs.
- Each migration runs in a transaction; failure aborts the transaction
  but does not corrupt history.
- A DB at a higher `user_version` than the binary knows is **refused at
  open time**. This protects an operator from rollback-by-mistake
  silently corrupting future-schema data.
- A DB at a lower `user_version` is migrated forward as part of normal
  startup. Migration takes < 100 ms on a 1 M-row DB; no readiness-probe
  budget concern.

**Adding a column** (additive): new migration entry that runs
`ALTER TABLE audit_events ADD COLUMN ...`. SQLite supports this without
a table rewrite. The Go struct gains the field; readers handle absent
values for old rows (NULL → empty string, same as the existing
nullable columns).

**Removing or renaming a column** is a breaking schema change and is
treated as a major version (would land in v2 with a migration
strategy).

---

## 10. Retention semantics

Two caps act as belt-and-suspenders:

```yaml
audit:
  retentionDays: 30     # delete rows older than this (0 = disabled)
  maxSizeMB:    1024    # delete oldest until file ≤ this size (0 = disabled)
  vacuumInterval: 24h   # how often the prune+VACUUM loop runs
```

Whichever cap binds first wins. **Setting both to 0 is the
unbounded-growth footgun**; `Validate()` warns at startup but does
not refuse to boot.

### Prune algorithm (per tick)

1. **Age prune** (if `retentionDays > 0`):
   `DELETE FROM audit_events WHERE ts_unix_nano < (now − retentionDays × 24h)`.
   Indexed on `ts_unix_nano`; cost is `O(rows-to-delete)`.

2. **Size prune** (if `maxSizeMB > 0`): single-shot. Compute
   `excess = (file_size − target) / file_size`, drop the oldest
   `rowCount × excess × 1.1` rows. The 10% headroom lands the file
   comfortably under the cap after VACUUM rather than just at it.
   See `pruneBySize` in `sqlite_sink.go:410` for why it's single-shot
   rather than a loop (SQLite doesn't shrink the file between
   DELETEs without an intervening VACUUM, so an in-loop convergence
   test never converges).

3. **VACUUM** (only if step 1 or 2 deleted anything): reclaims freed
   pages back to the filesystem. Holds an exclusive write lock for
   the duration (5–30 s on a 1 GiB DB). Concurrent `Record()` calls
   block on the busy_timeout (5 s) and may end up logged-and-dropped
   under sustained pressure — `StdoutSink` continues to capture them.

4. **WAL truncate**: `PRAGMA wal_checkpoint(TRUNCATE)`. Without this,
   `audit.db-wal` can grow alongside `audit.db` on busy writers and
   eat into the operator's on-disk budget.

### Initial sweep

`OpenSQLiteSink` runs steps 1–4 **synchronously** before returning,
with a **30-second deadline**. Handles "pod woke up after a long
downtime with stale rows" without making the readiness probe wait
minutes. If 30 s isn't enough the regular loop catches up on its
24 h cadence; the pod still becomes Ready.

### Volume sizing

In `pvc` mode the operator sets `audit.storage.size` directly. In
`emptyDir` mode the kubelet `sizeLimit` is **auto-derived** as
`2 × maxSizeMB`. The 2× factor covers VACUUM's transient
file-doubling plus WAL growth between checkpoints.

`Validate` warns when `maxSizeMB × 2 > availableDiskMB` at the
parent directory — i.e. the volume cannot survive a VACUUM cycle. The
check is best-effort (skipped when the directory doesn't yet exist
because Validate runs before the volume is mounted in some test
configurations).

### Failure modes

| What fails | What happens |
|---|---|
| `OpenSQLiteSink` (PVC unmountable, schema migration error, disk full) | `slog.Warn`, continue with stdout-only, `/api/audit` returns 404. Pod becomes Ready. |
| `Record` exec | `slog.Error`, swallow, continue. Stdout sink already wrote the row. |
| Retention `DELETE` | `slog.Error`, continue. Next tick retries. |
| `VACUUM` | `slog.Error`, continue. File stays at high-water mark; next prune retries. |
| WAL checkpoint | `slog.Error`, continue. WAL grows until next successful checkpoint. |

The pipeline never blocks the pod from booting and never blocks a
privileged request.

---

## 11. Read API: `GET /api/audit`

Registered **only when** the SQLite sink opened successfully. When
audit persistence is off (or open failed) the route is unregistered
and returns 404 — the right shape for "feature off."

### Request

```
GET /api/audit?
  actor=<sub>
  &verb=<apply|delete|trigger|secret_reveal|exec_open|exec_close>
  &outcome=<success|failure|denied>
  &cluster=<name>
  &namespace=<ns>
  &name=<resource-name>
  &request_id=<id>
  &from=<RFC3339Nano>     // inclusive
  &to=<RFC3339Nano>       // exclusive
  &limit=<1..500>          // default 50
  &offset=<n>
```

All filters compose with AND. Empty filters are ignored. Bad time or
int values return 400; unknown verb/outcome strings return rows
matching the literal value (i.e. zero rows).

### Response

```json
{
  "items":  [ /* Row, see 6 */ ],
  "total":  1247,
  "limit":  50,
  "offset": 0
}
```

`total` is the count under the same WHERE clause (cheap on the
retention-capped DB). `items` is ordered newest-first
(`ORDER BY ts_unix_nano DESC, id DESC`).

`limit` is **clamped at 500** server-side. A client asking for 1000
gets 500 back with `limit: 500` in the response — no error, no surprise
empty page.

### Read-side RBAC: `X-Audit-Scope`

Two scopes, returned as a response header:

- **`X-Audit-Scope: self`** — the server **hard-overrides** the
  `actor` filter to the caller's own subject regardless of what the
  client passed. The caller can self-audit but never sees what
  colleagues did.
- **`X-Audit-Scope: all`** — the caller can query any actor's rows.

Resolution order (in `auditQueryHandler`, delegating to
`authz.Resolver.IsAuditAdmin`):

1. **`auth.authorization.auditAdminGroups`** — explicit operator
   switch. If non-empty, scope is `all` iff any of the user's IdP
   groups appears in the list. **Wins regardless of authz mode**.
2. **Mode-specific fallback:**

   | Mode | Default audit-admin |
   |---|---|
   | `tier`   | Users whose resolved tier is `admin` |
   | `shared` | Users in `allowedGroups` (only when that list is non-empty) |
   | `raw`    | Nobody — must use `auditAdminGroups` explicitly |

3. Otherwise → `self`.

`auditAdminGroups` is matched against the user's **raw IdP groups** (no
prefix), even in raw mode where K8s impersonation prefixes them. This
is intentional: audit-admin is decoupled from K8s admin so a security
team can read history without needing cluster-mutation power.

The SPA reads `X-Audit-Scope` to render a banner explaining the scope
to the user, and consults `/api/whoami` (which returns `auditEnabled`
and `auditScope`) to hide audit nav when the feature is off.

---

## 12. Compatibility and forward evolution

### What is covered by semver

- **Verb names and outcome names** (the closed enums).
- **Top-level event field names** in JSON (`actor.sub`, `verb`,
  `outcome`, `cluster`, `resource.{group,version,resource,namespace,name}`,
  `reason`, `extra`).
- **`extra` field names** for already-shipped verbs.
- **`/api/audit` query param names** and response field names.
- **Sink interface** (`Record(ctx, evt)`).
- **Reader interface** (`Query(ctx, args) → result, error`).

### What is NOT covered by semver

- Stdout slog field ordering (slog does not guarantee key order
  anyway).
- Internal SQL queries, index names, PRAGMA values.
- The `route` field (debug aid, not persisted).
- Prune algorithm details (single-shot vs loop, headroom factor).
- Specific error wording in `reason` — that's `err.Error()` from
  client-go, which is upstream-defined and itself not semver-stable.

### Adding a verb (minor version)

1. Add the constant in `internal/audit/event.go`.
2. Update 4 of this RFC with the emission site, outcome classes, and
   `extra` shape.
3. Wire the emission at the handler. Reuse `actorFromContext` and
   `outcomeFor`.
4. No schema migration needed — verb is a TEXT column.

### Adding a column (minor version)

1. Append a new migration to `migrations` in `sqlite_sink.go`.
2. Add the column to the Go struct and the `Row` JSON tags.
3. Update 6 (top-level shape) and 9 (schema) of this RFC.
4. Old rows return NULL → empty in JSON, which existing clients
   already tolerate for the existing nullable columns.

### Renaming or removing a verb / column / field (major version)

Lands in v2 with a documented migration. The schema version refusal
in `runMigrations` (a binary at v1 refuses to open a DB at v2) means
operators must roll forward, but not cliff-edge: the v2 binary reads
v1 DBs.

---

## 13. Security considerations

**Tamper resistance.** v1.0 trusts the operator with disk access. An
admin who can `kubectl exec` into the pod can edit `audit.db` directly.
The mitigations in v1.0 are:
- Stdout shipping to an external SIEM (operator's responsibility) gives
  an out-of-band copy that the in-pod admin cannot retroactively edit.
- Read-only root filesystem and dropped capabilities limit casual
  tampering from inside the container — but the audit volume is
  necessarily writable to the periscope process.

v2 adds hash-chain signing: each row carries a hash of the previous
row, signed by a per-pod key. An operator can detect retroactive edits
(any break in the chain) without preventing them. The ledger is
verified on read.

**Sensitive data in `reason`.** `reason` is `err.Error()` from
client-go on failure paths. Kubernetes error strings can contain
resource names, namespaces, even snippets of the rejected payload.
Treat `reason` as roughly the same sensitivity as an apiserver audit
log line: visible to anyone with audit-read access. The `secret_reveal`
verb itself only records the secret **key name** and the **size** of
the decoded value, never the value.

**Sensitive data in `extra`.** Schema-level discipline: contributors
adding `extra` keys for new verbs review them against this list:

- ✅ identifiers, sizes, durations, exit codes, transport names,
  session IDs, command vectors (the user *typed* them, audit shows them)
- ❌ stdin payloads, secret values, kubeconfig contents, AWS
  credentials, OIDC access tokens, raw response bodies, manifest YAML

The exec stdin/stdout payload **never** appears in audit fields
(`bytes_stdin` / `bytes_stdout` are byte counts, not contents). RFC
0001 17 acceptance criterion #7 enforces this with a test.

**RBAC bypass risk.** The `actor` filter override is **server-side**
in `auditQueryHandler`. There is no client-supplied way to escape it
when the resolver returns `self` scope — the server overwrites the
field unconditionally, ignoring whatever the client sent. The
`X-Audit-Scope` header lets the SPA render the right banner; it is
not load-bearing for security.

---

## 14. Acceptance criteria

The audit pipeline is complete (v1.0) when:

1. Every privileged handler in `cmd/periscope/` (apply, delete,
   trigger, secret_reveal, exec_open, exec_close) emits exactly one
   `audit.Event` per request lifecycle, with `Outcome` set on every
   non-canceled path.
2. The `Verb` and `Outcome` constants are the only string sources
   for those fields — no free-string literals at emission sites
   (`grep -rn 'audit.Event{' cmd/ internal/` shows only constant
   verbs).
3. `OpenSQLiteSink` failures (mkdir, open, ping, migrate, prepare)
   leave the pod Ready with stdout-only audit and `/api/audit`
   unregistered.
4. `Record` errors during normal operation log at `slog.Error` and
   never propagate to the calling handler (verified by
   `sqlite_sink_test.go`).
5. The retention loop honors both caps independently and the
   "both zero" combo is rejected at startup with a `slog.Warn`.
6. A binary at schema v1 refuses to open a DB at schema v2.
7. `GET /api/audit` returns `404` when SQLite is off.
8. `GET /api/audit` with no admin grant returns `X-Audit-Scope: self`
   and ignores any client-supplied `actor=` parameter
   (`audit_handler_test.go` covers both directions).
9. `limit > 500` is silently clamped to 500.
10. RFC 0001 17 #7 (no stdin payloads in any audit field) holds.

All ten are met as of v1.0.0.

---

## 15. Future work

- **v1.x — `log_open` emission.** Wire the reserved verb behind a
  rate-aware emitter so a tail-follow doesn't flood the table.
- **v2 — hash-chain signing.** Append a `prev_hash` column and a
  per-pod signing key. Verify on read.
- **v2 — external SIEM contract.** Document a stable JSON schema
  version (`schema_version` field) so downstream parsers don't need
  to track Periscope versions.

---

## 16. Appendix — references

- [`internal/audit/event.go`](../../internal/audit/event.go) — `Event`, `Verb`, `Outcome`, `Actor`, `ResourceRef`
- [`internal/audit/emitter.go`](../../internal/audit/emitter.go) — fanout
- [`internal/audit/sink.go`](../../internal/audit/sink.go) — `Sink` interface, `StdoutSink`
- [`internal/audit/sqlite_sink.go`](../../internal/audit/sqlite_sink.go) — schema, retention, query
- [`internal/audit/reader.go`](../../internal/audit/reader.go) — `Reader` interface, `QueryArgs`, `Row`
- [`internal/audit/context.go`](../../internal/audit/context.go) — `RequestContext`, `PatchActor`
- [`cmd/periscope/audit_handler.go`](../../cmd/periscope/audit_handler.go) — `/api/audit` HTTP handler
- [`cmd/periscope/errors.go`](../../cmd/periscope/errors.go) — `outcomeFor`, `actorFromContext`
- [`docs/setup/audit.md`](../setup/audit.md) — operator-facing guide
- RFC 0001 10 — pre-existing exec audit-event shape, preserved by `StdoutSink`
- RFC 0002 3 — actor identity sources (OIDC subject, IdP groups)
