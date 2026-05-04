# Helm values reference

Canonical reference for every value in the Periscope and
periscope-agent Helm charts. For walkthroughs and the "why" behind
each block, follow the per-topic links — this page is the
exhaustive flat list operators reach for during a `helm upgrade`.

The source of truth is each chart's `values.yaml`. This doc is
re-derived from those files; if a value here disagrees with the
chart, the chart wins and please file a docs bug.

- Chart: [`deploy/helm/periscope/values.yaml`](../../deploy/helm/periscope/values.yaml)
- Agent chart: [`deploy/helm/periscope-agent/values.yaml`](../../deploy/helm/periscope-agent/values.yaml)
- Schema validators (run on every `helm install` / `template`):
  [`values.schema.json`](../../deploy/helm/periscope/values.schema.json)
  and the agent's
  [`values.schema.json`](../../deploy/helm/periscope-agent/values.schema.json)

## Stability

Every value documented here is part of the **v1.0 public
configuration surface** and covered by semver: breaking changes
(rename, type change, removal) require a major bump (v2). New
values may land additively in any minor release with safe defaults.

---

# Server chart (`periscope`)

## image

| Value | Type | Default | Notes |
|---|---|---|---|
| `image.repository` | string | `ghcr.io/gnana997/periscope` | OCI repo for the server image. |
| `image.tag` | string | `""` (defaults to `Chart.AppVersion`) | Pin to a specific release tag in production. |
| `image.pullPolicy` | string | `IfNotPresent` | Standard K8s pull policy. |
| `imagePullSecrets` | list | `[]` | `[{name: <secret>}]` entries for private registries. |

## replicaCount

| Value | Type | Default | Notes |
|---|---|---|---|
| `replicaCount` | int | `1` | v1.0 is single-replica only — the in-memory session store is per-pod. HA / multi-replica is a post-1.0 follow-up. |

## serviceAccount / podIdentity

| Value | Type | Default | Notes |
|---|---|---|---|
| `serviceAccount.create` | bool | `true` | When false, set `serviceAccount.name` to an existing SA. |
| `serviceAccount.name` | string | `""` (derived from release name) | The SA the Deployment runs as. |
| `serviceAccount.annotations` | map | `{}` | IRSA path: set `eks.amazonaws.com/role-arn` here. |
| `podIdentity.enabled` | bool | `false` | Pod Identity path (preferred for new EKS). When true, no IRSA annotation is rendered — create the association out-of-band. |

See [`deploy.md`](./deploy.md) for the IRSA vs Pod Identity decision.

## auth

OIDC, session, and authorization settings. Written verbatim into
the rendered `auth.yaml` ConfigMap. See [`auth0.md`](./auth0.md) /
[`okta.md`](./okta.md) for IdP-specific values, and
[`cluster-rbac.md`](./cluster-rbac.md) for authorization mode
trade-offs.

| Value | Type | Default | Notes |
|---|---|---|---|
| `auth.oidc.issuer` | string | `""` | OIDC discovery URL. |
| `auth.oidc.clientID` | string | `""` | OIDC client ID. |
| `auth.oidc.clientSecret` | string | `${OIDC_CLIENT_SECRET}` | Reference into the chosen `secrets.mode`. |
| `auth.oidc.redirectURL` | string | `""` | Periscope's `/api/auth/callback` URL. |
| `auth.oidc.scopes` | list | `[openid, profile, email, offline_access]` | Standard scopes; usually leave as-is. |
| `auth.oidc.audience` | string | `""` | Auth0 only; leave empty for Okta and most IdPs. |
| `auth.oidc.postLogoutRedirect` | string | `""` | URL the browser lands on after IdP logout. |
| `auth.session.cookieName` | string | `periscope_session` | Session cookie name. |
| `auth.session.idleTimeout` | duration | `30m` | Idle timeout before the session is invalidated. |
| `auth.session.absoluteTimeout` | duration | `8h` | Hard cap on session lifetime. |
| `auth.session.cookieDomain` | string | unset | Optional explicit `Domain=` attribute on the cookie. |
| `auth.authorization.mode` | enum | `shared` | `shared` \| `tier` \| `raw`. See RFC 0002 §4 and [`cluster-rbac.md`](./cluster-rbac.md). |
| `auth.authorization.groupTiers` | map | `{}` | tier-mode only: IdP group → tier (`read`\|`triage`\|`write`\|`maintain`\|`admin`). |
| `auth.authorization.defaultTier` | string | `""` | tier-mode only: tier applied when no group matches. `""` = deny. |
| `auth.authorization.groupPrefix` | string | `periscope:` | raw-mode only: prefix prepended to each impersonated group. |
| `auth.authorization.groupsClaim` | string | `groups` | IdP token claim that holds the groups list. Auth0 needs a custom namespaced claim. |
| `auth.authorization.allowedGroups` | list | `[]` | All modes: gate on these IdP groups. Empty = any authenticated user. |
| `auth.authorization.auditAdminGroups` | list | `[]` | IdP groups granted full `/api/audit` visibility across all users. |
| `auth.dev.subject` | string | `dev@local` | Local-dev only; identity used when no OIDC is configured. |
| `auth.dev.email` | string | `dev@local` | Local-dev only. |
| `auth.dev.groups` | list | `[dev]` | Local-dev only. |

## clusters

The cluster registry. Written verbatim into the rendered
`clusters.yaml` ConfigMap. Three backends — see
[`deploy.md`](./deploy.md) and
[`agent-onboarding.md`](./agent-onboarding.md).

| Value | Type | Default | Notes |
|---|---|---|---|
| `clusters` | list | `[]` | Each entry has `name` + `backend` plus backend-specific fields. |

Per-entry fields by backend:

**`backend: eks`** — `name`, `backend: eks`, `region`, `arn`.

**`backend: kubeconfig`** — `name`, `backend: kubeconfig`, `kubeconfigPath`, optional `kubeconfigContext`.

**`backend: in-cluster`** — `name`, `backend: in-cluster`. The chart auto-binds the Periscope SA to the impersonator role.

**`backend: agent`** — `name`, `backend: agent`. No other backend-specific fields; the agent dialing in with a matching mTLS CN supplies the connection.

**Per-cluster overrides** (any backend):

| Field | Type | Notes |
|---|---|---|
| `exec.enabled` | bool | `false` disables exec entirely on this cluster. |
| `exec.serverIdleSeconds` | int | Overrides global idle timeout for this cluster. |
| `exec.idleWarnSeconds` | int | Overrides global idle-warn lead. |
| `exec.heartbeatSeconds` | int | Overrides global heartbeat. |
| `exec.maxSessionsPerUser` | int | Overrides global per-user cap. |
| `exec.maxSessionsTotal` | int | Overrides global per-cluster cap. |
| `environment` | string | Free-form label, surfaced on the fleet card (e.g. `prod`, `staging`). |

## secrets

How the OIDC client secret reaches the pod. Pick exactly one mode.

| Value | Type | Default | Notes |
|---|---|---|---|
| `secrets.mode` | enum | `existing` | `existing` \| `plain` \| `external` \| `native`. |
| `secrets.existing.name` | string | `periscope-oidc` | Name of the pre-applied K8s Secret. |
| `secrets.existing.key` | string | `OIDC_CLIENT_SECRET` | Key in the Secret + env-var name on the pod. |
| `secrets.plain.clientSecret` | string | `""` | Used only when `mode=plain`. Renders a `kind: Secret` from values — fine for kind/minikube, never for prod. |
| `secrets.external.storeName` | string | `""` | External Secrets Operator: SecretStore / ClusterSecretStore name. |
| `secrets.external.storeKind` | enum | `ClusterSecretStore` | Or `SecretStore`. |
| `secrets.external.refreshInterval` | duration | `1h` | Resync interval on the rendered ExternalSecret. |
| `secrets.external.remoteKey` | string | `""` | Upstream secret path / name. |
| `secrets.external.remoteProperty` | string | `""` | JSON key to extract when the upstream value is JSON-shaped. |
| `secrets.native.enabled` | bool | `true` | No K8s Secret at all; Periscope's resolver fetches from AWS Secrets Manager / SSM at startup. Point `auth.oidc.clientSecret` at e.g. `aws-secretsmanager://periscope/oidc#client_secret`. |

## env

| Value | Type | Default | Notes |
|---|---|---|---|
| `env` | list | `[]` | Extra `name: value` pairs. Rarely needed — the chart maps documented `PERISCOPE_*` vars from typed values. |

## service / ingress

| Value | Type | Default | Notes |
|---|---|---|---|
| `service.type` | string | `ClusterIP` | Standard K8s Service type. |
| `service.port` | int | `8080` | Periscope listens on `:8080` inside the pod. |
| `ingress.enabled` | bool | `false` | When true, an Ingress resource is rendered. |
| `ingress.className` | string | `""` | IngressClass name (`nginx`, `alb`, …). |
| `ingress.annotations` | map | `{}` | Controller-specific annotations. |
| `ingress.host` | string | `""` | Hostname the Ingress matches. |
| `ingress.path` | string | `/` | Path to match. |
| `ingress.pathType` | string | `Prefix` | Standard pathType. |
| `ingress.tls.enabled` | bool | `false` | Render a TLS section. |
| `ingress.tls.secretName` | string | `""` | TLS Secret name when enabled. |

## Pod / container specs

| Value | Type | Default | Notes |
|---|---|---|---|
| `resources.requests.cpu` | quantity | `100m` | |
| `resources.requests.memory` | quantity | `128Mi` | |
| `resources.limits.cpu` | quantity | `500m` | |
| `resources.limits.memory` | quantity | `512Mi` | |
| `podAnnotations` | map | `{}` | Extra annotations on the Deployment's pod template. |
| `podSecurityContext.runAsNonRoot` | bool | `true` | |
| `podSecurityContext.runAsUser` | int | `65532` | Distroless `nonroot` UID. |
| `podSecurityContext.runAsGroup` | int | `65532` | |
| `podSecurityContext.fsGroup` | int | `65532` | |
| `podSecurityContext.seccompProfile.type` | string | `RuntimeDefault` | |
| `containerSecurityContext.allowPrivilegeEscalation` | bool | `false` | |
| `containerSecurityContext.readOnlyRootFilesystem` | bool | `true` | |
| `containerSecurityContext.capabilities.drop` | list | `[ALL]` | |
| `nodeSelector` | map | `{}` | Standard. |
| `tolerations` | list | `[]` | Standard. |
| `affinity` | map | `{}` | Standard. |

## audit

Audit log persistence. See [`audit.md`](./audit.md) for retention
sizing and the "stdout always on, SQLite optional" model.

| Value | Type | Default | Notes |
|---|---|---|---|
| `audit.enabled` | bool | `false` | When true, also writes audit events to a local SQLite DB. stdout JSON emission is unconditional. |
| `audit.retentionDays` | int | `30` | Time-based retention. `0` disables time-based pruning. |
| `audit.maxSizeMB` | int | `1024` | Application-level on-disk cap. `0` disables size-based pruning. |
| `audit.vacuumInterval` | duration | `24h` | How often the prune+VACUUM loop runs. |
| `audit.storage.type` | enum | `pvc` | `pvc` (persistent) \| `emptyDir` (ephemeral). |
| `audit.storage.size` | quantity | `5Gi` | PVC request size. Used only when `type=pvc`. |
| `audit.storage.storageClass` | string | `""` | Empty = cluster default StorageClass. |
| `audit.storage.accessMode` | string | `ReadWriteOnce` | v1.0 single-replica needs only RWO. |

## exec

Pod-exec global defaults. Per-cluster overrides go under
`clusters[].exec`. See [`pod-exec.md`](./pod-exec.md) for the
operator guide.

| Value | Type | Default | Notes |
|---|---|---|---|
| `exec.serverIdleSeconds` | int | `600` | Server-side idle timeout. |
| `exec.idleWarnSeconds` | int | `30` | Browser warning lead before the cut. |
| `exec.heartbeatSeconds` | int | `20` | WebSocket heartbeat ping interval. |
| `exec.maxSessionsPerUser` | int | `5` | Concurrent sessions per OIDC subject. |
| `exec.maxSessionsTotal` | int | `50` | Concurrent sessions per cluster. |
| `exec.probeClustersOnBoot` | bool | `false` | Pre-warm IAM creds + exec policy at startup. |

There is intentionally no global `exec.enabled` switch. Disable
exec per-cluster via `clusters[i].exec.enabled: false`.

## watchStreams

SSE live-list configuration. See
[`watch-streams.md`](./watch-streams.md) for the kind registry,
group aliases, and fallback behavior.

| Value | Type | Default | Notes |
|---|---|---|---|
| `watchStreams.kinds` | string | `""` | Empty / `all` / `off` / `none` / comma-separated kinds / group aliases. |
| `watchStreams.perUserLimit` | int | `60` | Per-user concurrent stream cap. `0` disables (not recommended). |

Group aliases: `core`, `workloads`, `networking`, `storage`,
`cluster`. Per-kind tokens: see `watchStreams:` block in
`values.yaml`.

## pdb

| Value | Type | Default | Notes |
|---|---|---|---|
| `pdb.enabled` | bool | `true` | Render a PodDisruptionBudget. |
| `pdb.maxUnavailable` | int | `1` | v1.0 single-replica: `1` allows drains. Switch to `minAvailable` per replica when HA lands post-1.0. |

## networkPolicy

See [`networkpolicy.md`](./networkpolicy.md) for the full recipe.

| Value | Type | Default | Notes |
|---|---|---|---|
| `networkPolicy.enabled` | bool | `false` | Render a NetworkPolicy. |
| `networkPolicy.ingress.fromNamespaces` | list | `[]` | `namespaceSelector` matchLabels for permitted ingress sources. |
| `networkPolicy.ingress.extra` | list | `[]` | Raw `NetworkPolicyIngressRule` entries appended. |
| `networkPolicy.egress.extra` | list | `[]` | Raw `NetworkPolicyEgressRule` entries (e.g. IdP CIDRs, EKS endpoints). DNS to kube-dns is always added. |

## clusterRBAC

Tier-mode RBAC manifests rendered for the central cluster. See
[`cluster-rbac.md`](./cluster-rbac.md).

| Value | Type | Default | Notes |
|---|---|---|---|
| `clusterRBAC.enabled` | bool | `false` | Render the periscope-tier ClusterRoles + bindings. |
| `clusterRBAC.bridgeGroup` | string | `periscope-bridge` | K8s group your EKS Access Entry binds the pod principal to. |

## agent

Server-side agent backend (#42) — opens the mTLS tunnel listener
and the registration endpoints. See
[`agent-onboarding.md`](./agent-onboarding.md) and
[`../architecture/agent-tunnel.md`](../architecture/agent-tunnel.md).

| Value | Type | Default | Notes |
|---|---|---|---|
| `agent.enabled` | bool | `false` | Master switch — opens `:8443`, mounts `/api/agents/*` routes, pre-creates the CA Secret. |
| `agent.listenAddr` | string | `:8443` | Bind address for the mTLS tunnel listener. |
| `agent.tunnelSANs` | string | `localhost` | Comma-separated SANs baked into the server cert agents present on the tunnel. |
| `agent.caSecretName` | string | `periscope-agent-ca` | K8s Secret holding the per-deployment CA. |
| `agent.tunnelService.enabled` | bool | `false` | Render a second Service to expose the tunnel listener externally. |
| `agent.tunnelService.type` | string | `LoadBalancer` | NLB recommended (TLS-passthrough). |
| `agent.tunnelService.port` | int | `8443` | External port for the tunnel Service. |
| `agent.tunnelService.annotations` | map | `{}` | Cloud-LB-specific annotations. |

---

# Agent chart (`periscope-agent`)

Each managed cluster runs ONE periscope-agent Deployment. Most
fields are install-time identity; the rest can stay at defaults.
See [`agent-onboarding.md`](./agent-onboarding.md) for the
topology decision matrix.

## image

| Value | Type | Default | Notes |
|---|---|---|---|
| `image.repository` | string | `ghcr.io/gnana997/periscope-agent` | |
| `image.tag` | string | `""` (defaults to `Chart.AppVersion`) | |
| `image.pullPolicy` | string | `IfNotPresent` | |
| `imagePullSecrets` | list | `[]` | |

## agent

| Value | Type | Default | Notes |
|---|---|---|---|
| `agent.serverURL` | string | `""` | **Required.** WebSocket URL of the central tunnel listener (`wss://...:8443/api/agents/connect`). |
| `agent.clusterName` | string | `""` | **Required.** Must match a `clusters[].name` of `backend: agent` on the server. |
| `agent.registrationToken` | string | `""` | **Required on first install only.** Bootstrap token from `POST /api/agents/tokens`. Single-use, 15-min TTL. |
| `agent.registrationURL` | string | `""` | Set when registration uses a different LB than the tunnel (Topology B — split ALB+NLB). Empty = derive from `serverURL`. |
| `agent.serverCAHash` | string | `""` | SPKI hash (`sha256:...`) for self-signed registration endpoints (Topology C). |
| `agent.stateSecretName` | string | `periscope-agent-state` | Persisted mTLS cert + key + server CA. |
| `agent.healthAddr` | string | `:8081` | Liveness / readiness probe port. |
| `agent.logLevel` | enum | `""` (binary default = `info`) | `debug` \| `info` \| `warn` \| `error`. Set `debug` for per-request access logs. |

## deployment shape

| Value | Type | Default | Notes |
|---|---|---|---|
| `replicaCount` | int | `1` | Tunnel sessions are 1:1 with agent pods. HA needs server-side peer routing (post-1.0). |
| `serviceAccount.create` | bool | `true` | |
| `serviceAccount.name` | string | `""` | |
| `serviceAccount.annotations` | map | `{}` | |

## RBAC

| Value | Type | Default | Notes |
|---|---|---|---|
| `clusterRole.enabled` | bool | `true` | Bind the agent SA to a ClusterRole granting `get/list/watch` on every kind + `impersonate` on users/groups/SAs. |
| `clusterRBAC.enabled` | bool | `true` | Install tier ClusterRoleBindings on the managed cluster (`periscope-tier-read/write/admin/triage/maintain`). Required for impersonation to actually authorize. |

## resources / security context

Mirrors the server chart's defaults; same fields, just with
agent-appropriate request/limit values.

| Value | Type | Default |
|---|---|---|
| `resources.requests.cpu` | quantity | `50m` |
| `resources.requests.memory` | quantity | `64Mi` |
| `resources.limits.cpu` | quantity | `500m` |
| `resources.limits.memory` | quantity | `256Mi` |
| `podSecurityContext.runAsNonRoot` | bool | `true` |
| `podSecurityContext.runAsUser` | int | `65532` |
| `podSecurityContext.runAsGroup` | int | `65532` |
| `podSecurityContext.seccompProfile.type` | string | `RuntimeDefault` |
| `containerSecurityContext.allowPrivilegeEscalation` | bool | `false` |
| `containerSecurityContext.readOnlyRootFilesystem` | bool | `true` |
| `containerSecurityContext.capabilities.drop` | list | `[ALL]` |
| `nodeSelector` | map | `{}` |
| `tolerations` | list | `[]` |
| `affinity` | map | `{}` |
| `podAnnotations` | map | `{}` |
| `env` | list | `[]` |

---

## See also

- [`environment-variables.md`](./environment-variables.md) — `PERISCOPE_*` env vars the binary reads (one-to-one mapping with most typed values above).
- [`deploy.md`](./deploy.md) — full install walkthrough.
- [`agent-onboarding.md`](./agent-onboarding.md) — multi-cluster agent install with topology decision matrix.
