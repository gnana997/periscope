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

- Added [RFC 0003 — Audit log: schema and retention semantics](docs/rfcs/0003-audit-log.md),
  formalizing the verb taxonomy, wire-stable event shape, SQLite
  schema, retention algorithm, `/api/audit` read-side RBAC, semver
  coverage, and the v1.0 security model (operator-trust now;
  hash-chain signing in v2).

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

### Security

- OIDC session and PKCE/state generation now propagate `crypto/rand`
  failures as errors instead of panicking the pod (#35). Login
  callbacks return 500 on the (vanishingly rare) RNG failure path
  rather than crashing the process and dropping every active
  session on the same replica.

[Unreleased]: https://github.com/gnana997/periscope/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/gnana997/periscope/releases/tag/v1.0.0
