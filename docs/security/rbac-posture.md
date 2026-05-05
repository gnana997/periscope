# RBAC posture for security review

This document is for security-team reviewers evaluating Periscope for adoption. It explains, in detail:

1. **The RBAC model** — what Periscope actually grants on a managed cluster, and why each piece is necessary.
2. **What the default Helm install creates** — every ClusterRole and ClusterRoleBinding, listed.
3. **CIS / AWS Guardrails findings the default install will trigger** — why each fires, why each is a lower-risk-than-the-rule-implies finding given Periscope's architecture, and how to opt out.
4. **Restrictive deployment recipes** — for environments where the default posture is too permissive.

If you're an operator setting up Periscope and just want the install instructions, see [`docs/setup/cluster-rbac.md`](../setup/cluster-rbac.md). This document is the deeper "why" behind the choices that doc makes.

---

## TL;DR

- **Periscope's pod** gets the `impersonate` verb on a managed cluster — nothing else by default. Every K8s call rides `Impersonate-User` / `Impersonate-Group` headers, and the apiserver evaluates RBAC against the **human user**, not Periscope.
- **Group prefixing** (`periscope-tier:` / `periscope:`) means an attacker who compromises Periscope cannot impersonate into `system:masters` or any unprefixed privileged group.
- **Audit logs** record both the impersonator (Periscope) and the impersonated identity (the human) on every request. Forensics get the full chain.
- **The default Helm install creates a `cluster-admin` ClusterRoleBinding** for the `periscope-tier:admin` group. CIS Benchmark 5.1.1 and AWS Guardrails will flag this. The binding is gated behind tier-mode + an explicit IdP-group-to-`admin` mapping in operator config — so the rule fires on the YAML alone, not on actual privilege at rest.

  An [opt-in default is tracked for v1.1](https://github.com/gnana997/periscope/issues/84). Today, operators in restrictive environments can disable the binding via `clusterRBAC.enabled: false` and ship their own RBAC (recipe below).

- **The agent's own ServiceAccount** has `get/list/watch` on `*` resources + `impersonate` on `users/groups/serviceaccounts`. This is broad-but-not-cluster-admin and is necessary for the dashboard to enumerate every resource type. The wildcard verbs are the load-bearing reason the SPA can list any kind without per-kind RBAC bumps.

---

## The RBAC model in 2 minutes

```
┌────────────────────────────────┐                    ┌─────────────────┐
│  Browser  (alice@corp)         │                    │    Apiserver    │
│  + httpOnly session cookie     │                    │    (managed)    │
└──────────────┬─────────────────┘                    └────────▲────────┘
               │ HTTPS                                         │
               │                                               │
               ▼                                               │
┌────────────────────────────────┐                             │
│  Periscope server pod          │       Impersonate-User: alice@corp
│  (its own SA, NOT cluster-     │       Impersonate-Group: periscope-tier:write
│   admin — only `impersonate`)  │  ───────────────────────────┘
└────────────────────────────────┘
                                              │
                                              │  Apiserver evaluates RBAC
                                              │  against `alice@corp` and
                                              │  `periscope-tier:write`,
                                              │  NOT against the
                                              │  Periscope SA. Audit log
                                              │  records BOTH identities.
                                              ▼
                                        ─ allowed ─→ proceed
                                        ─ denied  ─→ 403 Forbidden
```

Two non-negotiables that fall out of this:

1. **Periscope's own SA gets ONLY `impersonate`** on each managed cluster (built-in + tier modes; raw mode also adds it). No other K8s perms. A compromised Periscope can act *as any user the operator has configured*, but cannot itself read secrets, exec into pods, or do anything else without an impersonation step that gets audited.

2. **Impersonated groups are always prefixed** with `periscope-tier:` or `periscope:`. RBAC bindings reference the prefixed names. An attacker who compromises Periscope cannot impersonate into `system:masters` or any other unprefixed privileged group — bindings on those won't match.

For the full architectural rationale, see [RFC 0002 — Auth](../rfcs/0002-auth.md).

---

## What the default Helm install creates

Two charts. The server chart (`periscope`) runs the central dashboard pod and emits cluster-RBAC manifests that you apply on each managed cluster. The agent chart (`periscope-agent`) runs the per-cluster tunnel pod and is installed directly on each managed cluster.

### Server chart (`deploy/helm/periscope`)

When `clusterRBAC.enabled: true` (default) **and** at least one cluster has `backend: in-cluster`:

| Object | Kind | What it grants |
|---|---|---|
| `periscope-impersonator` | ClusterRole | `impersonate` on users/groups/serviceaccounts; **nothing else** |
| `periscope-impersonator` | ClusterRoleBinding | binds the impersonator role to the bridge group |
| `periscope-tier-read` | ClusterRoleBinding | `periscope-tier:read` group → built-in `view` ClusterRole |
| `periscope-tier-write` | ClusterRoleBinding | `periscope-tier:write` group → built-in `edit` ClusterRole |
| `periscope-tier-admin` | ClusterRoleBinding | `periscope-tier:admin` group → built-in **`cluster-admin`** ⚠️ |
| `periscope-triage` | ClusterRole | view + debug verbs (no spec mutation) |
| `periscope-tier-triage` | ClusterRoleBinding | `periscope-tier:triage` group → `periscope-triage` |
| `periscope-maintain` | ClusterRole | view + write on workloads (no `secrets`/`*roles*`/`*bindings*`) |
| `periscope-tier-maintain` | ClusterRoleBinding | `periscope-tier:maintain` group → `periscope-maintain` |

Plus a small Role + RoleBinding for the agent CA Secret (`get`/`update` on a single named Secret in the install namespace — least-privilege, the chart pre-creates the Secret so the server doesn't even need `create`).

### Agent chart (`deploy/helm/periscope-agent`)

When the chart is installed on a managed cluster (`clusterRole.enabled: true` + `clusterRBAC.enabled: true`, both default):

| Object | Kind | What it grants |
|---|---|---|
| `<release>-periscope-agent` | ClusterRole | **`get`/`list`/`watch` on `*` resources; `impersonate` on `users`/`groups`/`serviceaccounts`/`userextras/scopes`** |
| `<release>-periscope-agent` | ClusterRoleBinding | binds the above to the agent SA |
| Same five tier bindings + custom roles | as server chart |

Plus a Role + RoleBinding for the agent's own state Secret (the persisted mTLS cert + key, scoped to the install namespace).

---

## CIS / AWS Guardrails findings the default install will trigger

Listed in order of how often each rule fires for Periscope.

### Finding 1 — CIS Kubernetes Benchmark 5.1.1: "Avoid use of the `cluster-admin` role"

**The rule**: any ClusterRoleBinding referencing `cluster-admin` is flagged.

**Where it fires**: both charts' `cluster-rbac.yaml` create `periscope-tier-admin → cluster-admin`.

**Why it's there**:

The binding makes the `tier` authorization mode's `admin` tier mean "full cluster access via impersonation." Operators who have at least one IdP group mapped to `admin` (`auth.authorization.groupTiers.<group>: admin`) get a working admin tier without writing custom RBAC.

**Why it's lower-risk than the rule implies**:

The binding is gated by **three** independent operator decisions:

1. The binding only matters when `auth.authorization.mode` is set to `tier` (default is `shared`).
2. The binding only takes effect for users in groups the operator has explicitly mapped to `admin` via `groupTiers`.
3. The IdP must actually populate those groups in the OIDC token (Auth0 / Okta operator-controlled).

If any one of those is unset, the binding is dormant. Periscope's own SA does **not** have cluster-admin; it has only `impersonate`. An attacker who compromises Periscope cannot use this binding to escalate without ALSO compromising the operator's OIDC IdP.

**Audit-trail implication**: a user reaching cluster-admin via this path leaves both the impersonator (Periscope's bridge identity) and the impersonated user (`alice@corp`, with their groups) on the K8s audit log. Periscope's own audit log additionally records the action structurally.

**How to opt out today**:

```yaml
# Disables ALL chart-managed tier bindings — coarse-grained.
# Operators who want read/triage/write/maintain but NOT admin can
# instead ship a custom RBAC set; recipe below.
clusterRBAC:
  enabled: false
```

**The fix that's coming** ([#84, milestone v1.1](https://github.com/gnana997/periscope/issues/84)):

A new `clusterRBAC.adminTier.enabled: false` value will be the default. Default install will create read / triage / write / maintain bindings but **not** the cluster-admin binding. Operators who want admin tier opt in explicitly:

```yaml
clusterRBAC:
  enabled: true            # tier system on
  adminTier:
    enabled: true          # opt-in to cluster-admin binding
    clusterRoleName: cluster-admin   # or point at a tighter custom role
```

After v1.1 ships, default installs will pass CIS 5.1.1 out of the box.

### Finding 2 — Wildcard verbs / wildcard resources

**The rule** (multiple variants — CIS 5.1.3, kube-bench 5.1.5, AWS Guardrails RBAC checks): a ClusterRole with `verbs: ["*"]` or `resources: ["*"]` is flagged as overly permissive.

**Where it fires**: the agent chart's `clusterrole.yaml` grants `get`/`list`/`watch` on `*` resources:

```yaml
rules:
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["get", "list", "watch"]
```

**Why it's there**:

The Periscope SPA renders every resource kind the cluster supports — including Custom Resources discovered dynamically from `/openapi/v3`. A static enumeration of resource types in the agent's RBAC would either:

- Miss CRDs the operator hasn't anticipated (forcing a chart values bump for every new CRD), or
- Be a several-hundred-line list that's harder to audit than the wildcard

**Why it's lower-risk than the rule implies**:

- Verbs are restricted to **read-only** (`get`/`list`/`watch`). The agent's SA cannot mutate anything — every write goes through impersonation, so write RBAC depends on the **human user**.
- The agent does NOT have `escalate`, `bind`, `*` verbs, or any subresource access beyond what the tier mappings explicitly grant.
- The agent has the `impersonate` verb, but that's the load-bearing primitive of the entire architecture (see "The RBAC model" above).

**How to tighten if your environment requires it**:

```yaml
# In your agent values.yaml override
clusterRole:
  enabled: false      # disable the chart's wildcard ClusterRole

# Then ship your own ClusterRole + ClusterRoleBinding listing
# specific apiGroups / resources / verbs. Recipe below.
```

### Finding 3 — `impersonate` verb on users/groups

**The rule** (multiple variants): granting `impersonate` is flagged as a privilege-escalation path.

**Where it fires**: both charts grant `impersonate` on `users`, `groups`, `serviceaccounts` to either the server SA (in `tier`/`raw` mode) or the agent SA.

**Why it's there**:

Without `impersonate`, per-user RBAC enforcement is impossible — Periscope cannot identify the human user to the apiserver, so RBAC evaluates against the (single, shared) Periscope SA. The audit log would show `system:serviceaccount:periscope:periscope` for every action, not `alice@corp`. This defeats the entire compliance model.

**Why it's intentional**:

This is the architectural **premise** of Periscope. Group prefixing (`periscope-tier:` / `periscope:`) prevents impersonation into unprefixed privileged groups (`system:masters`, etc.). The audit log records the impersonator + impersonated identity on every request. Periscope's design *requires* this verb — disabling it disables Periscope.

**Mitigation**: not applicable. Operators who can't allow `impersonate` cannot run Periscope and should evaluate other tools.

---

## Restrictive deployment recipes

For environments where the default posture is too permissive.

### Recipe A — Custom RBAC, no chart-managed bindings

Disable everything the chart creates and ship your own RBAC tailored to your policy:

```yaml
# Server chart values
clusterRBAC:
  enabled: false           # don't create periscope-tier-* bindings

# Agent chart values
clusterRole:
  enabled: false           # don't create the agent SA's ClusterRole
clusterRBAC:
  enabled: false           # don't create periscope-tier-* bindings
```

Then write your own ClusterRole + ClusterRoleBinding with whatever verbs/resources your policy allows, bound to whatever group names you want Periscope to impersonate into. Make sure the agent SA gets the `impersonate` verb on at least the groups your operators will use.

### Recipe B — Tier system minus admin tier (today, before v1.1)

Disable all chart-managed bindings (Recipe A) and ship just the four non-admin tier bindings yourself. Use the chart's rendered output as a starting point:

```sh
helm template my-periscope deploy/helm/periscope \
  --set clusterRBAC.enabled=true \
  --show-only templates/cluster-rbac.yaml \
  > rbac.yaml
# Delete the cluster-admin binding (search for "name: cluster-admin")
# Apply on each managed cluster:
kubectl --context <cluster> apply -f rbac.yaml
```

After [v1.1 ships](https://github.com/gnana997/periscope/issues/84), this collapses to a single `clusterRBAC.adminTier.enabled: false` value.

### Recipe C — Tighter agent SA (no wildcards)

Replace the agent's ClusterRole with an explicit list of resources the SPA will surface in your fleet. Verbose but Guardrails-clean:

```yaml
# 1. Disable the chart's ClusterRole
clusterRole:
  enabled: false

# 2. Ship a custom ClusterRole + binding via your own manifests
# (see deploy/helm/periscope-agent/templates/clusterrole.yaml for the
#  shape; replace the wildcards with the explicit list of apiGroups +
#  resources you want).
```

Maintenance cost: you'll need to add new resource types to this list when CRDs are added on managed clusters. Worth it only if Guardrails rules block adoption otherwise.

### Recipe D — Kustomize patch on top of `helm template`

For shops that prefer Kustomize, render the chart and patch:

```sh
helm template my-periscope-agent deploy/helm/periscope-agent \
  --values my-values.yaml > base/agent.yaml

# kustomization.yaml
# patches:
#   - target: { kind: ClusterRoleBinding, name: periscope-tier-admin }
#     patch: |-
#       - op: remove
#         path: /
```

---

## What we deliberately don't do

- **Periscope does not grant itself `escalate`** — agents cannot grant themselves any permissions beyond what's already in their ClusterRole.
- **Periscope does not impersonate `system:*` groups** — the `Impersonate-Group` header always carries the `periscope-*` prefix, never `system:masters` or other privileged unprefixed groups.
- **Periscope does not store kubeconfigs or user tokens** — every API call rebuilds credentials from the request context (Pod Identity / IRSA for AWS, agent tunnel for non-AWS). Nothing static lives on disk or in memory.

---

## References

- [`docs/setup/cluster-rbac.md`](../setup/cluster-rbac.md) — operator-facing setup guide for the three authz modes
- [`docs/rfcs/0002-auth.md`](../rfcs/0002-auth.md) — auth RFC, including the impersonation model
- [`docs/architecture/agent-tunnel.md`](../architecture/agent-tunnel.md) — agent backend architecture
- [`SECURITY.md`](../../SECURITY.md) — vulnerability disclosure policy
- [Issue #84 — opt-in cluster-admin binding (v1.1)](https://github.com/gnana997/periscope/issues/84)
- CIS Kubernetes Benchmark v1.10 §5.1.1, §5.1.3
- AWS Guardrails — *EKSClusterAdminRoleCheck*, *EKSWildcardRBACCheck*
