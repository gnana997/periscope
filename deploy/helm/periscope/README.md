# Periscope Helm chart

Deploy Periscope on any Kubernetes cluster (EKS preferred for the
keyless-auth pitch). See `docs/setup/deploy.md` for the full
walkthrough and `docs/setup/{auth0,okta}.md` for the IdP setup
that produces the values you'll paste into `auth:` below.

## Quickstart

```sh
helm install periscope ./deploy/helm/periscope \
  --namespace periscope --create-namespace \
  --values my-values.yaml
```

Where `my-values.yaml` contains, at minimum:

```yaml
auth:
  oidc:
    issuer: https://your-tenant.us.auth0.com/
    clientID: your-client-id
    redirectURL: https://periscope.your-corp.com/api/auth/callback
    postLogoutRedirect: https://periscope.your-corp.com/api/auth/loggedout
    audience: ""
  authorization:
    groupsClaim: https://periscope/groups
    allowedGroups: [periscope-users]

clusters:
  - name: prod-eu-west-1
    backend: eks
    region: eu-west-1
    arn: arn:aws:eks:eu-west-1:222222222222:cluster/prod-eu-west-1

serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::111111111111:role/periscope-base

secrets:
  mode: existing
  existing:
    name: periscope-oidc

ingress:
  enabled: true
  className: alb            # whichever your cluster uses
  host: periscope.your-corp.com
  tls:
    enabled: true
    secretName: periscope-tls
```

Then apply the Secret out-of-band (since `secrets.mode=existing`):

```sh
kubectl -n periscope create secret generic periscope-oidc \
  --from-literal=OIDC_CLIENT_SECRET='<the client secret from your IdP>'
```

## Secret modes

| `secrets.mode` | What the chart renders | When to use |
|---|---|---|
| `existing` (default) | nothing | You bring your own K8s Secret (GitOps-managed, SealedSecrets, SOPS, etc.) |
| `plain` | a `kind: Secret` with `stringData` from values | Demos, kind/minikube, never prod |
| `external` | an `external-secrets.io/v1` `ExternalSecret` | You run External Secrets Operator with a `(Cluster)SecretStore` already configured |
| `native` | nothing — no K8s Secret at all | Periscope's resolver pulls from AWS Secrets Manager / SSM at startup. Requires `auth.oidc.clientSecret` set to e.g. `aws-secretsmanager://...` |

Per-mode value blocks live under `secrets.{mode}` in `values.yaml`.

## Pod Identity vs IRSA

The chart defaults to a plain ServiceAccount. Pick one path:

- **Pod Identity** (preferred for new EKS): set `podIdentity.enabled=true`
  and run the `aws eks create-pod-identity-association` command shown in
  `helm install` post-install notes. No SA annotation; cleaner across
  clusters.
- **IRSA**: set `serviceAccount.annotations` to include
  `eks.amazonaws.com/role-arn: arn:aws:iam::<acct>:role/periscope-base`.
  Works on EKS or self-managed K8s with an OIDC provider.

The Periscope code is identical for both — same default credential chain.

## Pod exec

Pod exec is enabled on every cluster by default. Tune the global
defaults under `exec:` (idle/heartbeat/cap settings) and override
per-cluster under `clusters[].exec:` (partial overrides are fine —
omitted fields fall back to the global default). Set
`clusters[<i>].exec.enabled: false` to opt a specific cluster out.

See `docs/setup/pod-exec.md` for the operator guide and RFC 0001 for
the design.

## Helm release browser

The chart deploys a read-only Helm release browser as part of the
Periscope binary — it surfaces under `/api/clusters/{cluster}/helm/...`
and the SPA's "Helm" sidebar group. No additional values; the
impersonated user's RBAC governs visibility (the browser needs `get`
on `secrets` in release namespaces, which is the default storage
driver). See `docs/setup/helm-releases.md`.

## Values reference

See `values.yaml` for the full surface with inline comments.

## Upgrade notes

- `auth.yaml` and `clusters.yaml` are mounted from ConfigMaps. Pod
  annotations carry `checksum/auth` and `checksum/clusters` so values
  changes automatically roll the Deployment.
- The Deployment uses `strategy: Recreate` because the in-memory
  session store is per-replica; rolling-update overlap would orphan
  sessions on the outgoing pod.
- To bump the image: set `image.tag` (defaults to `.Chart.AppVersion`).
