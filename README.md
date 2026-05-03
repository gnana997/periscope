# Periscope

> A multi-cluster Kubernetes console for EKS, built around Pod Identity and IRSA.

[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![Node](https://img.shields.io/badge/Node-22-339933.svg)](https://nodejs.org/)

> **Status — early development.** Core flows (multi-cluster auth, resource browsing, pod exec, logs, YAML editing) work, but APIs, configuration, and UI are still changing. Expect breaking changes until a tagged release.

## What is Periscope

Periscope is a self-hosted, multi-cluster Kubernetes console focused on EKS environments where modern compliance regimes make static AWS credentials hard to justify. It authenticates **to** clusters using Pod Identity / IRSA, authenticates **users** via OIDC, and emits structured audit events for sensitive operations — all from a stateless, single-binary deployment.

## Why Periscope

- **No long-lived AWS keys.** Cluster access is obtained on demand via Pod Identity or IRSA. Nothing static lives on the console pod.
- **OIDC-gated user identity.** Auth0 and Okta tested. Authorization by IdP group, with configurable tiers.
- **Schema-aware YAML editor.** Built-in kinds and Custom Resources. Server-side apply, field-ownership glyphs, conflict resolution, live drift detection while editing.
- **Audit-ready.** Sensitive operations (pod exec, secret reveal) emit structured JSON events to stdout. Plug into CloudWatch, Loki, OpenSearch, Datadog — whatever you have.

## Quickstart

### Run locally

Prerequisites: Go 1.26, Node 22, and a kubeconfig with access to at least one cluster.

```sh
make backend    # Go API on :8088
make frontend   # Vite dev server on :5173 (proxies /api -> :8088)
```

Open <http://localhost:5173>.

### Install on a cluster

A Helm chart lives at [`deploy/helm/periscope/`](deploy/helm/periscope/). Full walkthrough including OIDC client setup and Pod Identity / IRSA wiring: [`docs/setup/deploy.md`](docs/setup/deploy.md).

## Features

**Authentication & access**
- Pod Identity / IRSA for cluster access (no static AWS credentials on the pod)
- OIDC user auth with IdP-group-gated authorization
- Per-cluster RBAC enforced server-side via impersonation

**Browsing & inspection**
- Multi-cluster: switch context from the sidebar, no kubeconfig juggling
- Common resources (pods, deployments, services, configmaps, secrets, jobs, ingresses, RBAC, …) plus full Custom Resource catalog
- Live events, describe view, logs (with follow + filtering)
- Cmd+K palette: search resources by name across the active cluster

**Editing**
- Inline Monaco YAML editor for any resource — built-in or CRD
- Schema-aware autocomplete and validation against the cluster's `/openapi/v3`
- Server-side apply with minimal diffs (no `last-applied` annotation churn)
- Field-ownership glyphs: see who manages each field before you edit
- Conflict resolution: per-field "keep mine / take theirs" when a controller owns the field
- Live drift detection: warns when the cluster changes underneath the editor
- Unsaved-changes guards on refresh, sidebar nav, row-click

**Observability**
- In-browser pod shell (`exec`) with reconnect on transient disconnects
- Structured JSON audit events (user, action, target, outcome) for sensitive operations

## Documentation

- [Configuration & deployment](docs/setup/deploy.md)
- [OIDC setup — Auth0](docs/setup/auth0.md)
- [OIDC setup — Okta](docs/setup/okta.md)
- [In-cluster RBAC the backend needs](docs/setup/cluster-rbac.md)
- [Architecture & RFCs](docs/rfcs/) — `0001-pod-exec.md`, `0002-auth.md`, …

## Configuration

| What | Where |
|---|---|
| OIDC (user auth) | [`examples/config/auth.yaml.auth0`](examples/config/auth.yaml.auth0), [`examples/config/auth.yaml.okta`](examples/config/auth.yaml.okta) |
| Cluster registry | Helm values; see [deploy guide](docs/setup/deploy.md) |
| In-cluster RBAC | [`docs/setup/cluster-rbac.md`](docs/setup/cluster-rbac.md) |

## Architecture

Single Go binary embeds the React SPA. Stateless with respect to user credentials — OIDC sessions kept in memory only; no kubeconfigs or AWS keys persisted. Runs as non-root with a read-only root filesystem, no privilege escalation, and a `RuntimeDefault` seccomp profile (configured in the Helm chart).

For component-level detail see [`docs/rfcs/`](docs/rfcs/).

## Development

Repository layout:

```
cmd/periscope/    backend entry point
internal/         backend packages (auth, authz, clusters, credentials, exec, k8s, secrets, httpx, spa)
web/              React + TypeScript SPA (Vite, Monaco editor)
deploy/helm/      Helm chart
docs/             setup guides and RFCs
examples/         reference configs
Makefile          common targets
```

Common targets:

| Target | Purpose |
|---|---|
| `make backend` | Run the Go backend on `:8088` |
| `make frontend` | Run the Vite dev server on `:5173` |
| `make build` | Build the SPA, embed it, produce a single binary at `bin/periscope` |
| `make test` | Run Go tests |
| `make image` | Build the container image |
| `make helm-lint` / `make helm-template` | Validate or render the chart locally |

Frontend tests:

```sh
cd web && npx vitest run
```

(See [`CONTRIBUTING.md`](CONTRIBUTING.md) for coding conventions, PR process, and a longer dev guide.)

## Roadmap

Until the project tags its first release, planning is informal — see [GitHub Issues](https://github.com/gnana997/periscope/issues) for what's open. A structured roadmap will be published alongside the v0.1 release.

## Community & support

- **Bugs & feature requests** — [GitHub Issues](https://github.com/gnana997/periscope/issues)
- **Questions & discussion** — [GitHub Discussions](https://github.com/gnana997/periscope/discussions) *(enable in repo settings)*
- **Security vulnerabilities** — see [`SECURITY.md`](SECURITY.md)

## Contributing

Contributions are welcome. Read [`CONTRIBUTING.md`](CONTRIBUTING.md) before opening a PR. By participating in this project you agree to abide by its [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## License

[Apache License 2.0](LICENSE).
