# periscope-agent Helm chart

Install on any K8s cluster you want Periscope to manage (the
"managed cluster"). The agent dials *out* to the central Periscope
server over a long-lived WebSocket — no inbound network access
needed, no IAM role per cluster, no kubeconfig with embedded creds
to ship around. Works on EKS, GKE, AKS, on-prem k3s, kind, anything
with outbound HTTPS to the central server.

The central Periscope server install (separate chart:
[`periscope`](https://artifacthub.io/packages/helm/periscope/periscope))
must be configured with `agent.enabled: true` first; this chart
plugs into the tunnel that install exposes.

## Quickstart

The full walkthrough — Auth0/Okta setup, ingress shape, troubleshooting
— lives at [`docs/setup/agent-onboarding.md`](https://github.com/gnana997/periscope/blob/main/docs/setup/agent-onboarding.md).
The short version:

```sh
# 1. On the central server, mint a single-use 15-min bootstrap token
#    via the SPA's "+ Onboard cluster" button OR:
TOKEN=$(curl -sX POST https://periscope.your-corp.com/api/agents/tokens \
  -H "Cookie: periscope_session=<your-session>" \
  -H "Content-Type: application/json" \
  -d '{"cluster":"prod-eu"}' | jq -r .token)

# 2. On the managed cluster, install the agent
helm upgrade --install periscope-agent \
  oci://ghcr.io/gnana997/charts/periscope-agent \
  --version <X.Y.Z> \
  --namespace periscope --create-namespace \
  --set agent.serverURL=wss://agents.periscope.your-corp.com:8443 \
  --set agent.clusterName=prod-eu \
  --set agent.registrationToken=$TOKEN
```

Within seconds the cluster's card on the central server's fleet
view flips to **healthy**.

## What the chart installs

- **`Deployment`** running the `periscope-agent` binary (single
  replica — tunnel sessions are 1:1 with agent pods)
- **`ServiceAccount`** with a namespace-scoped `Role` for managing
  the agent's persisted state Secret
- **`ClusterRole`** + binding granting the agent SA cluster-wide
  `get/list/watch` plus `impersonate` on users + groups (the lever
  that makes per-user RBAC enforcement possible — the central
  server forwards `Impersonate-User` headers through the tunnel
  and the local apiserver evaluates RBAC against the human, not
  the agent SA)
- **Bootstrap-token Secret** (one-shot — safely deleted after
  registration succeeds; the persisted mTLS cert lives in a
  separate `helm.sh/resource-policy: keep` Secret managed by the
  agent itself)

## Required values

| Value | Purpose |
|---|---|
| `agent.serverURL` | Central tunnel URL (`wss://...:8443`). |
| `agent.clusterName` | Cluster name as registered with the central server. Must match a `clusters[].name` entry of `backend: agent`. |
| `agent.registrationToken` | Bootstrap token from the central server's token endpoint. Single-use, 15-min TTL. |

The `values.schema.json` enforces all three at install time so
typos surface immediately, not at pod-start.

## Optional values for split / self-signed setups (#48)

| Value | Purpose |
|---|---|
| `agent.registrationURL` | URL for the unauth registration POST. Set when central server splits HTTP and mTLS onto different load balancers (ALB+NLB topology). Empty = derive from `serverURL` via wss/ws → https/http translation. |
| `agent.serverCAHash` | SPKI hash for kubeadm-style pinning on the registration TLS dial. Format: `sha256:<64 hex chars>`. Bootstraps the agent against a self-signed central server endpoint without distributing the full CA bundle. |

Three deployment topologies — pick the one matching your central
server's LB shape:

- **Single LB, public cert** — set `serverURL` only. Agent does
  standard chain validation against system roots.
- **Split LBs (ALB + NLB)** — set `serverURL` + `registrationURL`.
  Agent registers via the public-cert ALB, tunnels via the NLB.
- **Single LB, self-signed** — set `serverURL` + `serverCAHash`.
  Agent SPKI-pins the registration dial, falls back to standard
  chain validation for the tunnel after registration.

Full walkthrough with worked examples for each topology:
[`docs/setup/agent-onboarding.md`](https://github.com/gnana997/periscope/blob/main/docs/setup/agent-onboarding.md).

## Security model

- The agent's mTLS cert (signed by the central server's per-deployment
  CA on registration) is the long-lived identity. Anyone with `secrets/get`
  on the agent's namespace can pull it; the chart's RBAC limits broader access.
- The bootstrap token authorises **registration for one cluster name**
  and burns on attempt — leakage gets you exactly that one registration,
  not data access.
- Agent's default `ClusterRole` is `get/list/watch + impersonate`. Apply,
  delete, exec verbs flow through the human user's impersonated identity
  — the agent itself never holds write power beyond what its cluster RBAC
  allows. Tighten or widen via `clusterRole.enabled: false` and your own
  binding.

For the deeper model walkthrough see
[issue #41](https://github.com/gnana997/periscope/issues/41).

## See also

- [`docs/setup/agent-onboarding.md`](https://github.com/gnana997/periscope/blob/main/docs/setup/agent-onboarding.md)
  — operator guide with full troubleshooting
- [`examples/agent/`](https://github.com/gnana997/periscope/tree/main/examples/agent)
  — sample values files + reference `register-and-install.sh` script
- [Periscope server chart](https://artifacthub.io/packages/helm/periscope/periscope)
  — what runs on the central cluster

## License

[Apache 2.0](https://github.com/gnana997/periscope/blob/main/LICENSE).
