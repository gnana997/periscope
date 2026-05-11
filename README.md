<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://periscopehq.dev/readme-banner-dark.png">
    <img alt="periscope · one dashboard. every cluster." src="https://periscopehq.dev/readme-banner-light.png" width="100%">
  </picture>
</p>

# Periscope

> A multi-cluster Kubernetes console — keyless on EKS via Pod Identity / IRSA, anything-with-egress via the periscope-agent tunnel.

[![CI](https://github.com/gnana997/periscope/actions/workflows/ci.yaml/badge.svg?branch=main)](https://github.com/gnana997/periscope/actions/workflows/ci.yaml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Latest Release](https://img.shields.io/github/v/release/gnana997/periscope?include_prereleases&sort=semver)](https://github.com/gnana997/periscope/releases/latest)
[![Artifact Hub](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/periscope)](https://artifacthub.io/packages/search?repo=periscope)
[![Artifact Hub agent](https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/periscope-agent)](https://artifacthub.io/packages/search?repo=periscope-agent)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![Node](https://img.shields.io/badge/Node-22-339933.svg)](https://nodejs.org/)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/gnana997/periscope/badge)](https://scorecard.dev/viewer/?uri=github.com/gnana997/periscope)
[![CodeQL](https://github.com/gnana997/periscope/actions/workflows/codeql.yml/badge.svg?branch=main)](https://github.com/gnana997/periscope/actions/workflows/codeql.yml)

**v1.0.0 launched.** Read the [launch announcement](https://github.com/gnana997/periscope/discussions/70) — origin story, what shipped, what's not in v1.0, and what's next.

> **Status — v1.0 stable.** Public HTTP API, configuration shape, and Helm values are covered by semver: breaking changes will land in a future major (v2). Bugfixes and additive features land on minor / patch tags off `main`. See [`CHANGELOG.md`](CHANGELOG.md) for what shipped per release.

## What is Periscope

Periscope is a self-hosted, multi-cluster Kubernetes console focused on EKS environments where modern compliance regimes make static AWS credentials hard to justify. It authenticates **to** clusters using Pod Identity / IRSA, authenticates **users** via OIDC, and writes a structured audit trail signed by the human who took each action — all from a stateless, single-binary deployment.

## Why Periscope

- **No long-lived AWS keys.** Cluster access is obtained on demand via Pod Identity or IRSA. Nothing static lives on the console pod.
- **Real human in every audit row.** Every K8s call carries the user's OIDC identity via impersonation, so the audit log shows `alice@corp` — never `periscope-bot`.
- **OIDC-gated user identity.** Auth0 and Okta tested. Authorization by IdP group, with configurable tiers.
- **Searchable audit log.** SQLite-backed, with a first-class in-app view, time-filterable and retention-bounded — compliance reviews stop being a log-grep exercise.
- **Live, not polled.** 21+ resource list pages stream over SSE for real-time updates, with a tested polling fallback for restrictive proxies.
- **Schema-aware YAML editor.** Built-in kinds and Custom Resources. Server-side apply, field-ownership glyphs, conflict resolution, live drift detection while editing.

## Quickstart

### Try locally with kind <span title="60-second evaluation">⚡</span>

The fastest way to see Periscope without touching AWS or an IdP. Runs on any local Kubernetes — kind / k3d / minikube. Sign-in uses an in-process dev fallback so anyone hitting the dashboard becomes `dev@local`.

```sh
# 1. Create a local cluster
kind create cluster --name periscope-demo

# 2. Install with the kind-tuned values
helm install periscope \
  oci://ghcr.io/gnana997/charts/periscope --version 1.0.5 \
  --namespace periscope --create-namespace \
  --values https://periscopehq.dev/examples/values-kind.yaml

# 3. Wait + open
kubectl -n periscope wait --for=condition=Available deploy/periscope --timeout=120s
kubectl -n periscope port-forward svc/periscope 8080:8080

# 4. Visit http://localhost:8080 in your browser.
#    Sign-in is automatic — you arrive as "dev@local". No IdP setup.
```

Cleanup: `kind delete cluster --name periscope-demo`.

> ⚠️  Dev sign-in is for local evaluation only. Don't run this profile against a cluster that holds real workloads — anyone who can reach the SPA becomes the configured `dev` user with `cluster-admin` impersonation.


### Run locally

Prerequisites: Go 1.26, Node 22, and a kubeconfig with access to at least one cluster.

```sh
make backend    # Go API on :8088
make frontend   # Vite dev server on :5173 (proxies /api -> :8088)
```

Open <http://localhost:5173>.

### Install on a cluster

A Helm chart lives at [`deploy/helm/periscope/`](deploy/helm/periscope/). Full walkthrough including OIDC client setup and Pod Identity / IRSA wiring: [`docs/setup/deploy.md`](docs/setup/deploy.md).


```sh
# Pin to a specific version. Find the latest at
# https://artifacthub.io/packages/helm/periscope/periscope
helm install periscope \
  oci://ghcr.io/gnana997/charts/periscope \
  --version <VERSION> \
  --namespace periscope --create-namespace
```

For CI / scripts that always want the latest stable, resolve the tag from the GitHub API:

```sh
LATEST=$(curl -s https://api.github.com/repos/gnana997/periscope/releases/latest \
  | jq -r .tag_name | sed 's/^v//')
helm install periscope \
  oci://ghcr.io/gnana997/charts/periscope \
  --version "$LATEST" \
  --namespace periscope --create-namespace
```

Both signed (cosign keyless) and discoverable on [Artifact Hub](https://artifacthub.io/packages/helm/periscope/periscope). To verify the chart signature before install, see the verification snippet in [`docs/RELEASING.md`](docs/RELEASING.md).

## Features

**Authentication & access**
- Pod Identity / IRSA for cluster access (no static AWS credentials on the pod)
- OIDC user auth with IdP-group-gated authorization (shared / tier / raw modes)
- Per-cluster RBAC enforced server-side via `Impersonate-User` / `Impersonate-Group` headers
- Pre-flight RBAC checks (SAR / SSRR) so disabled buttons explain why instead of failing on click

**Multi-cluster**
- Fleet view at `/` — every registered cluster as a status card with identity, hot signals, and one-click drill-in
- Switch context from the cluster rail (Slack-style left bar)
- Per-cluster scoping for every resource view
- Add managed clusters via `backend: agent` (#42) — `kubectl apply` an agent on any K8s with outbound HTTPS, no IAM trust required. Works on EKS, GKE, AKS, on-prem k3s.

**Browsing & inspection**
- Common resources (pods, deployments, services, configmaps, secrets, jobs, ingresses, RBAC, …) plus full Custom Resource catalog
- Live events, describe view, logs (with follow + filtering)
- In-browser pod shell (`exec`) with reconnect on transient disconnects — works on every backend (eks, kubeconfig, in-cluster, agent)
- Cmd+K palette: search resources by name across the active cluster

**Real-time updates (watch streams)**
- 21+ resource list pages stream over SSE for live updates spanning workloads, networking, storage, and cluster-scoped resources
- Per-user concurrency cap to keep apiserver watch quotas safe
- Polling fallback when the EventSource path fails (corporate proxies, etc.)
- Operator opt-out via Helm: subset, group aliases (`workloads`, `networking`, `storage`, `cluster`, `core`), or full disable

**Editing**
- Inline Monaco YAML editor for any resource — built-in or CRD
- Schema-aware autocomplete and validation against the cluster's `/openapi/v3`
- Server-side apply with minimal diffs (no `last-applied` annotation churn)
- Field-ownership glyphs: see who manages each field before you edit
- Conflict resolution: per-field "keep mine / take theirs" when a controller owns the field
- Live drift detection: warns when the cluster changes underneath the editor
- Unsaved-changes guards on refresh, sidebar nav, row-click

**Helm**
- Release browser per cluster: per-release values, manifest, history, NOTES.txt, and structured dyff-based diff between revisions (read paths use direct Secret/ConfigMap decoding, no Helm SDK on the read path)
- In-browser install / upgrade / uninstall with Atomic-by-default rollback on partial failure
- Per-revision rollback from the history tab: pick the target revision, see the side-by-side diff before clicking, with `wait` / `cleanup-on-fail` / `disable-hooks` knobs surfaced inline
- Schema-aware values editor: structured form when the chart ships `values.schema.json`, Monaco YAML otherwise, with binary form/YAML toggle for `$ref`-heavy schemas
- Dry-run preview pane shows the rendered manifests + RBAC pre-flight denials + (upgrade) semantic diff against the live cluster before the operator commits
- Public HTTP and OCI chart fetch with SSRF protection (IMDS / link-local always blocked, RFC1918 opt-in via env)

**EKS managed add-ons**
- Browse installed add-ons with health, installed version, latest available, k8s compatibility window, and "blocks next k8s minor" warnings — feeds upgrade readiness
- Catalog browse of every AWS-published add-on for the cluster's K8s version, filterable by AWS / third-party / type
- Install / upgrade / delete actions with schema-aware configuration editor and `resolveConflicts` choice
- Right-edge detail pane with describe / config tabs: see the operator's stored `configurationValues`, IAM service account role, Pod Identity associations, and full version history with one-click "upgrade to" on newer versions
- Status-aware polling watches `CREATING` / `UPDATING` / `DELETING` flips so the UI stays in sync without manual refresh

**EKS upgrade readiness**
- Upgrade Insights surface with per-issue severity, kubernetes-version-step, and remediation links
- Managed node group AMI drift detection so operators see which groups need a rotation before bumping the cluster
- Works on `in-cluster`, `agent`, `eks`, and `kubeconfig` backends as long as the cluster entry has `arn` + `region`

**Karpenter**
- Curated read-only dashboard at `/clusters/{c}/karpenter`, auto-detected via the `karpenter.sh/v1` CRD probe — sidebar entry only appears on Karpenter-enabled clusters
- NodePool table with weight, disruption budgets, current/limit usage, and per-pool `$/hr` + spot-savings (controller `/metrics` scraped via apiserver service-proxy under impersonation)
- NodeClaims grouped by NodePool with `Drifted` / `Initialized` / `Launched` conditions surfaced as badges; pools with any drifted claim auto-expand
- Pending pods waiting on Karpenter with the per-NodePool incompatibility breakdown extracted from the `FailedScheduling` apiserver Event — operators no longer have to grep karpenter-controller logs to see *why* a pod isn't being scheduled
- Resizable detail pane on row click for describe / yaml / events without leaving the dashboard
- Graceful degradation: missing `/metrics` or events list failures degrade individual panels without failing the whole response

**Security & CVE surfacing**
- Inline severity chips on Pods / Nodes / Karpenter list pages — at-a-glance `2C · 5H · 12M` per row, sourced from Amazon Inspector v2
- New `security` detail-pane tab on Pod / Node / Deployment / StatefulSet / DaemonSet / Karpenter NodeClaim with the full finding list (description, remediation, EPSS score, exploit-availability flag, vendor advisory link, Inspector deep-link)
- Per-cluster local cache, lazily hydrated on first activation; reads are O(1) thereafter, with 6h TTL background refresh and a manual `↻ refresh` button (entity-scoped, emits one `cve_refresh` audit row)
- Empty-state contract: when Inspector v2 is disabled or the IAM grant is missing, every CVE-aware page renders an unobtrusive once-per-cluster hairline banner instead of erroring out
- Opt-in via Helm (`inspector.enabled: true`) — see [usage guide](docs/usage/cve.md) for the IAM block and cost considerations

**Audit & observability**
**Audit & observability**
- Every privileged action signed by the human user — apply, delete, exec, secret reveal, log open, cronjob trigger
- Persistent audit log: SQLite (single-replica), with retention and size caps
- First-class in-app audit view with filters by actor, verb, outcome, time range, namespace, request id
- Density timeline strip surfaces denials and failures at a glance
- Tier-mode audit-admin groups can see every actor's rows; everyone else sees their own
- Structured JSON events also stream to stdout for shipping into CloudWatch / Loki / OpenSearch / Datadog

## Documentation

**Setup**
- [Configuration & deployment](docs/setup/deploy.md)
- [Helm values reference](docs/setup/values.md)
- [Environment variables reference](docs/setup/environment-variables.md)
- [OIDC setup — Auth0](docs/setup/auth0.md)
- [OIDC setup — Okta](docs/setup/okta.md)
- [In-cluster RBAC the backend needs](docs/setup/cluster-rbac.md)
- [Audit log persistence](docs/setup/audit.md)
- [Watch streams (SSE) operator guide](docs/setup/watch-streams.md)
- [Helm release browser](docs/setup/helm-releases.md)
- [Pod exec setup](docs/setup/pod-exec.md)
- [NetworkPolicy](docs/setup/networkpolicy.md)
- [Apply YAML dialog](docs/setup/apply-yaml.md) — paste/drop multi-doc YAML with per-doc RBAC pre-flight
- [Multi-cluster onboarding (agent)](docs/setup/agent-onboarding.md) — register a managed cluster via the periscope-agent tunnel
- [EKS upgrade readiness](docs/setup/eks-upgrade-readiness.md) — Upgrade Insights + managed node group AMI drift

**Usage**
- [Karpenter dashboard](docs/usage/karpenter-view.md) — NodePool/$/hr/Drift/pending-pods walkthrough + cost-RBAC sample
- [CVE surfacing (Inspector v2)](docs/usage/cve.md) — chip column + Security tab walkthrough, enablement, audit + cost

**Architecture**
- [Architecture overview](docs/architecture/README.md) — component map, source-tree guide, reading order for new contributors
- [Watch streams — push model, fallback, RBAC](docs/architecture/watch-streams.md)
- [Agent tunnel — multi-cluster transport, PKI, registration](docs/architecture/agent-tunnel.md)

**Reference**
- [HTTP API reference (stability tiers, auth, conventions)](docs/api.md)

**RFCs**
- [RFC 0001 — Pod exec support](docs/rfcs/0001-pod-exec.md)
- [RFC 0002 — Authentication (OIDC + per-user K8s authz)](docs/rfcs/0002-auth.md)
- [RFC 0003 — Audit log: schema and retention semantics](docs/rfcs/0003-audit-log.md)

## Configuration

| What | Where |
|---|---|
| OIDC (user auth) | [`examples/config/auth.yaml.auth0`](examples/config/auth.yaml.auth0), [`examples/config/auth.yaml.okta`](examples/config/auth.yaml.okta) |
| Cluster registry | Helm values; see [deploy guide](docs/setup/deploy.md) |
| In-cluster RBAC | [`docs/setup/cluster-rbac.md`](docs/setup/cluster-rbac.md) |
| Audit persistence | Helm `audit:` block; see [`docs/setup/audit.md`](docs/setup/audit.md) |
| Watch streams | Helm `watchStreams:` block; see [`docs/setup/watch-streams.md`](docs/setup/watch-streams.md) |

## Architecture

Single Go binary embeds the React SPA. Stateless with respect to user credentials — OIDC sessions kept in memory only; no kubeconfigs or AWS keys persisted. Runs as non-root with a read-only root filesystem, no privilege escalation, and a `RuntimeDefault` seccomp profile (configured in the Helm chart).

For component-level detail see [`docs/architecture/`](docs/architecture/) and [`docs/rfcs/`](docs/rfcs/).

## Development

Repository layout:

```
cmd/periscope/    backend entry point
internal/         backend packages (auth, authz, audit, clusters, credentials, exec, k8s, secrets, sse, httpx, spa)
web/              React + TypeScript SPA (Vite, Monaco editor)
deploy/helm/      Helm chart
docs/             setup guides, architecture notes, RFCs
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

CI: every push and PR runs `golangci-lint`, `go test`, `npm run lint`, `npm test`, `npm run build`, `helm lint` + `helm template` smoke renders, and an embedded-binary build. See [`.github/workflows/ci.yaml`](.github/workflows/ci.yaml).

(See [`CONTRIBUTING.md`](CONTRIBUTING.md) for coding conventions, PR process, and a longer dev guide.)

## Roadmap

Planning is tracked in [GitHub Issues](https://github.com/gnana997/periscope/issues). Helm install / upgrade / uninstall and the full EKS add-on lifecycle (browse / install / upgrade / delete) shipped in v1.0.4. Areas in active scoping for the next minor: private OCI chart auth (ECR via Pod Identity / IRSA), richer per-cluster RBAC introspection, and EKS add-on detail enrichment (`requiresIamPermissions` / architecture / compute-type signals from `DescribeAddonVersions`).

## Community & support

- **Bugs & feature requests** — [GitHub Issues](https://github.com/gnana997/periscope/issues)
- **Questions & discussion** — [GitHub Discussions](https://github.com/gnana997/periscope/discussions)
- **Security vulnerabilities** — see [`SECURITY.md`](SECURITY.md)

## Contributing

Contributions are welcome. Read [`CONTRIBUTING.md`](CONTRIBUTING.md) before opening a PR. By participating in this project you agree to abide by its [`CODE_OF_CONDUCT.md`](CODE_OF_CONDUCT.md).

## License

[Apache License 2.0](LICENSE).
