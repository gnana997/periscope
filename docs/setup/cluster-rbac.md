# Per-cluster K8s RBAC for Periscope

Periscope's K8s authorization is operator-selectable. Pick a mode in
`auth.yaml: authorization.mode`, set up a tiny amount of per-cluster
RBAC, and you're done.

| Mode | What users get | Operator effort |
|---|---|---|
| `shared` (default) | Identical permissions for everyone — whatever the pod role is bound to. | One Access Entry per cluster. |
| `tier` | Five built-in tiers (read/triage/write/maintain/admin) mapped from your existing IdP groups. | Apply 7 shipped manifests per cluster + ~5 lines of config. |
| `raw` | Pass-through impersonation: each user's actual IdP groups, prefixed. | Full RBAC YAML per cluster (CLI tool ships in PR-B.2). |

This guide walks each mode end-to-end.

---

## Background: how impersonation works

In `tier` and `raw` modes, Periscope's pod authenticates to each cluster
with **only** the `impersonate` verb. Every user request rides
Impersonate-User and Impersonate-Group HTTP headers, and the apiserver
re-evaluates RBAC under the impersonated identity. K8s audit log
records:

```
user.username   = auth0|alice                       # the OIDC sub
user.groups     = ["periscope-tier:write"]          # the resolved tier (or :raw groups)
impersonatedBy.username = system:node:periscope-bridge   # Periscope's principal
```

Two non-negotiables:

1. **Periscope's pod role gets ONLY `impersonate`** on each cluster.
   No other K8s perms. This is defense-in-depth: a compromised Periscope
   can act as ANY user, but cannot itself read secrets, exec into pods,
   or anything else without an impersonation step.
2. **Impersonated groups are always prefixed** (`periscope-tier:` or
   `periscope:`). RBAC bindings reference the prefixed names. An attacker
   who compromises Periscope cannot impersonate into `system:masters` or
   any other un-prefixed privileged group — bindings on those won't match.

---

## Mode 1: `shared` (default)

No impersonation. Every user has whatever K8s permissions Periscope's
pod role has. Best for:

- Solo / small teams (everyone is admin)
- POC and demo deployments
- Lab clusters where RBAC friction isn't worth it
- Migration period: install in `shared` first, move to `tier` once you
  outgrow it

### Setup

1. **Access Entry** on each cluster, mapping Periscope's pod principal
   to a K8s group:

   ```sh
   aws eks create-access-entry \
     --cluster-name prod-eu-west-1 \
     --principal-arn arn:aws:iam::222222222222:role/periscope-base \
     --kubernetes-groups periscope-bridge \
     --type STANDARD
   ```

2. **Bind the bridge group** to whatever K8s ClusterRole you want
   everyone to have:

   ```yaml
   apiVersion: rbac.authorization.k8s.io/v1
   kind: ClusterRoleBinding
   metadata:
     name: periscope-shared-cluster-admin
   roleRef:
     apiGroup: rbac.authorization.k8s.io
     kind: ClusterRole
     name: cluster-admin     # or `view`, `edit`, etc. — your call
   subjects:
     - kind: Group
       name: periscope-bridge
       apiGroup: rbac.authorization.k8s.io
   ```

3. In `values.yaml`:

   ```yaml
   auth:
     authorization:
       mode: shared
   ```

That's it. Every authenticated Periscope user can do whatever the bound
ClusterRole allows.

### Caveat — audit attribution

In shared mode, the K8s audit log shows `user.username =
system:node:periscope-bridge` — the pod's principal, not the user. The
*application* audit log (`auth.login`, etc.) still attributes by OIDC
sub, but if "who deleted that pod?" reaches K8s, you can't tell from
the K8s side alone. This is the cost of zero-RBAC-config; if attribution
matters, use `tier` or `raw`.

> **See also — audit visibility in the dashboard.** Periscope itself
> records every privileged action through its own audit pipeline,
> attributed by OIDC sub regardless of K8s impersonation mode. The
> read-side endpoint (`/api/audit`) has its own RBAC: by default users
> see only their own actions. To grant security or SRE teams full
> visibility, set `auth.authorization.auditAdminGroups`. The full
> resolution order across all three authz modes — including why raw
> mode requires the explicit grant and shared mode falls back to
> `allowedGroups` — is documented in
> [docs/setup/audit.md](audit.md). The audit-admin story is decoupled
> from K8s admin on purpose: security teams who can read history
> shouldn't need to mutate prod.

---

## Mode 2: `tier` (recommended once you've outgrown shared)

Five built-in tiers; map your existing IdP groups to one of them.

### Tier definitions

| Tier | K8s mapping | What it does |
|---|---|---|
| `read` | `view` (built-in) | Read everything except secrets. |
| `triage` | shipped `periscope-triage` | Read + debug verbs (exec, logs, port-forward, restart pods, scale workloads). No spec edits. |
| `write` | `edit` (built-in) | Modify all namespaced resources except RBAC. |
| `maintain` | shipped `periscope-maintain` | `admin` (namespaced incl. RoleBindings) + cluster-scoped reads. No cluster-level RBAC create. |
| `admin` | `cluster-admin` (built-in) | Everything. |

The `triage` and `maintain` ClusterRoles ship in the chart with
sensible default verb sets; verb sets evolve in v1.x as we learn from
real use. `kubectl edit clusterrole periscope-triage` to tighten or
broaden per cluster.

### Setup

1. **Access Entry** on each cluster, same as shared mode (bridge
   group). The pod principal needs only the `impersonate` verb on
   each cluster — the chart's `cluster-rbac.yaml` template gives it
   exactly that.

2. **Apply the tier RBAC** to each managed cluster. Render from the
   chart with your values, then `kubectl apply`:

   ```sh
   helm template periscope ./deploy/helm/periscope \
     --values my-values.yaml \
     --set clusterRBAC.enabled=true \
     --show-only templates/cluster-rbac.yaml \
     | kubectl --context prod-eu-west-1 apply -f -

   # Repeat per managed cluster (or wrap in a loop / GitOps).
   ```

   This applies 7 manifests:
   - `ClusterRole/periscope-impersonator` — the impersonate verb
   - `ClusterRoleBinding/periscope-impersonator` → bridge group
   - `ClusterRoleBinding/periscope-tier-read` → `view`
   - `ClusterRoleBinding/periscope-tier-write` → `edit`
   - `ClusterRoleBinding/periscope-tier-admin` → `cluster-admin`
   - `ClusterRole/periscope-triage` + `ClusterRoleBinding/periscope-tier-triage`
   - `ClusterRole/periscope-maintain` + `ClusterRoleBinding/periscope-tier-maintain`

3. **Map IdP groups to tiers** in `values.yaml`:

   ```yaml
   auth:
     authorization:
       mode: tier
       groupTiers:
         SRE-Platform:        admin
         SRE-OnCall:          triage
         Backend-TeamLeads:   maintain
         Engineering-All:     write
         Contractors:         read
       defaultTier: ""        # "" = users in no listed group are denied
   ```

   You don't need to create new IdP groups for this — reuse whatever
   exists. If your Okta org has an "SRE-Platform" group, map it. The
   group string in `groupTiers` keys is exactly what your IdP emits in
   the `groupsClaim` token claim.

   When a user is in multiple matching groups, the **highest-privilege
   tier wins** (admin > maintain > write > triage > read).

4. (Optional) **Tighten the custom ClusterRoles** if the shipped defaults
   don't match your cluster's needs:

   ```sh
   kubectl --context prod-eu-west-1 edit clusterrole periscope-triage
   ```

   Drift between shipped roles (chart `appVersion`) and the cluster is
   the operator's responsibility. The chart's NOTES.txt prints the
   shipped-role version on `helm install` so you can pin and re-apply
   on chart upgrade.

### Verifying tier mode works

```sh
# After login, /api/auth/whoami should report your tier:
curl -b cookies.txt https://periscope.your-corp.com/api/auth/whoami
# {"subject":"auth0|...","email":"...","groups":[...],
#  "mode":"tier","tier":"admin","expiresAt":...}
```

In the SPA, the user-menu popover shows a tier badge (`admin`,
`triage`, etc.) so users can see at a glance what they can do.

K8s audit log on the target cluster:

```
user.username = auth0|alice
user.groups = ["periscope-tier:admin"]
impersonatedBy.username = system:node:periscope-bridge
```

That last line is the per-user attribution payoff: every K8s action
traceable to a real human.

---

## Mode 3: `raw` (full flexibility, full operator effort)

Periscope passes the user's actual IdP groups through (prefixed). You
write all RBAC bindings against those prefixed group names. Use when
you need:

- Per-namespace differentiation (admin in dev namespace, viewer in prod)
- Per-CRD scoping (Postgres team admins their `pgclusters/*`, readonly elsewhere)
- Org-specific roles that don't fit the 5 tiers

### Setup

1. **Access Entry**: same as the other modes — bridge group on each
   cluster.

2. **Apply the impersonator binding** (same as tier mode's step 2,
   minus the tier ClusterRoleBindings — those are unused in raw mode):

   ```yaml
   apiVersion: rbac.authorization.k8s.io/v1
   kind: ClusterRole
   metadata:
     name: periscope-impersonator
   rules:
     - apiGroups: [""]
       resources: ["users", "groups"]
       verbs: ["impersonate"]
   ---
   apiVersion: rbac.authorization.k8s.io/v1
   kind: ClusterRoleBinding
   metadata:
     name: periscope-impersonator
   roleRef:
     apiGroup: rbac.authorization.k8s.io
     kind: ClusterRole
     name: periscope-impersonator
   subjects:
     - kind: Group
       name: periscope-bridge
       apiGroup: rbac.authorization.k8s.io
   ```

3. **Configure raw mode** in `values.yaml`:

   ```yaml
   auth:
     authorization:
       mode: raw
       groupPrefix: "periscope:"      # default
   ```

4. **Write RBAC bindings** referencing `periscope:<group-name>` for
   each IdP group you want to grant something:

   ```yaml
   # SRE-Platform → cluster-admin everywhere
   ---
   apiVersion: rbac.authorization.k8s.io/v1
   kind: ClusterRoleBinding
   metadata:
     name: periscope-sre-platform
   roleRef:
     apiGroup: rbac.authorization.k8s.io
     kind: ClusterRole
     name: cluster-admin
   subjects:
     - kind: Group
       name: periscope:SRE-Platform
       apiGroup: rbac.authorization.k8s.io

   # Backend-Devs → admin in payment, checkout, ledger namespaces only
   ---
   apiVersion: rbac.authorization.k8s.io/v1
   kind: RoleBinding
   metadata:
     name: periscope-backend-devs
     namespace: payments
   roleRef:
     apiGroup: rbac.authorization.k8s.io
     kind: ClusterRole
     name: admin
   subjects:
     - kind: Group
       name: periscope:Backend-Devs
       apiGroup: rbac.authorization.k8s.io
   # ... repeat for checkout, ledger
   ```

5. **(Coming in PR-B.2)** The `periscope-rbac` CLI tool generates these
   bindings from a declarative intent file. Until then, hand-write or
   templatize.

---

## Choosing between the modes

```
Start here:
  Are you a small team (<5 users) where everyone is effectively admin?
    YES → shared mode. You're done.
    NO  → continue.

  Do your permission needs fit "viewer / debugger / developer / lead / admin"?
    YES → tier mode. Map IdP groups, apply 7 manifests per cluster, go.
    NO  → raw mode. Wait for PR-B.2's CLI tool, or hand-write RBAC YAML.
```

You can flip modes any time by editing `values.yaml` and
`helm upgrade`-ing. Migrating from `tier` to `raw` requires rewriting
your bindings to use `periscope:<group>` instead of `periscope-tier:<tier>`,
but the rest of the deployment is unchanged.

---

## Common pitfalls

- **Tier mode user gets 403 on everything.** Either `defaultTier: ""`
  is denying them (they're in no listed group), or the chart's
  `cluster-rbac.yaml` was never applied to the cluster they're hitting.
  Check `kubectl --context <cluster> get clusterrolebinding | grep periscope-tier`.

- **K8s audit log doesn't show impersonation.** You're in `shared` mode,
  or your chart's `clusterRBAC.enabled` is false. Enable both.

- **403 specifically on `pods/exec` in triage tier.** Make sure the
  shipped `periscope-triage` ClusterRole has `pods/exec → create` (it
  does by default; verify nothing edited it out).

- **Group prefix mismatch.** RBAC binding references
  `periscope:engineers` but Periscope is sending `periscope-tier:write`.
  You're in tier mode but wrote a raw-style binding. Either flip mode
  or rewrite the binding.

- **Drift after chart upgrade.** Chart bumped `periscope-triage` to add
  a verb; your cluster still has the old version. Re-render and apply:
  `helm template ... --show-only templates/cluster-rbac.yaml | kubectl apply -f -`.

---

## Helm release browser RBAC

Periscope ships a read-only Helm release browser
([`docs/setup/helm-releases.md`](./helm-releases.md)). Releases live
in K8s storage objects — Secrets by default, ConfigMaps as a
fallback — under the impersonated user's identity. To see releases
in the SPA, the user's resolved K8s identity needs `get` and `list`
on whichever storage kind the cluster uses, scoped to the namespaces
where releases live (or cluster-wide for the auto-probe to populate
the SPA's "all releases" list).

The shipped tier ClusterRoles cover the common cases:

| Tier | Secret-driver releases | ConfigMap-driver releases |
|---|---|---|
| `read` (`view`) | ❌ — `view` excludes Secret reads | ✅ — `view` covers ConfigMaps |
| `triage` (custom) | ❌ — no secrets verbs | ✅ — covers ConfigMaps |
| `write` (`edit`) | ✅ | ✅ |
| `maintain` (custom) | ✅ | ✅ |
| `admin` (`cluster-admin`) | ✅ | ✅ |

If you want **read-tier users** to see secret-driver releases (the
common case — Helm 3 defaults to Secrets), apply this binding to
each managed cluster:

```yaml
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: periscope-helm-browser
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: []                # bound by label selector via the API at read time
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: periscope-helm-browser-read
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: periscope-helm-browser
subjects:
  - kind: Group
    name: periscope-tier:read
    apiGroup: rbac.authorization.k8s.io
```

This is **opt-in** because granting `secrets: list` cluster-wide is a
significant escalation from `view`'s defaults — `view` deliberately
excludes Secret reads to keep "read-only" from leaking credentials.
Only add the binding if your team's threat model accepts that
read-tier users will see Helm release storage payloads (which
include the chart's rendered manifest and merged values, but not
arbitrary cluster secrets).

For tighter scoping, narrow the binding to a `RoleBinding` in
specific namespaces — the helm browser then shows only the releases
in those namespaces for that tier.

---

## Appendix: tier ClusterRole verbs

The chart's `templates/cluster-rbac.yaml` ships the `triage` and
`maintain` ClusterRoles inline. The other three tiers (`read`,
`write`, `admin`) bind to the K8s built-ins `view`, `edit`, and
`cluster-admin` — refer to the upstream K8s docs for those.

This appendix documents what's in the shipped custom roles so you
can audit or customize them without reading template YAML. If the
chart bumps these roles, this appendix should be updated alongside.

### `periscope-triage`

**Reads (mirrors `view`):**

| API group | Resources |
|---|---|
| `""` (core) | `bindings`, `configmaps`, `endpoints`, `events`, `limitranges`, `namespaces` (+`/status`), `persistentvolumeclaims` (+`/status`), `pods` (+`/log`, `/status`), `replicationcontrollers` (+`/scale`, `/status`), `resourcequotas` (+`/status`), `serviceaccounts`, `services` (+`/status`) |
| `apps` | `controllerrevisions`, `daemonsets` (+`/status`), `deployments` (+`/scale`, `/status`), `replicasets` (+`/scale`, `/status`), `statefulsets` (+`/scale`, `/status`) |
| `batch` | `cronjobs` (+`/status`), `jobs` (+`/status`) |
| `networking.k8s.io` | `ingresses` (+`/status`), `networkpolicies` |
| `autoscaling` | `horizontalpodautoscalers` (+`/status`) |
| `policy` | `poddisruptionbudgets` (+`/status`) |
| `discovery.k8s.io` | `endpointslices` |

Verbs: `get`, `list`, `watch`.

**Debug verbs (the triage-specific gap-filler):**

| Resource | Verb | Purpose |
|---|---|---|
| `pods/exec` | `create` | Open Shell |
| `pods/portforward` | `create` | Port-forward UI |
| `pods/eviction` | `create` | Evict a stuck pod |
| `pods` | `delete` | Restart pod (controller recreates) |
| `apps/deployments/scale`, `apps/statefulsets/scale`, `apps/replicasets/scale` | `update`, `patch` | Scale workloads |
| `apps/deployments`, `apps/statefulsets`, `apps/daemonsets` | `patch` | Rollout restart (annotation bump) |

**What's NOT in triage:** spec edits (no `pods: patch` for the spec
itself, no full `deployments: update`), Secret reads, RBAC, anything
cluster-scoped except via the inherited `view` rules. Triage is
"diagnose + nudge", not "redeploy".

### `periscope-maintain`

**Reads:** `*` on `*` for `get`, `list`, `watch` (everything
readable). Cluster-scoped reads explicitly: `nodes` (+`/status`),
`namespaces`, `persistentvolumes`, storage classes, CSI drivers/nodes,
volume attachments, priority classes.

**Mutate (namespaced):**

| API group | Resources | Verbs |
|---|---|---|
| `""` (core) | `configmaps`, `events`, `persistentvolumeclaims`, `pods` (+`/exec`, `/log`, `/portforward`), `replicationcontrollers` (+`/scale`), `secrets`, `serviceaccounts`, `services` | `*` |
| `apps` | `*` | `*` |
| `batch` | `*` | `*` |
| `networking.k8s.io` | `*` | `*` |
| `autoscaling` | `*` | `*` |
| `policy` | `*` | `*` |
| `rbac.authorization.k8s.io` | `roles`, `rolebindings` | `*` |

**What's intentionally NOT in maintain:** `clusterroles`,
`clusterrolebindings`, anything CRD-related (apply per cluster if
needed), workload identity APIs. Granting cluster-level RBAC mutate
is the line between `maintain` and `admin`.

### Customization

Both roles are operator-tunable. After applying:

```sh
kubectl --context <cluster> edit clusterrole periscope-triage
```

Drift between the shipped role (chart `appVersion`) and the cluster
is the operator's responsibility. The chart's `NOTES.txt` prints the
shipped-role version on each `helm install` / `helm upgrade` so you
can pin and re-apply when the chart bumps a verb.

---

## Mode: `in-cluster` (single-cluster install)

When Periscope is deployed into the same cluster it manages (the most common single-cluster install pattern — kind, minikube, single-cluster prod), register that cluster with `backend: in-cluster`:

```yaml
# my-values.yaml
clusters:
  - name: in-cluster
    backend: in-cluster
```

The pod uses its own ServiceAccount token (mounted at `/var/run/secrets/kubernetes.io/serviceaccount/`) as the underlying credentials, then layers per-user impersonation on top via `Impersonate-User` / `Impersonate-Group` headers — same model as `eks` and `kubeconfig`, just with a different credential source.

### Auto-rendered RBAC

The chart's `cluster-rbac.yaml` auto-detects in-cluster mode and renders the impersonator binding with the chart's SA as a subject. No separate `kubectl apply` step needed — `helm install` does the whole thing.

The rendered `ClusterRoleBinding` looks like:

```yaml
kind: ClusterRoleBinding
metadata:
  name: periscope-impersonator
subjects:
  - kind: Group                  # for tier-mode managed clusters (if enabled)
    name: periscope-bridge
  - kind: ServiceAccount         # auto-added when an in-cluster cluster is in the registry
    name: <release-name>-periscope
    namespace: <release-namespace>
```

### What the SA can and can't do

The SA only ever holds the `impersonate` verb on `users` / `groups`. It cannot read or modify any resource directly. Every actual API call goes through impersonation, so the apiserver evaluates RBAC against the impersonated user (e.g. `alice@corp` in tier mode mapped to `periscope-tier:admin`).

Net: the chart-rendered SA is a thin "proxy" with no standalone power. All real authorization is per-user, per the existing tier / shared / raw modes.

### Combining with managed clusters

In-cluster and the other backends compose. A common production setup:

```yaml
clusters:
  # The cluster periscope itself runs in
  - name: periscope-host
    backend: in-cluster
  # Managed EKS clusters reached via Pod Identity
  - name: prod-eu-west-1
    backend: eks
    region: eu-west-1
    arn: arn:aws:eks:eu-west-1:111111111111:cluster/prod-eu-west-1
  - name: stg-us-east-1
    backend: eks
    region: us-east-1
    arn: arn:aws:eks:us-east-1:111111111111:cluster/stg-us-east-1
```

Each cluster is independent. The same OIDC user identity flows to all of them via impersonation; per-cluster RBAC determines what the user can actually do where.

## Mode: `agent` (managed cluster via tunnel)

Available since v1.0.0. Pre-existing eks / kubeconfig / in-cluster
modes still work alongside; agent is just a fourth `backend:` value.

The agent backend inverts the network direction: a tiny
`periscope-agent` pod runs *on the managed cluster* and dials *out*
to the central Periscope server. The agent's own ServiceAccount
identity (not Pod Identity, not IRSA) is what the local apiserver
sees; per-user RBAC enforcement still happens via the same
`Impersonate-User` / `Impersonate-Group` headers, just forwarded
through the tunnel.

Operator-facing setup (token mint, helm install, troubleshooting)
lives in [`docs/setup/agent-onboarding.md`](agent-onboarding.md);
the design walkthrough is in
[`docs/architecture/agent-tunnel.md`](../architecture/agent-tunnel.md).

What the agent's RBAC looks like by default (rendered by the
[`periscope-agent`](../../deploy/helm/periscope-agent/) chart's
`clusterrole.yaml`):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: periscope-agent
rules:
  # Read everything — the bulk of dashboard traffic.
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]

  # Impersonation — the load-bearing permission. Lets the central
  # server's per-user authz model reach this cluster's apiserver.
  - apiGroups: [""]
    resources: ["users", "groups", "serviceaccounts"]
    verbs: ["impersonate"]
  - apiGroups: ["authentication.k8s.io"]
    resources: ["userextras/scopes"]
    verbs: ["impersonate"]
```

**Important: the agent's RBAC is the ceiling for what's physically
possible on this cluster.** Impersonation does not bypass cluster
RBAC — the apiserver still evaluates the impersonated user's
permissions, but the *actual transport* (the agent) needs the
underlying verbs allowed too. So if you want users to be able to
`apply`, `delete`, or `exec` on this cluster, the agent's
ClusterRole has to grant those verbs.

The chart-shipped default is **read + impersonate only**. To enable
write paths, set `clusterRole.enabled: false` and bind your own
ClusterRole that includes `create`, `update`, `patch`, `delete`,
`pods/exec` etc.

Tier subjects (`periscope-bridge`, `periscope-tier-admin`, etc.)
are not relevant on the agent's local cluster — those bindings live
on the cluster Periscope is running in (the central one), where the
server validates the human's tier before forwarding the
impersonation header. The agent just relays bytes.

What the agent does **not** do:

- It doesn't connect to AWS, GCP, or any cloud control plane. No
  IAM trust, no `eks:GetToken`, no Pod Identity association.
- It doesn't see user passwords, OIDC tokens, or session cookies.
  Those terminate at the central server.
- It doesn't have permission to modify its own RBAC. The chart's
  ServiceAccount is locked to `get/update` on a single named state
  Secret in its own namespace.
- It doesn't write to audit. All audit emission is server-side.

For the failure-mode catalogue (cert expired, agent disconnected,
deregistered cluster, server CA rotated) see
[`docs/architecture/agent-tunnel.md`](../architecture/agent-tunnel.md) 10.
