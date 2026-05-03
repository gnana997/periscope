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
