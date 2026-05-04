# Onboarding a managed cluster via the agent (#42)

This guide walks an operator through registering a new managed
cluster with Periscope using the agent backend. Same-account same-
region first; cross-account variants are at the end.

If you're new to Periscope, read [`docs/setup/deploy.md`](deploy.md)
first — that covers installing the central server. This page picks
up after the central server is running and you want to add another
cluster to its fleet view.

> **Why an agent at all?** The agent dials *out* from the managed
> cluster to the central server over a long-lived WebSocket — no
> inbound network access needed, no IAM role per cluster, works on
> any K8s with outbound HTTPS (EKS, GKE, AKS, on-prem k3s, kind).
> See [#41](https://github.com/gnana997/periscope/issues/41) for
> design rationale.

## Prerequisites

On the **central cluster** (where the periscope server runs):

- Periscope ≥ v1.x.0 installed via the periscope chart
- Server values include `agent.enabled: true` and a real `agent.tunnelSANs`
  matching whatever DNS name agents will dial
- The tunnel listener (default `:8443`) is reachable from your managed
  clusters — typically a `LoadBalancer`-type Service or a separate
  TLS-passthrough Ingress (ALB strips client certs, so an ALB Ingress
  in front of the tunnel **will not work**)

On the **managed cluster** (the one you want to add to the fleet):

- Outbound HTTPS to the central server's tunnel hostname
- A namespace to install the agent into (default `periscope`)

## Step-by-step

### 1. Pre-register the cluster name

The agent's mTLS CN must match an entry in the central server's
cluster registry. Add the cluster to your server's values.yaml:

```yaml
clusters:
  - name: prod-eu
    backend: agent
    environment: prod
```

Then `helm upgrade` the central server. The cluster card appears in
the fleet view immediately as **unreachable** (no agent is connected
yet).

> The cluster name is **load-bearing**: it becomes the mTLS cert CN,
> the tunnel session key, and the registry key. Must match the DNS-
> 1123 shape (lowercase, digits, dashes; 1-63 chars). The schema
> validators on both charts enforce this — typos surface at install
> time, not at runtime.

### 2. Mint a bootstrap token

From an admin-tier session (the SPA's user menu shows the tier; only
admin can hit `/api/agents/tokens`):

```sh
curl -sX POST https://periscope.example.com/api/agents/tokens \
  -H "Cookie: periscope_session=<your-session-cookie>" \
  -H "Content-Type: application/json" \
  -d '{"cluster":"prod-eu"}'
# → { "token":"abc123...", "cluster":"prod-eu", "expiresAt":"2026-..." }
```

The token is **single-use** and **15-min TTL**. If the agent install
fails or you wait too long, mint a fresh one.

> Cluster-name binding is enforced: a token minted for `prod-eu` can
> only register `prod-eu`. A wrong-cluster guess **burns the token**
> (one-shot redemption either way).

### 3. Install the agent on the managed cluster

```sh
kubectl config use-context <managed-cluster>

helm upgrade --install periscope-agent \
  oci://ghcr.io/gnana997/charts/periscope-agent \
  --version <X.Y.Z> \
  --namespace periscope \
  --create-namespace \
  --set agent.serverURL=wss://agents.periscope.example.com:8443 \
  --set agent.clusterName=prod-eu \
  --set agent.registrationToken=<paste-token-from-step-2>
```

The agent will:
1. Generate an ECDSA P-256 keypair locally (private key never leaves
   the cluster)
2. Build a CSR with `CN=prod-eu`
3. POST `/api/agents/register` with the bootstrap token + CSR
4. Persist the returned mTLS cert + key + server CA into the
   `periscope-agent-state` Secret
5. Open the WebSocket tunnel to `wss://agents.periscope.example.com:8443/api/agents/connect`
   presenting the mTLS cert
6. Hold the tunnel open for the lifetime of the pod, with jittered
   exponential reconnect on drops

### 4. Verify

On the managed cluster:

```sh
kubectl -n periscope logs -l app.kubernetes.io/name=periscope-agent -f
# Expect:
#   periscope-agent starting ...
#   first boot: registering with central server ...
#   agent registration complete; state persisted ...
#   agent identity ready ...
#   opening tunnel ...
#   tunnel.client_connected ...
```

In the central server's fleet view, the `prod-eu` card should flip
from **unreachable** to **healthy** within a few seconds.

### 5. Bootstrap-token cleanup

Once the agent has registered successfully, the bootstrap-token
Secret is single-use spent. Optional cleanup:

```sh
kubectl -n periscope delete secret periscope-agent-bootstrap
```

## Troubleshooting

### "registration rejected" (HTTP 401) on first boot

The bootstrap token failed validation. The server returns a uniform
error message for security; check the server logs to distinguish:

```sh
kubectl -n periscope logs deploy/periscope --tail=20 | grep tunnel.register
```

Common causes:
- **Token expired** (15-min TTL elapsed) → mint a fresh one
- **Token already used** (re-running `helm install` with the same
  value) → mint a fresh one
- **Cluster name mismatch** between `--set agent.clusterName=` and
  the value in the token mint request → re-mint with the correct name

### Agent connects, then the tunnel drops every few seconds

Check the central server logs:

```
tunnel.session_error  err="websocket: close 1006 (abnormal closure)"
```

If the agent reconnects but the cluster never goes healthy, the most
common causes are:

- **Server cert SAN mismatch** — agent dials `wss://agents.example.com:8443`
  but the server cert SAN is `localhost`. Fix:
  `helm upgrade periscope --set-string 'agent.tunnelSANs=agents.example.com\,localhost'`
  on the central cluster, then restart the periscope pod (the server
  cert is minted at startup).
- **Agent's CA bundle is stale** — server CA was rotated since the
  agent registered. Delete the agent's state Secret + bootstrap
  Secret, mint a fresh token, re-install.
- **Corporate proxy idle-times out the WebSocket** — increase the
  agent's keepalive: `--set env[0].name=...` (a future
  `agent.keepaliveSeconds` value will avoid the env-array escape).

### Cluster card stays "unreachable" — no agent in the registry

Check the registry actually has the entry:

```sh
curl -s https://periscope.example.com/api/clusters \
  -H "Cookie: periscope_session=..." | jq .
```

If `prod-eu` isn't in the list, you forgot to `helm upgrade` the
central server after adding to `clusters[]`. Re-do step 1.

### `helm template` fails with `agent.serverURL is required`

You forgot to set `agent.serverURL` (or `agent.clusterName`). The
agent chart's schema enforces both as required.

### mTLS handshake fails with "bad certificate"

The agent's persisted cert doesn't chain to the server's current CA.
Either:
- The server's CA Secret was deleted and regenerated (every previously
  -issued agent cert is now invalid)
- The agent was registered against a different server

Fix: delete the agent's state Secret on the managed cluster, mint a
fresh token, re-install.

```sh
kubectl -n periscope delete secret periscope-agent-state
# then redo steps 2 + 3
```

## Cross-account / cross-cloud

The agent doesn't care which AWS account, cloud, or network its
managed cluster lives in — only that the agent pod can reach the
central server's tunnel hostname over outbound HTTPS. No IAM trust
policies to set up, no peering, no VPN.

For each cluster, repeat steps 1–5. The bootstrap token mint is the
only step that talks to the central server's IdP; once a token is
in hand, the agent installs on any K8s cluster with egress.

## Security notes

- The bootstrap token authorises **registration** for one cluster
  name, not anything else. A leaked token can register that one
  cluster (and burn itself); it cannot impersonate other clusters
  or access any data.
- The persisted mTLS cert is the long-lived agent identity. Keep the
  state Secret protected (the chart's RBAC limits access, but anyone
  with `secrets/get` on the agent's namespace can pull it).
- Agent cluster RBAC is scoped to `get/list/watch/impersonate` by
  default. Apply/delete handlers depend on the user's impersonated
  identity; the agent itself never gains write access beyond what
  its `ClusterRole` grants. Tighten the chart's `clusterRole`
  template if your security model wants stricter defaults.
- For a deeper threat-model walkthrough see
  [issue #41](https://github.com/gnana997/periscope/issues/41).

## What's next

The Periscope SPA includes a **+ Onboard cluster** button on the
fleet page that walks through this flow with copy-paste snippets and
live status (token mint → install command → connection state). For
now it shows the install command; full Rancher-style polling +
runtime registry mutations land in v1.x.1.

## See also

- [`examples/agent/`](../../examples/agent/) — sample values files
  and a reference `register-and-install.sh` script
- [RFC 0003](../rfcs/0003-audit-log.md) — audit shape (every action
  taken via an agent-backed cluster lands in the central audit
  store, with `cluster: <name>` set on the row)
- [Issue #41](https://github.com/gnana997/periscope/issues/41) —
  agent-vs-central-IAM design discussion
- [Issue #42](https://github.com/gnana997/periscope/issues/42) —
  v1.x.0 multi-cluster epic
