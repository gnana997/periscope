# Changelog

All notable changes to Periscope are tracked here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html):
the public HTTP API, the OIDC / cluster-registry config shape, and Helm
chart values are the surfaces covered by semver.

For per-release container images and signed Helm charts, see the
[GitHub Releases](https://github.com/gnana997/periscope/releases) page;
its auto-generated notes complement this file with the full PR list per
tag.

## [Unreleased]

### Documentation

- Added [`docs/architecture/README.md`](docs/architecture/README.md) —
  top-level architecture overview: component map, source-tree
  guide, suggested reading order for new contributors, and
  cross-cutting design choices (single binary + embedded SPA,
  stateless w.r.t. credentials, impersonation everywhere,
  pre-flight RBAC, audit-before-action).
- Added [`docs/setup/values.md`](docs/setup/values.md) — flat
  reference for every value in the periscope and periscope-agent
  Helm charts, organised by section, with type / default / notes
  per field. Single page operators can grep during a `helm upgrade`.
- Extended [`docs/setup/pod-exec.md`](docs/setup/pod-exec.md) with a
  dedicated "Operator notes for agent-backed clusters" section
  (transport path, RBAC, audit, latency, disconnect behavior) and
  an agent-specific troubleshooting bullet for the
  `cmd/periscope-agent/observability.go` Hijack shim regression.
- Stamped [RFC 0004](docs/rfcs/0004-exec-over-agent-tunnel-poc.md)
  status as "Implemented in v1.0.0".
- Normalized version nomenclature in operator-facing docs: `v1.x.0`
  / `v1.x.+` / `v1.x.1` collapsed to `v1.0` / `post-1.0` / `v1.x`
  per consistency.
- Tightened the `POST /api/agents/register` description in
  [`docs/api.md`](docs/api.md) to clarify "before registration"
  rather than the ambiguous "does not yet."
- README: explicit note that pod exec works on every backend
  including `agent`; new top-level architecture-overview link.

### Repository

- Added GitHub issue templates (`bug_report.yml`,
  `feature_request.yml`) and a pull-request template under
  `.github/`. Bug reports require backend, OIDC provider, and
  Periscope version up front; PR template prompts surfaces
  touched and a tested-paths summary.

## [1.0.0]

Initial stable release.

### Added

- **Authentication & access**
  - OIDC user authentication (Auth0 and Okta tested) with PKCE,
    state validation, and HttpOnly / Secure / SameSite session
    cookies.
  - Per-cluster RBAC enforced via `Impersonate-User` /
    `Impersonate-Group` headers — every K8s call carries the human
    user's identity.
  - Three authorization modes: `shared`, `tier`, `raw` — operator
    chooses how IdP groups map to in-cluster identity.
  - Pre-flight RBAC checks (SAR / SSRR) so disabled actions in the
    UI explain themselves instead of failing on click.
  - Pod Identity / IRSA factory for AWS access — no static AWS
    credentials on the pod.

- **Multi-cluster**
  - Fleet view aggregator at `/` over every registered cluster.
  - Cluster rail (left bar) for context switching.
  - Per-cluster scoping for every resource view.
  - In-cluster cluster backend for self-managed deployments — the
    chart auto-binds the periscope ServiceAccount to the
    impersonator role when a cluster is registered with
    `backend: in-cluster`.
  - Agent backend (#42) — per-cluster `periscope-agent` pod
    dials out to the central server over a long-lived mTLS-pinned
    WebSocket. Adds managed clusters via one `helm install` on the
    target cluster; works on EKS / GKE / AKS / on-prem k3s / kind,
    no IAM trust per cluster. PKI bootstrapped at server startup
    (per-deployment ECDSA P-256 CA in a K8s Secret); 15-min single-
    use bootstrap tokens; 90-day rotating client certs.
  - SPA "+ onboard cluster" button (admin-tier only) on the fleet
    page mints a token and renders the helm install command with the
    token baked in, copy-paste ready.
  - **Pod exec on agent-managed clusters** (#43, collapses into
    #42 per RFC 0004 §10). client-go's WebSocket and SPDY exec
    executors bypass `rest.Config.Transport`, so a loopback HTTP
    CONNECT proxy in `internal/k8s/agent_exec_proxy.go` translates
    per-cluster CONNECTs into tunnel dials. The agent's reverse
    proxy implements `http.Hijacker` so the WS / SPDY upgrade
    succeeds. Validation in `internal/k8s/exec_tunnel_test.go`
    (Tier 1 in-process) + `hack/poc-exec-tunnel/` (Tier 2 kind e2e).

- **Browsing & inspection**
  - List, detail, describe, events, and YAML for the common
    workload, networking, storage, RBAC, and config kinds.
  - Full Custom Resource catalog driven by `/openapi/v3`.
  - Live pod logs with follow + filtering.
  - In-browser pod shell (`exec`) with reconnect on transient
    disconnects, audited open / close events.
  - `Cmd+K` palette for cluster-wide name search.

- **Real-time updates (watch streams)**
  - 21+ resource kinds streamed over SSE (workloads, networking,
    storage, cluster-scoped) with a polling fallback.
  - `Last-Event-ID` resume on transient disconnects.
  - Per-user concurrency cap (`PERISCOPE_WATCH_PER_USER_LIMIT`,
    default 60) to protect apiserver watch quota.
  - Operator opt-out via Helm: subset, group aliases (`workloads`,
    `networking`, `storage`, `cluster`, `core`), or full disable.

- **Editing**
  - Inline Monaco YAML editor for built-in kinds and CRDs.
  - Schema-aware autocomplete and validation against the cluster's
    `/openapi/v3`.
  - Server-side apply with minimal diffs and field-ownership glyphs.
  - Per-field conflict resolution and live drift detection.
  - Unsaved-changes guards on refresh / nav / row-click.

- **Helm**
  - Read-only release browser per cluster — values, manifest,
    history, and `dyff`-based diff between revisions.
  - Auto-probes Secret vs ConfigMap storage drivers per cluster.
  - Bounded TTL cache for release listings.

- **Audit & observability**
  - Persistent SQLite audit sink with retention / size caps and
    a fail-open boot path (warn, continue with stdout-only).
  - First-class in-app audit view with filters by actor, verb,
    outcome, time range, namespace, request id; density timeline.
  - Tier-mode audit-admin groups see every actor's rows; everyone
    else sees their own.
  - Structured JSON events also stream to stdout for shipping to
    CloudWatch / Loki / OpenSearch / Datadog.

- **Packaging & supply chain**
  - Multi-arch container image (`linux/amd64`, `linux/arm64`)
    published to `ghcr.io/gnana997/periscope`.
  - Helm chart published to `ghcr.io/gnana997/charts/periscope`
    as an OCI artifact, discoverable on Artifact Hub.
  - Cosign keyless signatures (Sigstore) for both the image and
    the chart; SPDX SBOM attached to the image.
  - Distroless static base, non-root UID 65532, read-only root
    filesystem, all capabilities dropped, `RuntimeDefault`
    seccomp profile in the Helm chart.

### Fixed

- LogStream component no longer hits an infinite render loop when
  toggling wrap mode (#66).
- Auth: `periscope_session` cookie is now `SameSite=Lax` (was
  `Strict`). Strict suppressed the cookie on the post-OIDC-callback
  redirect to `/`, so first-time sign-in landed on the
  unauthenticated page until the user manually refreshed (#37).
- Auth: browser navigations to `/` (or any deep link) without a
  session now `302` to `/api/auth/login` instead of returning plain
  `401 unauthenticated` — XHR callers still get the 401 (#37).

### Security

- OIDC session and PKCE/state generation now propagate `crypto/rand`
  failures as errors instead of panicking the pod (#35). Login
  callbacks return 500 on the (vanishingly rare) RNG failure path
  rather than crashing the process and dropping every active
  session on the same replica.

### Documentation

- Added [RFC 0003 — Audit log: schema and retention semantics](docs/rfcs/0003-audit-log.md),
  formalizing the verb taxonomy, wire-stable event shape, SQLite
  schema, retention algorithm, `/api/audit` read-side RBAC, semver
  coverage, and the v1.0 security model (operator-trust now;
  hash-chain signing in v2).
- Added [`docs/api.md`](docs/api.md) — HTTP API reference with
  three stability tiers (Tier 1 stable, Tier 2 SPA-coupled,
  Tier 3 live channels), authentication / cookie / session
  contract, error-code enum, CSRF posture, and the
  `/api/v2/...` versioning policy for future majors.
- Added [`docs/setup/environment-variables.md`](docs/setup/environment-variables.md) —
  centralized reference for every `PERISCOPE_*` env var (and
  `PORT`) the binary reads, with defaults, Helm-value mapping,
  and the semver coverage rules for the configuration surface.
- Fixed stale `PERISCOPE_WATCH_PER_USER_LIMIT` default in
- Added [`docs/architecture/agent-tunnel.md`](docs/architecture/agent-tunnel.md) —
  design walkthrough for the agent backend: topology, PKI lifecycle,
  registration handshake, mTLS session lifecycle, the
  `rest.Config.Transport` substitution that keeps existing handlers
  unchanged, identity propagation, audit shape, and failure modes.
- Added [`docs/setup/agent-onboarding.md`](docs/setup/agent-onboarding.md) —
  operator how-to for registering a managed cluster via the agent
  backend: same-account flow with prereqs, the 5-step register-
  install-verify sequence, troubleshooting (mTLS handshake, token
  expiry, SAN mismatch), security notes, cross-account note.
- Added [`examples/agent/`](examples/agent/) — sample values files
  for both server + agent charts and a reference
  `register-and-install.sh` script.
- Extended `docs/api.md` with the three new agent-backend endpoints
  (`POST /api/agents/tokens` admin-only, `POST /api/agents/register`
  unauth + token-gated, `WS /api/agents/connect` mTLS-required).
- Extended `docs/setup/environment-variables.md` 11 covering the
  two server-side and six agent-side env vars introduced by #42.
- Extended `docs/setup/cluster-rbac.md` with the agent-backend
  RBAC story (the agent SA's impersonation lever, default
  ClusterRole shape, how to tighten).
  `docs/architecture/watch-streams.md` (was 30, code is 60).

[Unreleased]: https://github.com/gnana997/periscope/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/gnana997/periscope/releases/tag/v1.0.0
