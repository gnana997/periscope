# Architecture

Contributor-facing design notes. The audience is someone changing
Periscope's internals, reviewing a PR that touches a load-bearing
seam, or trying to understand "why is this wired this way?"

For operator-facing how-tos (install, configure, troubleshoot), see
[`../setup/`](../setup/). For the wire contract between the SPA and
the backend, see [`../api.md`](../api.md). For accepted design
proposals (with full rationale and alternatives considered), see
[`../rfcs/`](../rfcs/).

## Component map

```
                       ┌──────────────────┐
                       │  Browser (SPA)   │
                       │  React + Vite +  │
                       │  Monaco          │
                       └────────┬─────────┘
                                │ HTTPS / SSE / WS
                                ▼
   ┌────────────────────────────────────────────────────────┐
   │  periscope (single Go binary, single pod by default)   │
   │                                                        │
   │  ┌──────────────────────────────────────────────────┐  │
   │  │ HTTP router (:8080)                              │  │
   │  │  ├ /api/auth/*       OIDC PKCE, session cookies  │  │
   │  │  ├ /api/whoami       resolved identity + tier    │  │
   │  │  ├ /api/clusters     registry + per-cluster API  │  │
   │  │  ├ /api/fleet        cross-cluster aggregator    │  │
   │  │  ├ /api/audit        SQLite-backed audit reads   │  │
   │  │  ├ /api/agents/*     token mint + register       │  │
   │  │  └ /                 embedded SPA assets         │  │
   │  └────────────────────┬─────────────────────────────┘  │
   │                       │                                │
   │  ┌────────────────────▼────────────────────────────┐   │
   │  │ Per-cluster handler (apply / list / watch /     │   │
   │  │ delete / can-i / exec / logs / helm)            │   │
   │  └────────────────────┬────────────────────────────┘   │
   │                       │ rest.Config built per request  │
   │                       │ with Impersonate-User =        │
   │                       │ <human's OIDC sub>             │
   │  ┌────────────────────▼────────────────────────────┐   │
   │  │ Backend factory (internal/k8s/client.go):       │   │
   │  │   eks         → EKS bearer via IRSA / Pod ID    │   │
   │  │   kubeconfig  → file-loaded kubeconfig          │   │
   │  │   in-cluster  → kubelet-mounted SA              │   │
   │  │   agent       → tunnel-bound RoundTripper       │   │
   │  └─────────────────────────────────────────────────┘   │
   │                                                        │
   │  ┌─────────────────────────────────────────────────┐   │
   │  │ TLS listener (:8443)                            │   │
   │  │  └ /api/agents/connect    mTLS-required         │   │
   │  │    (only mounted when agent.enabled=true)       │   │
   │  └─────────────────────────────────────────────────┘   │
   │                                                        │
   │  ┌─────────────────────────────────────────────────┐   │
   │  │ Audit pipeline (internal/audit)                 │   │
   │  │  ├ stdout JSON sink                             │   │
   │  │  └ SQLite sink (retention + size caps)          │   │
   │  └─────────────────────────────────────────────────┘   │
   └────────────────────────────────────────────────────────┘
```

The binary is **stateless w.r.t. user credentials**: OIDC sessions
live in memory only; no kubeconfigs or AWS keys persist on disk.
Audit rows in SQLite are the only durable state on the server pod.

## Source tree

| Path | What it is |
|---|---|
| [`cmd/periscope/`](../../cmd/periscope/) | Server binary entry, route mounting, handler wiring |
| [`cmd/periscope-agent/`](../../cmd/periscope-agent/) | Agent binary: tunnel client + local reverse proxy |
| [`internal/auth/`](../../internal/auth/) | OIDC PKCE flow, session cookies, identity resolution |
| [`internal/authz/`](../../internal/authz/) | Tier mode (shared / tier / raw), IdP-group → impersonated identity mapping |
| [`internal/audit/`](../../internal/audit/) | Verb taxonomy, sinks (stdout + SQLite), retention enforcement |
| [`internal/clusters/`](../../internal/clusters/) | Cluster registry (YAML), per-cluster overrides, validation |
| [`internal/credentials/`](../../internal/credentials/) | IRSA / Pod Identity bearer-token factory for `backend: eks` |
| [`internal/exec/`](../../internal/exec/) | Pod exec session lifecycle, idle / heartbeat / cap timers |
| [`internal/k8s/`](../../internal/k8s/) | rest.Config factory per backend, list/watch/apply/delete/exec primitives |
| [`internal/tunnel/`](../../internal/tunnel/) | Agent transport (rancher/remotedialer wrap, mTLS authorizer) |
| [`internal/sse/`](../../internal/sse/) | SSE writer, `Last-Event-ID` resume |
| [`internal/secrets/`](../../internal/secrets/) | Secret reveal (audited), envelope unwrap |
| [`internal/spa/`](../../internal/spa/) | Embedded SPA bundle, asset serving |
| [`internal/httpx/`](../../internal/httpx/) | Middleware (request id, logging, CSRF / origin checks) |
| [`web/`](../../web/) | React + TypeScript + Vite SPA (Monaco editor, React Query, Tailwind) |
| [`deploy/helm/`](../../deploy/helm/) | Server + agent Helm charts |

## Reading order for new contributors

If you're new to the codebase, the fastest path to context:

1. **`cmd/periscope/main.go`** — the route table is the index. Skim
   it once and you know what every URL maps to.
2. **`internal/k8s/client.go::buildRestConfig`** — the load-bearing
   per-request factory. Every backend, every impersonation header,
   every transport substitution flows through here.
3. **[RFC 0002 — Authentication](../rfcs/0002-auth.md)** — how user
   identity gets from an OIDC IdP into an `Impersonate-User` header.
4. **[RFC 0003 — Audit log](../rfcs/0003-audit-log.md)** — verb
   taxonomy and what every privileged handler emits.
5. **One concrete handler** — `cmd/periscope/pods_handler.go` is a
   good first read; it exercises the registry → factory →
   impersonation → handler → audit chain end-to-end.

## Deep dives in this directory

- **[`watch-streams.md`](./watch-streams.md)** — how live list pages
  push updates over SSE. Covers list-then-watch semantics, the
  `kindReg` extension point, per-user concurrency caps, polling
  fallback for restrictive proxies, `Last-Event-ID` resume.
- **[`agent-tunnel.md`](./agent-tunnel.md)** — how the central
  server reaches managed clusters when `backend: agent`. Covers
  topology, PKI lifecycle, registration handshake, mTLS session
  lifecycle, the `rest.Config.Transport` substitution that keeps
  existing handlers unchanged, identity propagation through the
  tunnel, audit shape, and failure modes.

Other surfaces (auth, audit, exec) currently live as RFCs in
[`../rfcs/`](../rfcs/) rather than separate architecture docs —
the RFCs were written as design proposals but read as accurate
design references. New architecture docs may carve them out
post-1.0 if the divergence between "as-shipped" and "as-RFC'd"
grows.

## Cross-cutting design choices worth knowing

- **Single binary, embedded SPA.** No separate frontend deployment.
  `make build` produces `bin/periscope` with the Vite-built SPA
  embedded via `embed.FS`.
- **Stateless w.r.t. credentials.** OIDC sessions are in-memory
  only. Restart drops sessions; users sign in again. Multi-replica
  deploys share no session state in v1.0 (a post-1.0 concern).
- **Impersonation everywhere.** No handler talks to apiserver "as
  Periscope." Every K8s call sets `Impersonate-User` to the human's
  OIDC sub. The bridge identity (EKS / SA token) is only the
  transport credential; per-call authz is the human's RBAC.
- **Pre-flight RBAC.** Disabled SPA actions explain themselves
  via SAR / SSRR pre-flight rather than failing on click.
- **Audit before action.** Privileged handlers emit
  `*.attempted` before the K8s call and `*.succeeded` /
  `*.failed` after. So a denied / errored action still leaves a
  row.
- **Stability tiers on the API.** Not every endpoint is semver-
  stable. See [`../api.md`](../api.md) §2 for the three-tier
  classification.

## Where things deliberately aren't

A few things you might expect to find but won't:

- **No central database.** SQLite for audit; in-memory for
  everything else. Postgres support is post-1.0.
- **No kubeconfig persistence.** kubeconfig-backed clusters load
  on demand; nothing is cached on disk.
- **No agent-side audit emission.** Audit happens server-side, in
  the same handlers, regardless of backend. The agent is a dumb
  transport.
- **No cluster-mutating operations from the SPA without the
  user's RBAC.** The agent's SA (or the EKS bridge identity) is
  the ceiling for what's *physically possible*, but every
  individual action runs as the impersonated user.
