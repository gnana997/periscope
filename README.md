# Periscope

A multi-cluster Kubernetes console for EKS, built around Pod Identity and IRSA.

> **Status — early development.** The core flows (multi-cluster auth, resource browsing, pod exec, logs, YAML editing) work, but APIs, configuration, and UI are still changing. Expect breaking changes between commits until a tagged release.

## Why this exists

Periscope exists to fill a specific gap: most multi-cluster Kubernetes consoles still require static AWS credentials to reach EKS clusters, which is increasingly hard to justify under modern compliance regimes. Periscope authenticates to EKS using Pod Identity or IRSA, so the console pod itself holds no long-lived AWS keys. It also keeps a structured audit trail of what users did, when, and against which cluster.

## What it does

- Authenticates **to** EKS clusters using Pod Identity or IRSA — no static AWS credentials stored or mounted on the console pod.
- Authenticates **users** via OIDC (Auth0 and Okta tested) and gates access by IdP group.
- Browses common Kubernetes resources (pods, deployments, services, configmaps, secrets, jobs, CRDs, …) across multiple clusters from one UI.
- Opens an interactive shell into a pod in the browser, with reconnect on transient disconnects.
- Views and edits resources as YAML with server-side apply, field-ownership hints, and conflict resolution when another controller owns a field.
- Emits structured JSON audit records for sensitive operations (pod exec sessions, secret reveal) covering user, target, and outcome.

## Compliance posture

- No static AWS credentials live in the console pod — cluster access is obtained on demand via Pod Identity or IRSA.
- User identity comes from OIDC; authorization is enforced by IdP group membership with configurable tiers.
- Sensitive operations (pod exec, secret reveal) emit structured JSON audit events, suitable for any standard log pipeline (CloudWatch, Loki, OpenSearch, Datadog, …).
- The Helm chart runs the workload as non-root with a read-only root filesystem, no privilege escalation, and a `RuntimeDefault` seccomp profile.
- The backend is stateless with respect to user credentials — OIDC sessions are kept in memory only, and no kubeconfigs or AWS keys are persisted.

## Audit logging

Every authenticated, sensitive action emits a structured JSON event covering the user, action, target cluster/resource, and outcome. Today these are written to the pod's stdout, where any standard log pipeline (CloudWatch, Loki, OpenSearch, Datadog, …) can pick them up. Pluggable storage backends and an in-app audit viewer are planned but not yet implemented.

## Quickstart

### Run locally (development)

Prerequisites: Go 1.26, Node 22, and a kubeconfig with access to at least one cluster.

```sh
make backend    # Go API on :8080
make frontend   # Vite dev server on :5173 (proxies /api -> :8080)
```

Open <http://localhost:5173>.

### Install on a cluster

A Helm chart is provided at [`deploy/helm/periscope/`](deploy/helm/periscope/). See [`docs/setup/deploy.md`](docs/setup/deploy.md) for the full walkthrough, including OIDC client setup and EKS Pod Identity / IRSA wiring.

## Configuration

- **OIDC (user auth).** Reference config: [`examples/config/auth.yaml.example`](examples/config/auth.yaml.example). Provider-specific guides: [`docs/setup/auth0.md`](docs/setup/auth0.md), [`docs/setup/okta.md`](docs/setup/okta.md).
- **Cluster RBAC.** What the backend's service account needs in each target cluster: [`docs/setup/cluster-rbac.md`](docs/setup/cluster-rbac.md).
- **Cluster registry.** The list of clusters the console can reach is supplied via Helm values; see the deploy guide.

## Development

Repository layout:

- `cmd/periscope/` — backend entry point.
- `internal/` — backend packages (`auth`, `authz`, `clusters`, `credentials`, `exec`, `k8s`, `secrets`, `httpx`, `spa`).
- `web/` — React + TypeScript SPA (Vite, Monaco editor).
- `deploy/helm/periscope/` — Helm chart.
- `docs/` — setup guides and RFCs.

Common Make targets:

| Target | Purpose |
|---|---|
| `make backend` | Run the Go backend on `:8080`. |
| `make frontend` | Run the Vite dev server on `:5173`. |
| `make build` | Build the SPA, embed it, and produce a single binary at `bin/periscope`. |
| `make test` | Run Go tests. |
| `make image` | Build the container image. |
| `make helm-lint` / `make helm-template` | Validate or render the chart locally. |

Frontend tests:

```sh
cd web && npm test
```

## License

See [LICENSE](LICENSE).