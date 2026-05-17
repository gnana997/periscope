# impersonation-client

Companion artifact for the Periscope blog post on per-user impersonation
in Kubernetes. A ~200-line Go program that demonstrates the two patterns
the post argues for:

1. **Impersonate-User / Impersonate-Group headers** set via `client-go`'s
   `rest.Config.Impersonate`. From there, every request carries the human's
   identity and the apiserver evaluates RBAC under that identity — not the
   bridge service account's.
2. **`SelfSubjectAccessReview` pre-flight**. Before showing a button, the
   UI asks the apiserver "can this user do this?" and disables the button
   inline if not. The alternative is a 403 after the click and a support
   ticket.

## Prerequisites

- Go 1.24+
- `kubectl` configured with a cluster where you have permission to apply
  RBAC manifests. A `kind` cluster works out of the box (cluster-admin
  has the `impersonate` verb implicitly).

## Quickstart

```bash
make apply           # applies bridge SA + alice's pod-reader Role
make run-allow       # SSAR: alice can list pods in default     → allowed
make run-deny        # SSAR: alice cannot delete kube-system pods → denied
make run-execute     # same as run-allow, then actually lists the pods
```

Override the kubeconfig context with `CONTEXT=`, e.g.:

```bash
make CONTEXT=kind-mycluster run-allow
```

## What the manifests do

- `manifests/bridge-sa.yaml` — the `periscope-bridge` ServiceAccount + a
  `ClusterRole` granting only `impersonate` on users/groups. This is the
  shape every Periscope-style console runs with. The bridge has zero
  direct rights on pods, secrets, deployments — those rights belong to
  the impersonated user.
- `manifests/grant-alice.yaml` — test fixture binding `alice@corp` to a
  pod-reader Role in `default`. Without it, `run-allow` denies. Delete it
  to see the deny path for both calls.

## What to look for

Run `kubectl --v=8` alongside the demo to see the actual HTTP request
headers:

```bash
kubectl --v=8 --as alice@corp --as-group dev get pods -n default 2>&1 \
  | grep -i impersonate
```

You'll see `Impersonate-User: alice@corp` and `Impersonate-Group: dev`
on every request — the same headers this program sends via
`rest.Config.Impersonate`.

## Flags

| Flag | Default | Purpose |
|---|---|---|
| `--context` | (current) | kubeconfig context to use |
| `--kubeconfig` | `$KUBECONFIG` or `~/.kube/config` | kubeconfig path |
| `--as` | (required) | user to impersonate |
| `--as-group` | (none) | comma-separated groups to impersonate |
| `--verb` | `list` | verb to check (get, list, create, update, delete, …) |
| `--resource` | `pods` | resource (pods, deployments, configmaps, secrets, services, namespaces, nodes) |
| `--namespace` | `default` | namespace (empty for cluster-scoped) |
| `--name` | (none) | resource name; required for `--execute get` |
| `--execute` | `false` | if SSAR allows, perform the verb (get/list only) |

`--execute` deliberately refuses mutating verbs. The SSAR verdict is the
point of the demo — proving the verb *would have been* allowed without
actually mutating cluster state.
