# Helm release browser

Periscope ships a **read-only Helm release browser** as part of the
core binary. It surfaces the helm releases installed on each managed
cluster — list, detail (manifest + values), revision history, and
diff between any two revisions — under the SPA's "Helm" sidebar
group and the API path `/api/clusters/{cluster}/helm/...`.

There is no chart configuration to enable it; the routes are always
registered. Visibility is governed entirely by the impersonated
user's RBAC on the storage objects Helm uses (Secrets by default,
ConfigMaps as a fallback).

This page covers the operational surface: what's there, what RBAC
is required, the limits that bound the responses, and how cache
behavior affects what you see in the UI.

---

## 1. Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/clusters/{cluster}/helm/releases` | List releases on the cluster (current revision per release). |
| `GET` | `/api/clusters/{cluster}/helm/releases/{namespace}/{name}` | Release detail: values, rendered manifest, parsed resources, metadata. |
| `GET` | `/api/clusters/{cluster}/helm/releases/{namespace}/{name}/history` | Revision history (newest first). |
| `GET` | `/api/clusters/{cluster}/helm/releases/{namespace}/{name}/diff?from=N&to=M` | Structured diff (values + manifest) between revisions N and M. |

All endpoints run under the requesting user's impersonated identity.
A user who can't read the underlying storage objects gets a clean
403 from the apiserver, which Periscope forwards.

---

## 2. Storage driver auto-probe

Helm 3 stores releases in **Secrets** (the default driver) or
**ConfigMaps**. Periscope auto-detects which driver the cluster uses:

1. Try listing `Secrets` cluster-wide with the helm `owner=helm`
   label. If any are returned, lock to the **secret** driver.
2. Otherwise, try the same against `ConfigMaps`. If any are returned,
   lock to the **configmap** driver.
3. Otherwise, default to the **secret** driver — downstream LIST
   calls will surface the real distinction (403 vs empty) cleanly.

The probe result is cached per-cluster for 5 minutes. The `sql`
driver is **not supported** (it's rare in practice and would require
managing a connection pool to an external DB the operator
configured).

---

## 3. RBAC requirements

The impersonated user needs:

| Driver | Verbs needed |
|---|---|
| `secret` (default) | `get`, `list` on `secrets` in the release namespaces |
| `configmap` | `get`, `list` on `configmaps` in the release namespaces |

For the auto-probe to succeed cluster-wide (so the SPA's "Releases"
list is populated across namespaces), `list` on the storage kind
should be cluster-scoped. A namespace-scoped binding works for
single-namespace use but the cluster-wide list returns empty in that
case.

The shipped tier ClusterRoles cover this:

- `read` (`view`) — gets `secrets:get` and `configmaps:get`/`list`,
  enough for the configmap driver only. Helm releases stored as
  Secrets aren't readable from the `view` ClusterRole alone (Secret
  reads are intentionally privileged in the upstream `view` role).
- `triage`, `write`, `maintain`, `admin` — all cover both drivers.

If you want **read-tier users** to also see secret-driver releases,
add a ClusterRoleBinding that grants them `get`/`list` on Secrets in
the relevant namespaces — see the
[cluster-rbac.md helm browser appendix](./cluster-rbac.md#helm-release-browser-rbac)
for a copy-pasteable sample.

---

## 4. Response caps and truncation

Three constants bound the responses to keep payloads predictable
under impersonation:

| Cap | Value | Where |
|---|---|---|
| Release list | **200 releases** | `cmd/periscope/helm_handler.go: helmListCap` |
| Detail / diff payload | **5 MiB** | `cmd/periscope/helm_handler.go: helmDetailMaxBytes` |
| History default | **10 revisions** | `internal/k8s/helm.go` (override with `?limit=N`) |

When the list cap binds, the response carries `truncated: true` and
the SPA shows a "showing first 200 of N" banner. The 200-cap reflects
realistic helm fleet sizes (most clusters run < 50 releases); past
that, a search-by-name UX is the right answer rather than scrolling
a thousand-row list.

The 5 MiB detail cap is generous (a real-world chart's manifest +
values rarely exceeds 200 KiB) but exists to defend against
accidentally-huge releases (a chart inlining a binary blob in
`values.yaml`, for example).

---

## 5. Caching

The list endpoint caches per `(actor, cluster, impersonation
groups)` for **30 seconds**. Subsequent navigations across the SPA's
Helm tab feel instant; a release just-installed by `helm install`
appears within ~30s of the install completing.

Detail, history, and diff endpoints are **not cached** — each
request hits the apiserver. These pages are rarer-clicked and the
freshness signal matters more (an operator looking at "what
happened in revision 7" wants the live answer).

To force a list refresh, the SPA's Helm tab has a "refresh" button
that bypasses the cache.

---

## 6. What's read, what's not

The browser surfaces:

- Release name, namespace, revision, status, chart name + version,
  app version, last-updated timestamp.
- Rendered manifest (parsed into a structured resource list per
  revision).
- Computed values (`helm get values --all` equivalent — chart
  defaults merged with user overrides).
- Release notes (`NOTES.txt` output).
- Revision history with status transitions.
- Side-by-side diff between any two revisions: values diff +
  manifest diff.

It does **not** support:

- `helm install` / `helm upgrade` / `helm uninstall` (write
  operations are explicitly out of scope for v1; SAR fan-out per
  rendered resource is planned for v2).
- Chart catalog browsing.
- Repository management.
- The `sql` storage driver.

The Helm release browser is a debugging / observability surface, not
a deployment tool. For deploys, your existing GitOps (ArgoCD, Flux)
or pipeline tooling stays the source of truth.

---

## 7. Verifying

After install:

```sh
# 1. Route is registered (will 401 if you're not authenticated):
kubectl -n periscope port-forward svc/periscope 8080:8080
curl -i http://localhost:8080/api/clusters/<cluster>/helm/releases

# 2. The SPA shows the "Helm" group in the sidebar (under any
#    cluster). Clicking it lists releases.

# 3. RBAC sanity:
kubectl --context <cluster> auth can-i list secrets \
  --as=auth0\|alice \
  --as-group=periscope-tier:write
# yes
```

If the list is empty but you know there are releases on the cluster,
check (a) the user's impersonated groups have `list` on the storage
kind cluster-wide, (b) the auto-probe locked onto the right driver
(check pod logs for `helm: driver auto-detected driver=...`).

---

## 8. Related docs

- [`docs/setup/cluster-rbac.md`](./cluster-rbac.md) — RBAC modes,
  tier verbs, helm browser sample binding.
- [`docs/setup/audit.md`](./audit.md) — read-side observability for
  privileged actions (helm browser reads aren't audited; only
  privileged mutations are).
- `internal/k8s/helm.go` — the storage decoder and auto-probe.
- `cmd/periscope/helm_handler.go` — list/detail/history/diff handlers.
- `cmd/periscope/helm_cache.go` — the per-actor list cache.
