# Deploying Periscope on Kubernetes

The supported deploy artifact is the Helm chart at
[`deploy/helm/periscope/`](../../deploy/helm/periscope/). This guide
walks through the install, the choices you'll make on the way, and how
to verify the result.

For IdP setup that produces the `auth.*` values, see
[`docs/setup/auth0.md`](./auth0.md) or [`docs/setup/okta.md`](./okta.md).

---

## 1. Prerequisites

- A Kubernetes cluster (1.27+). EKS preferred — the keyless-auth path
  uses Pod Identity / IRSA — but any K8s cluster works for the OIDC
  side.
- `helm` 3.x (or 4.x).
- `kubectl` configured for the target cluster.
- For EKS: AWS CLI to set up Pod Identity associations or IAM roles.
- IdP tenant configured per `docs/setup/{auth0,okta}.md`.

---

## 2. Quickstart

```sh
# 1. Pull (or vendor) the chart
git clone https://github.com/gnana997/periscope
cd periscope

# 2. Write a values file (see 3)
cp examples/config/auth.yaml.auth0 my-auth.yaml   # for reference
$EDITOR my-values.yaml                             # paste in & adapt

# 3. Apply your OIDC client secret (default secrets.mode=existing)
kubectl create namespace periscope
kubectl -n periscope create secret generic periscope-oidc \
  --from-literal=OIDC_CLIENT_SECRET='<the-secret-from-your-IdP>'

# 4. Install
helm install periscope ./deploy/helm/periscope \
  --namespace periscope \
  --values my-values.yaml

# 5. Reach it
kubectl -n periscope port-forward svc/periscope 8080:8080
open http://localhost:8080/
```

---

## 3. Minimum values file

Paste this into `my-values.yaml` and edit:

```yaml
auth:
  oidc:
    issuer: https://your-tenant.us.auth0.com/        # or https://your-org.okta.com/oauth2/default
    clientID: <your-client-id>
    redirectURL: https://periscope.your-corp.com/api/auth/callback
    postLogoutRedirect: https://periscope.your-corp.com/api/auth/loggedout
    audience: ""                                      # Auth0 only; "" for Okta
  authorization:
    groupsClaim: https://periscope/groups             # Auth0; "groups" for Okta
    allowedGroups: [periscope-users]

clusters:
  - name: prod-eu-west-1
    backend: eks
    region: eu-west-1
    arn: arn:aws:eks:eu-west-1:222222222222:cluster/prod-eu-west-1

# Pick one: see 5
secrets:
  mode: existing
  existing:
    name: periscope-oidc

# Pick one: see 4
podIdentity:
  enabled: true        # set to false and use the IRSA path instead
# or:
# serviceAccount:
#   annotations:
#     eks.amazonaws.com/role-arn: arn:aws:iam::111111111111:role/periscope-base

ingress:
  enabled: true
  className: alb       # or nginx / etc.
  host: periscope.your-corp.com
  tls:
    enabled: true
    secretName: periscope-tls
```

---

## 4. AWS auth: Pod Identity vs IRSA

**Pod Identity (recommended for new EKS).** No SA annotation. Run once
after the chart is installed:

```sh
aws eks create-pod-identity-association \
  --cluster-name <hosting-cluster> \
  --namespace periscope \
  --service-account periscope \
  --role-arn arn:aws:iam::111111111111:role/periscope-base
```

The role's trust policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "Service": "pods.eks.amazonaws.com" },
    "Action": ["sts:AssumeRole", "sts:TagSession"]
  }]
}
```

Set `podIdentity.enabled=true` in values.

**IRSA (fallback / non-EKS / older clusters).** Annotate the SA:

```yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::111111111111:role/periscope-base
```

The role's trust policy uses the cluster's OIDC provider:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "Federated": "arn:aws:iam::111111111111:oidc-provider/<oidc-issuer>" },
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "<oidc-issuer>:sub": "system:serviceaccount:periscope:periscope",
        "<oidc-issuer>:aud": "sts.amazonaws.com"
      }
    }
  }]
}
```

Periscope code is identical for both. Pick whichever your platform team
already runs.

---

## 5. Secret modes

Pick the row that matches how you already manage secrets in the cluster.

### `existing` — default

You apply the K8s Secret out-of-band; the chart references it. Any
GitOps tool (ArgoCD ApplicationSet, SealedSecrets, SOPS, etc.) can
manage the Secret independently.

```yaml
secrets:
  mode: existing
  existing:
    name: periscope-oidc
    key: OIDC_CLIENT_SECRET    # default
```

```sh
kubectl -n periscope create secret generic periscope-oidc \
  --from-literal=OIDC_CLIENT_SECRET='<your client secret>'
```

### `plain` — quick start / demo only

Chart renders a `kind: Secret` with `stringData` from your values.
Secret value lives in your values file; never check that file in.

```yaml
secrets:
  mode: plain
  plain:
    clientSecret: <your client secret>
```

### `external` — External Secrets Operator

You run [External Secrets Operator](https://external-secrets.io/) with
a `ClusterSecretStore` already pointed at AWS Secrets Manager / SSM /
Vault. Chart renders an `ExternalSecret` (api `external-secrets.io/v1`)
that ESO syncs into a K8s Secret; the Deployment reads from there.

```yaml
secrets:
  mode: external
  external:
    storeName: aws-secretsmanager-prod
    storeKind: ClusterSecretStore         # or SecretStore
    refreshInterval: 1h
    remoteKey: prod/periscope/oidc        # the upstream secret name
    remoteProperty: client_secret         # if the upstream is JSON-shaped; "" otherwise
```

### `native` — no K8s Secret at all

Periscope's resolver fetches the secret directly at startup using the
pod's Pod Identity / IRSA credentials. There's no K8s Secret artifact
in the cluster. Set `auth.oidc.clientSecret` to a scheme URL:

```yaml
secrets:
  mode: native
auth:
  oidc:
    clientSecret: aws-secretsmanager://prod/periscope/oidc#client_secret
    # or: aws-ssm:///prod/periscope/oidc-client-secret
```

The pod's IAM role needs:
- `secretsmanager:GetSecretValue` on the specific secret ARN, or
- `ssm:GetParameter` (with `WithDecryption=true`) plus `kms:Decrypt` on
  the key

This is the lowest-trust mode — there's no plaintext secret stored in
etcd. Rotation = restart in v1; auto-refresh is a v1.x concern.

---

## 6. Ingress / TLS

The chart renders a vanilla `networking.k8s.io/v1` Ingress when
`ingress.enabled=true`. Class and annotations are passthrough so it
works with whichever controller your cluster runs (ALB, NGINX, Traefik,
Istio Gateway via ingress-class adapter, …).

```yaml
ingress:
  enabled: true
  className: alb
  annotations:
    alb.ingress.kubernetes.io/scheme: internal
    alb.ingress.kubernetes.io/target-type: ip
    alb.ingress.kubernetes.io/listen-ports: '[{"HTTPS":443}]'
  host: periscope.your-corp.com
  path: /
  pathType: Prefix
  tls:
    enabled: true
    secretName: periscope-tls   # cert-manager Certificate target, or whatever your cert pipeline produces
```

The IdP's allowed callback URL must be `https://<host>/api/auth/callback`
exactly. The redirectURL in your auth values must match.

---

## 7. Verify

After `helm install`:

```sh
# Pod is healthy
kubectl -n periscope rollout status deploy/periscope

# /healthz inside the pod
kubectl -n periscope exec deploy/periscope -- wget -qO- http://localhost:8080/healthz
# expects: ok

# /api/auth/whoami requires a session — first hit returns 401, that's correct
kubectl -n periscope port-forward svc/periscope 8080:8080
curl -i http://localhost:8080/api/auth/whoami
# expects: HTTP/1.1 401

# Open the dashboard, click "sign in with okta" (label is generic),
# complete the IdP flow, end up at /. Click your avatar — popover
# should show your email and an `oidc` badge.
open http://localhost:8080/
```

For EKS, also confirm the AssumeRole hops in CloudTrail:

```sh
aws cloudtrail lookup-events \
  --lookup-attributes AttributeKey=EventName,AttributeValue=AssumeRole \
  --max-results 5
# Look for RoleSessionName=periscope/<oidc-sub>
```

---

## 8. Upgrades

```sh
helm upgrade periscope ./deploy/helm/periscope \
  --namespace periscope \
  --values my-values.yaml
```

Notes:
- `auth.yaml` and `clusters.yaml` are mounted from ConfigMaps. The
  Deployment carries `checksum/auth` and `checksum/clusters`
  annotations so values changes auto-roll the pods.
- `strategy: Recreate` (not RollingUpdate) — the in-memory session
  store is per-replica; rolling-update overlap would orphan sessions
  on the outgoing pod. Users see a one-second blip during upgrades.
- HA / multi-replica session store is a v1.x concern.

---

## 9. Common pitfalls

- **`401 unauthenticated` everywhere after install.** That's the
  correct unauthenticated state — the SPA shows the LoginScreen at
  `/` and the IdP flow takes it from there. If the LoginScreen
  doesn't appear, check your Ingress is sending requests to the
  Service (`kubectl -n periscope describe ingress periscope`).
- **`CrashLoopBackOff` immediately on install.** Almost always a
  missing Secret in `existing` mode. Check
  `kubectl -n periscope describe pod` for `secret … not found`.
- **Login bounces forever.** Your IdP's allowed callback URL doesn't
  match `auth.oidc.redirectURL` exactly (trailing slashes, scheme,
  port). Both the IdP and `auth.yaml` must agree.
- **`AssumeRole denied`** when adding a cluster. The per-cluster role's
  trust policy doesn't allow `periscope-base` to assume it, or the EKS
  Access Entry isn't in place. The per-cluster RBAC walkthrough lives
  in [`docs/setup/cluster-rbac.md`](./cluster-rbac.md).

---

## 10. Feature configuration

The minimum values file in 3 only covers auth, clusters, and
secrets. The chart also exposes pod exec, audit, NetworkPolicy, and
PDB knobs — each has its own dedicated guide, summarised here.

### 10.1 Pod exec

Pod exec is on by default for every cluster. Tune the global
defaults under `exec:` (idle/heartbeat/cap settings) and override
per-cluster under `clusters[].exec:`. To disable exec on a specific
cluster, set `clusters[<i>].exec.enabled: false`. There is no global
"off" switch by design.

```yaml
exec:
  serverIdleSeconds: 600       # 10 min
  maxSessionsPerUser: 5
  probeClustersOnBoot: false   # pre-warm cold clusters

clusters:
  - name: prod
    backend: eks
    arn: arn:aws:eks:us-east-1:111:cluster/prod
    exec:
      serverIdleSeconds: 1800  # 30 min for prod debugging
```

Full operator guide: [`docs/setup/pod-exec.md`](./pod-exec.md).

### 10.2 Audit log persistence

Off by default — events go to stdout (one JSON line per privileged
action). Turn on persistence to enable the in-app audit query view
at `GET /api/audit`:

```yaml
audit:
  enabled: true
  retentionDays: 30
  maxSizeMB: 1024
  storage:
    type: pvc        # or emptyDir for kind/minikube
    size: 5Gi
```

Full operator guide: [`docs/setup/audit.md`](./audit.md).

### 10.3 Helm release browser

The chart deploys a read-only Helm release browser. No values to set
— the SPA shows it under each cluster's "Helm" sidebar group. RBAC
follows the impersonated user (the browser auto-detects whether the
cluster uses the `secret` or `configmap` storage driver).

Full guide: [`docs/setup/helm-releases.md`](./helm-releases.md).

### 10.4 Multi-cluster fleet view

`GET /api/fleet` aggregates per-cluster status (nodes ready, pods by
phase, hot signals) across every registered cluster. The home page
uses it to render a fleet card grid. The endpoint runs each cluster
under the user's impersonation in parallel; per-cluster failures
become per-card error states without breaking the whole page.

There is no helm config for the fleet endpoint — it's automatic once
clusters are registered.

### 10.5 Real-time list updates (watch streams)

Periscope's pod, event, replicaset, and job list pages update in
real time via SSE. **All four kinds are on by default**; the SPA
falls back to polling when the EventSource fails. The `watchStreams:`
helm block lets operators opt out (e.g. behind a proxy that
mishandles long-lived connections) or restrict to a subset:

```yaml
watchStreams:
  kinds: ""           # default ("all on"); "off" / "none" disable; "pods,events" restricts
  perUserLimit: 30    # concurrent SSE streams per OIDC subject
```

Full operator guide:
[`docs/setup/watch-streams.md`](./watch-streams.md). Contributor /
architecture view (the `watchKind[T,S]` primitive, how to add a
kind):
[`docs/architecture/watch-streams.md`](../architecture/watch-streams.md).

### 10.6 NetworkPolicy

Off by default. Every cluster has different ingress controller
plumbing and IdP egress targets, so a templated default would either
be too loose or too tight to use anywhere. Enable when you know your
environment:

```yaml
networkPolicy:
  enabled: true
  ingress:
    fromNamespaces:
      - kubernetes.io/metadata.name: ingress-nginx
```

Full guide: [`docs/setup/networkpolicy.md`](./networkpolicy.md).

### 10.7 Pod Disruption Budget

On by default with `maxUnavailable: 1` (single-replica v1 topology;
the PDB makes the drain semantics explicit in cluster audit). Set
`pdb.enabled: false` to skip. When HA support lands in v1.x, switch
to `minAvailable` per `replicaCount`.
