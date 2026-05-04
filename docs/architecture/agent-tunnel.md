# Agent tunnel

How Periscope reaches a managed cluster's apiserver when the cluster is
registered with `backend: agent` (#42). Companion to
[`docs/setup/agent-onboarding.md`](../setup/agent-onboarding.md), which
is the operator-facing how-to. This page is the design walkthrough for
contributors and reviewers.

## 1. The problem this solves

Pre-#42, Periscope reached managed clusters by holding cloud
credentials (`backend: eks` via Pod Identity / IRSA + `eks:GetToken`)
or kubeconfig (`backend: kubeconfig`). Both place the network and
auth burden on the central server:

- The server needs IAM trust into every managed cluster's account.
- Every cluster needs an EKS Access Entry (or `aws-auth` edit) and
  the chart's RBAC manifests applied separately.
- Cross-cloud / on-prem / cross-account targets are out of reach
  without VPN / VPC peering / cloud-specific glue.

The agent backend inverts the connection direction: a tiny
`periscope-agent` pod runs on the managed cluster and dials *out* to
the central server over a long-lived WebSocket. Outbound HTTPS is
the only network requirement — the dial direction is the same one
`kubectl` uses from a developer laptop, so anything that allows a
laptop to reach the central server allows an agent to.

The design discussion that picked this shape (over central-IAM
extensions and over GitOps-style pull models like OCM) lives in
[issue #41](https://github.com/gnana997/periscope/issues/41).

## 2. Topology

```
                 central cluster                              managed cluster
   ┌─────────────────────────────────────┐         ┌─────────────────────────────┐
   │                                     │         │                             │
   │  ┌──────────────────────────────┐   │         │  ┌────────────────────┐     │
   │  │   periscope (server pod)     │   │         │  │  periscope-agent   │     │
   │  │                              │   │         │  │       pod          │     │
   │  │  HTTP router :8080           │   │         │  │                    │     │
   │  │   ├ /api/clusters/* etc      │   │         │  │  in-cluster client │     │
   │  │   ├ /api/agents/tokens       │◀──┼── HTTPS │  │       (SA token)   │     │
   │  │   └ /api/agents/register     │   │         │  │           │        │     │
   │  │                              │   │         │  │           ▼        │     │
   │  │  TLS listener :8443          │◀══╪═════════╪══╡ wss://…/connect    │     │
   │  │   └ /api/agents/connect      │   │  mTLS   │  │   (long-lived WS)  │     │
   │  │                              │   │  WSS    │  │                    │     │
   │  │  internal/tunnel.Server      │   │         │  │  ┌────────────┐    │     │
   │  │   ├ remotedialer wrap        │◀──┼─dial──▶ │──│  apiserver   │    │     │
   │  │   ├ session map (name→Sess)  │   │  via    │  │  (in-cluster)│    │     │
   │  │   └ MTLSAuthorizer           │   │  tunnel │  └──────────────┘    │     │
   │  │                              │   │         │                      │     │
   │  └──────────────────────────────┘   │         │                      │     │
   │            ▲                        │         │                      │     │
   │            │  rest.Config.Transport │         └──────────────────────┘     │
   │            │  = tunnel.RoundTripper │                                       │
   │  ┌──────────────────────────────┐   │
   │  │  per-resource handlers       │   │
   │  │   (apply, list, watch, …)   │   │
   │  └──────────────────────────────┘   │
   │                                     │
   └─────────────────────────────────────┘
```

Two listening ports on the server:

- **`:8080`** (the existing one). Browser traffic, JSON APIs, the
  registration HTTP flow. Lives behind whatever ingress fronts the
  rest of Periscope (ALB, nginx-ingress, etc.).
- **`:8443`** (new). Dedicated TLS listener with
  `ClientAuth: RequireAndVerifyClientCert`. Hosts only
  `/api/agents/connect`. Must NOT be fronted by an HTTP-terminating
  load balancer (ALB strips client certs). Operators wire NLB / TCP
  LB / TLS-passthrough Ingress for this port; the smoke flow uses
  `kubectl port-forward`.

Both ports run inside the same pod — no second deployment.

## 3. The tunnel layer

`internal/tunnel/` wraps [`rancher/remotedialer`](https://github.com/rancher/remotedialer)
(Apache-2.0). Why remotedialer:

- Production-tested at Rancher scale (thousands of agents).
- Tunnels arbitrary TCP, so HTTP / SSE / WebSocket-upgrade all work
  without per-protocol code.
- The unobvious problems (back-pressure, reconnect, proxy/keepalive
  interop) are already absorbed.

The wrap exists so consumers never see `remotedialer` types — if we
ever swap transports (gRPC bidi, raw TCP over SSH), the public API
of `internal/tunnel` stays the same. See
[`internal/tunnel/doc.go`](../../internal/tunnel/doc.go) for the
public API contract.

Two halves:

- **`tunnel.Server`** — accepts WebSocket upgrade requests at
  `/api/agents/connect`, validates the agent's identity (via the
  pluggable `Authorizer` hook so #42b's mTLS validator slots in),
  tracks connected sessions in a `name → Session` map. Handlers
  reach into the map via `LookupSession(name)` or
  `DialerFor(name)`.

- **`tunnel.Client`** — agent-side dial-out, jittered exponential
  reconnect, per-cluster name claim header, mTLS handshake.

## 4. PKI lifecycle

Per-deployment ECDSA P-256 CA. Generated once on first server start,
loaded from the same on-disk material on every restart. ECDSA over
RSA: smaller, faster signing, modern best practice. P-256 is what
WebPKI uses; cert tooling (cosign, openssl, kubectl) handles it
without configuration.

Why a single CA over per-cluster CAs: makes validation a single
trust anchor and keeps the key material surface tiny. We sacrifice
the ability to revoke "all certs for cluster X" by burning the
issuer (we'd burn every cluster's cert instead). For v1.x.0 this
is the right tradeoff; per-cluster issuers can layer in additively
when threat model demands.

The CA lives in a K8s Secret in the server's namespace
(default name `periscope-agent-ca`). The chart pre-creates it
empty, gives the server SA `get/update` (resource-name-restricted)
RBAC, and the server fills the data on first boot. Operators with
their own PKI can pre-populate `ca.crt` + `ca.key`; the server
treats both-keys-present as load-only.

Three cert kinds the CA mints:

| Cert | EKU | CN | Where lives |
|---|---|---|---|
| **CA itself** | `cert sign` | `periscope-agent` | `periscope-agent-ca` Secret on the central cluster |
| **Server cert** | `serverAuth` | `periscope-server`, SANs from `agent.tunnelSANs` | In-memory (re-minted on every server restart, so SAN changes pick up automatically) |
| **Agent client cert** | `clientAuth` | the registered cluster name | `periscope-agent-state` Secret on the managed cluster |

Default validity: 90 days for client + server, 10 years for the CA.
Auto-rotation is a v1.x.+ follow-up; for now operators re-register
agents whose certs expire (no client outage; the agent surfaces the
expiry as a connect failure with a clear log line).

## 5. Registration handshake

Three parties: operator (admin tier), central server, agent. Two
endpoints:

```
                      operator                  server                  agent
                         │                       │                       │
   1. mint token         │  POST /tokens         │                       │
                         │ {cluster}             │                       │
                         │──────────────────────▶│                       │
                         │ {token, expiresAt}    │                       │
                         │◀──────────────────────│                       │
                         │                       │                       │
   2. install agent      │  helm install --set token=...                 │
                         │──────────────────────────────────────────────▶│
                         │                       │                       │
   3. register           │                       │  POST /register       │
                         │                       │ {token, name, csr}    │
                         │                       │◀──────────────────────│
                         │                       │ {cert, caBundle}      │
                         │                       │──────────────────────▶│
                         │                       │                       │
   4. dial tunnel        │                       │  WSS /connect          │
                         │                       │  (mTLS w/ client cert) │
                         │                       │◀══════════════════════ │
                         │                       │   session live         │
                         │                       │                       │
```

**Step 1.** The operator (admin tier) hits `POST /api/agents/tokens`
with `{"cluster": "<name>"}`. The server mints a 32-byte random
opaque token, base64url-encoded, with TTL 15 min and bound to the
named cluster. Stored in a `tunnel.TokenStore` (in-memory single-
writer for v1.x.0; Postgres-backed for HA in v1.x.+). Returns
`{token, cluster, expiresAt}`.

**Step 2.** The operator runs `helm install periscope-agent ...
--set agent.registrationToken=<token>` on the managed cluster.

**Step 3.** The agent boots, finds no persisted state Secret,
generates an ECDSA P-256 keypair locally, builds a CSR with
`CN=<cluster>`, `POST /api/agents/register` with
`{token, cluster, csr}`. The server:

1. Redeems the token (atomic: validates + marks consumed in one mutex
   acquisition). Wrong cluster name **burns the token** — a
   wrong-guess attempt costs the operator a fresh mint.
2. If valid: signs the CSR (CN overwritten with the cluster name from
   the redeemed token, EKU = `clientAuth`), returns
   `{cert, caBundle, expiresAt}`. The agent persists into the
   `periscope-agent-state` Secret.

All token failures (`unknown` / `expired` / `consumed` / `cluster mismatch`)
collapse to a uniform `401 "registration rejected"` so an attacker
probing the endpoint can't distinguish failure modes. The server-side
log line carries the real reason for forensics.

**Step 4.** The agent now has its long-lived identity. Opens
`wss://<server>:8443/api/agents/connect` presenting the mTLS client
cert. The server's TLS listener is configured
`ClientAuth: RequireAndVerifyClientCert` against the same CA, so
cert chain validation happens at the TLS layer; if it passes, the
HTTP handler reads `r.TLS.PeerCertificates[0].Subject.CommonName`
to identify the cluster.

The handler is `tunnel.MTLSAuthorizer.Authorize` (defense-in-depth
also checks the cert carries `ExtKeyUsageClientAuth`, calls the
operator's `NameAllowed` callback to gate on registry membership),
plugged into `tunnel.ServerOptions.Authorizer`. Returning `true`

### 5.1 LB topology and bootstrap trust (#48)

The two server endpoints — `:8080` for the HTTP API including
`/api/agents/register`, and `:8443` for `/api/agents/connect`
(mTLS-required) — naturally map to two different load balancers in
production. The agent's bootstrap flow needs to dial both, which
forces a design decision about how the agent knows which URL to
use for which call.

The agent supports three deployment topologies controlled by two
chart values:

| Topology | `agent.serverURL` | `agent.registrationURL` | `agent.serverCAHash` |
|---|---|---|---|
| **A** — single LB, public cert | `wss://periscope.example.com:8443` | _unset_ (derived) | _unset_ |
| **B** — split LBs (ALB + NLB) | `wss://agents.periscope.example.com:8443` | `https://periscope.example.com` | _unset_ |
| **C** — single LB, self-signed | `wss://periscope.example.com:8443` | _unset_ (derived) | `sha256:...` |

When `registrationURL` is unset, the agent derives the registration
URL from `serverURL` by translating `wss://` → `https://` and
`ws://` → `http://`. This is the right behaviour for Topology A and
C (single LB) and the wrong behaviour for B (the derived URL would
hit the mTLS endpoint, which rejects unauth POSTs). Topology B
operators set `registrationURL` explicitly to point at the public
ALB.

When `serverCAHash` is set, the agent does kubeadm-style
SubjectPublicKeyInfo pinning (RFC 7469) on the registration TLS
dial — `tls.Config{InsecureSkipVerify: true, VerifyPeerCertificate: ...}`
that compares the SHA-256 of the leaf cert's SPKI against the
configured hash. The pin replaces standard chain validation
exclusively for the registration dial; once registration succeeds,
the agent has the server's CA bundle and uses standard chain
validation for the long-lived tunnel. SPKI (not full-cert) hashing
means cert rotation that preserves the key doesn't break the pin.

The operator-facing how-to with topology examples lives in
[`docs/setup/agent-onboarding.md`](../setup/agent-onboarding.md).

admits the WebSocket session keyed by the cluster name.

## 6. mTLS handshake + session lifecycle

Once the WebSocket is up, remotedialer takes over: the agent and
server multiplex an arbitrary number of TCP streams over the single
WS connection. The server side of the multiplexer exposes a
`Dialer(clientKey)` that opens a new tunneled net.Conn to whatever
address the server requests; the agent side fulfils that dial by
calling its registered `LocalDialer` (which dials the local
apiserver — see 7).

Session state on the server:

- A `sync.Map` from cluster name to `*remotedialer.Session`,
  managed by remotedialer itself.
- A separate `connected map[string]time.Time` we maintain
  alongside, plus an `Observer` callback fired on connect /
  disconnect — used by the fleet view's "connected since"
  indicator and (future) Prometheus metrics.

Disconnect detection is currently a 2s polling loop on
`remotedialer.HasSession(name)` (see `tunnel/server.go::watchDisconnect`).
remotedialer doesn't expose a public disconnect channel; polling
is cheap (one map lookup behind a RWMutex) and the 0–2s lag on
the fleet view's "disconnected" badge is fine.

## 7. Transport substitution — why handlers don't need to change

This is the load-bearing trick that makes the agent backend additive
rather than a rewrite.

`internal/k8s/client.go::buildRestConfig` is the single place every
handler's clientset originates from. It switches on `c.Backend` and
delegates to a per-backend builder. The new builder
(`internal/k8s/agent_transport.go::buildAgentRestConfig`) returns a
`*rest.Config` whose `Host` is a sentinel
(`https://apiserver.<cluster>.tunnel`, never resolved) and whose
`Transport` is set to a tunnel-bound `http.RoundTripper`.

The RoundTripper is `tunnel.NewRoundTripper(dial, opts)`, where
`dial` is the agent's session dialer. It wraps a stdlib
`http.Transport` whose `DialContext` opens connections via the
tunnel instead of the host network. From the clientset's
perspective, every request to `https://apiserver.<cluster>.tunnel/api/v1/pods`
just becomes a TCP connection followed by standard HTTP framing —
the bytes happen to flow through a multiplexed WebSocket instead
of a direct apiserver socket, but neither the clientset nor the
handler that built it cares.

Consequence: handlers for apply / delete / list / watch / can-i /
fleet / helm / audit work on agent-backed clusters with **zero
changes**. The watch SSE handler reads from a channel fed by
`watch.Watch()`; the underlying GET to `/api/v1/watch/pods` flows
through the tunnel just like any other request.

The lookup hook (`k8s.SetAgentTunnelLookup`) is a package-level
function variable installed by `cmd/periscope/main.go` at startup;
tests substitute their own. The default refuses with a clear
`"SetAgentTunnelLookup not called"` error so a misconfigured
build never silently accepts agent-backed entries.

## 8. Identity propagation (post-#59)

Every Periscope handler talks to apiserver as the human user via
`Impersonate-User` / `Impersonate-Group` headers (see
[RFC 0002](../rfcs/0002-auth.md)). With the agent backend the
chain becomes:

```
browser (alice)
   │ session cookie
   ▼
periscope handler
   │ rest.Config.Impersonate.UserName = "alice@corp"
   ▼
http.Client built from rest.Config
   │ outgoing request:
   │   Impersonate-User: alice@corp
   │   Impersonate-Group: periscope-tier:admin
   │   (no Authorization — added by agent)
   ▼
tunnel.NewRoundTripper
   │ TCP frame (HTTP bytes) into WS multiplexer
   ▼
remotedialer Session
   │ WS frame (TCP bytes) over wss://
   ▼
periscope-agent's local HTTP reverse proxy (cmd/periscope-agent/proxy.go)
   │ adds Authorization: Bearer <agent SA token>
   │ preserves Impersonate-* headers
   │ forwards as HTTPS with kubelet-mounted apiserver CA
   ▼
apiserver in managed cluster
   │ authenticates: agent's ServiceAccount
   │ checks: SA has "impersonate" verb? ✓ (chart's ClusterRole)
   │ re-evaluates as alice@corp + admin group
   │ runs the verb under alice's RBAC
```

The agent talks to apiserver as **its own SA**. The apiserver
authenticates the agent via the SA token (kubelet-mounted into the
agent pod) and authorises **the agent's RBAC** (read + impersonate
by default). But for the actual K8s call, the apiserver evaluates
the Impersonate headers — so the verb runs as `alice`, audit-logged
with `alice`'s identity, denied if `alice` lacks the verb. The
agent's RBAC is the ceiling for what's physically possible; per-call
authorization is the human's RBAC.

This is the same impersonation model as the `eks` and `kubeconfig`
backends, just with a different bridge identity:

| Backend | Bridge identity | Auth credential | Origin |
|---|---|---|---|
| `eks` | EKS-mapped K8s user | EKS bearer token | minted per-request via `eks:GetToken` |
| `in-cluster` | host pod's SA | kubelet-mounted SA token | host pod's `/var/run/secrets/...` |
| `agent` | agent pod's SA | kubelet-mounted SA token | **agent** pod's `/var/run/secrets/...` |

### Why the local HTTP proxy (architectural note)

Pre-#59 the agent forwarded raw TCP bytes from the tunnel directly
to the apiserver. The server's HTTP request (built with no
Authorization header — only Impersonate-*) reached the apiserver
unauthenticated, the apiserver rejected it with 401/403 before
impersonation was even consulted, and every agent-backed cluster
appeared `denied` in the fleet view.

The fix: the agent runs a tiny `httputil.ReverseProxy` on
`127.0.0.1:7443`. The tunnel's `localDial` routes there instead of
to the apiserver. The proxy injects the agent's SA bearer token,
preserves Impersonate-* headers, and re-issues each request to the
apiserver over HTTPS using the kubelet-mounted CA bundle. TLS now
terminates at the proxy (not end-to-end through the tunnel), so the
server-side `rest.Config.Host` switched to `http://apiserver.<c>.tunnel`
(plain HTTP between server and tunnel; the unencrypted hop is in-
process bytes, never touching the network).

The proxy:
- Always overwrites the inbound `Authorization` header (defensive —
  closes the "compromised central server tries to substitute its
  own token" hole)
- Strips `X-Forwarded-*` headers `httputil.ReverseProxy` adds by
  default (apiserver doesn't need them; they leak tunnel internals
  into the apiserver's audit log)
- Sets `FlushInterval=-1` so SSE / watch / logs streaming flushes
  immediately (otherwise responses buffer and the SPA's watch streams
  stall)

## 9. Audit shape

Audit (RFC 0003) emits server-side, in the same handlers, with no
backend awareness. The `cluster:` field on the audit row is set
from the registry entry's name — same source for every backend.

When the tunnel is the transport, the row still contains the
human user (`actor.sub`), the verb, the outcome, and `cluster:
<name>`. Operators querying `/api/audit?cluster=prod-eu` get the
same shape regardless of how `prod-eu` is reached.

A future `clusterBackend: agent` field on audit rows is being
considered for the v1.x.+ audit RFC update, so SIEM consumers can
filter by transport type. Additive change to RFC 0003 6, no wire-
breaking impact.

## 10. Failure modes

| What happens | What the user sees | Recovery |
|---|---|---|
| Agent disconnects mid-request (network blip) | The `tunnel.RoundTripper` returns an error wrapped with `ErrAgentDisconnected`; the handler surfaces it as the standard upstream error class. SPA shows "cluster transient error, retry." | remotedialer reconnects with jittered backoff (1s → 30s); next request goes through. |
| Cert expires unattended | Agent's reconnect attempts fail with `bad certificate`. Cluster goes "unreachable" in fleet view. | Operator deletes the agent's state Secret, mints a fresh token, re-installs. (90d default lifetime; auto-rotation is a v1.x.+ follow-up.) |
| Server restart | All agent connections drop. Agents reconnect on jittered backoff; sessions re-register, fleet view re-populates within ~10s. | Automatic. |
| WS idle through corp proxy | Connection closes silently, agent reconnect kicks in. | remotedialer's keepalive frame keeps idle proxies happy; default <30s ping. |
| Wrong-cluster registration attempt | Token redemption returns `cluster mismatch`; token burns; HTTP returns uniform 401. Server log carries the real reason. | Operator mints fresh token with the correct cluster name. |
| Server CA rotated (e.g. Secret deleted + regenerated) | Every existing agent's cert chain breaks. All agent-backed clusters go unreachable. | Re-register every agent (delete state Secret, fresh token, re-install). The chart's `helm.sh/resource-policy: keep` on the CA Secret is meant to prevent this; deleting the Secret is an explicit operator decision. |
| Agent's K8s SA loses RBAC | Local apiserver dial succeeds, but the actual K8s call fails with `403 forbidden` from apiserver. Same as any RBAC error. | Restore the agent's `ClusterRole` / fix the binding. |
| Operator deregisters cluster from `clusters[]` while agent is still connected | `MTLSAuthorizer.NameAllowed` returns false on next reconnect; the agent's tunnel is rejected. The previously-issued cert is still cryptographically valid until expiry but no longer admitted. | Either re-add the entry or `helm uninstall periscope-agent` on the managed cluster. |

## 11. What's intentionally not here

- **Exec.** Pod exec is a SPDY/WebSocket bidirectional upgrade
  from the apiserver. In principle remotedialer carries it
  transparently — Rancher uses it for `kubectl exec` — but a
  direct integration test on Periscope's exact exec plumbing
  hasn't been done. v1.x.0 disables exec for agent-backed
  clusters at the SPA layer (the cluster card greys out the
  "Open Shell" button). v1.x.1 carries the focused POC + flip;
  see [#43](https://github.com/gnana997/periscope/issues/43).

- **HA peer routing.** v1.x.0 is single-replica; documented
  ~100-cluster ceiling per server pod (Rancher's empirically
  defensible number). Multi-replica with peer routing
  (request lands on replica A, agent's session is on replica B,
  replicas forward) lands in v1.5+.

- **Runtime cluster-registry mutations.** The "+ Onboard cluster"
  modal mints a token but assumes the operator has already added
  `backend: agent` to `clusters[]` in YAML and `helm upgrade`d
  the central server. Dynamic registry overlay (Secret-backed,
  merged with YAML) is a v1.x.1 follow-up; tracked in the same
  v1.x.0 epic [#42](https://github.com/gnana997/periscope/issues/42)
  as Phase 2.

- **Cert auto-rotation.** Agent re-registration is operator-driven
  in v1.x.0 (90-day cert, manual mint + re-install). Auto-rotate-
  at-2/3-lifetime is a small follow-up — the agent already has the
  in-cluster RBAC to update its own state Secret.

- **Audit-buffer-on-disconnect.** Agent doesn't emit audit; all
  audit emission stays server-side. So a dropped tunnel means
  "no actions are taken on that cluster," not "actions taken but
  audit lost." Documented out-of-band SIEM shipping
  (Fluent Bit → S3) is the recommended belt-and-suspenders pattern
  for the central server's stdout audit stream.

- **Per-tunnel rate limits.** A buggy or malicious agent could
  open many concurrent dials through its tunnel. v1.x.0 relies on
  the apiserver's own rate limits. Per-tunnel watch / connection
  caps are tracked for v1.x.+.

- **Multi-tenant shared CA.** Every Periscope deployment has its
  own CA; agents from one deployment cannot register against
  another. Cross-deployment trust (multiple Periscope servers
  sharing one CA, or fleet-of-fleets shapes) is not designed for.

## 12. Related code

Server side:
- [`internal/tunnel/`](../../internal/tunnel/) — tunnel package, public
  API in `doc.go`
- [`cmd/periscope/agent_handler.go`](../../cmd/periscope/agent_handler.go) —
  `registerAgentTunnel`, CA bootstrap, route mounting, listener
- [`internal/k8s/agent_transport.go`](../../internal/k8s/agent_transport.go) —
  `BackendAgent` clientset construction
- [`internal/clusters/cluster.go`](../../internal/clusters/cluster.go) —
  `BackendAgent` constant + registry validator case

Agent side:
- [`cmd/periscope-agent/`](../../cmd/periscope-agent/) — agent binary

Packaging:
- [`Dockerfile.agent`](../../Dockerfile.agent) — agent image
- [`deploy/helm/periscope-agent/`](../../deploy/helm/periscope-agent/) —
  agent chart
- [`deploy/helm/periscope/templates/agent-{rbac,ca-secret,tunnel-service}.yaml`](../../deploy/helm/periscope/templates/) —
  server-side chart additions for the agent feature

Operator-facing:
- [`docs/setup/agent-onboarding.md`](../setup/agent-onboarding.md) —
  the how-to
- [`examples/agent/`](../../examples/agent/) — sample values + reference script

Background:
- [Issue #41](https://github.com/gnana997/periscope/issues/41) —
  design discussion (agent-vs-central-IAM)
- [Issue #42](https://github.com/gnana997/periscope/issues/42) —
  v1.x.0 multi-cluster epic
- [Issue #43](https://github.com/gnana997/periscope/issues/43) —
  exec POC follow-up
