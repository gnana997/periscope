# Troubleshooting

A symptom-keyed index for things that surface during Periscope
installs, upgrades, and day-2 operation. Most feature-specific
issues are documented inline with the feature — this page indexes
them so an operator can grep by what they saw, plus adds
cross-cutting items (chart-versions OOM, network changes, scanner
false-positives) that don't fit a single feature doc.

If you don't find your symptom here, the most diagnostic single
file is the Periscope pod's slog stream — every privileged action
gets a structured line, and every request carries an
`X-Request-Id` that grep-joins to the matching audit row.

---

## Quick lookup

| Symptom | Where |
|---|---|
| "Open Shell" button missing on a pod page | [pod-exec.md §8](pod-exec.md#troubleshooting) |
| Shell button missing on a cluster page header | [cluster-shell.md §9](cluster-shell.md#9-troubleshooting) |
| WebSocket upgrade fails / instant disconnect | [pod-exec.md](pod-exec.md#websocket-upgrade-fails-instant-disconnect) |
| `E_FORBIDDEN` on cluster-shell click | [cluster-shell.md §9](cluster-shell.md#9-troubleshooting) |
| Shell session pod stays `Pending` >30s | [cluster-shell.md §9](cluster-shell.md#9-troubleshooting) — also see [below](#cluster-shell-pod-stuck-pending-behind-an-outbound-proxy) |
| Helm command not in `cluster_shell_close.commands[]` | Upgraded? On v1.1.5+ helm is audited; pre-v1.1.5 it isn't ([CHANGELOG](../../CHANGELOG.md)) |
| Tier user gets 403 on everything | [cluster-rbac.md](cluster-rbac.md#common-pitfalls) |
| `groups` claim missing / empty | [okta.md](okta.md#common-pitfalls) / [auth0.md](auth0.md#common-pitfalls) |
| Login bounces to IdP forever | [okta.md](okta.md#common-pitfalls) / [auth0.md](auth0.md#common-pitfalls) |
| Refresh fails after N hours | [okta.md](okta.md#common-pitfalls) / [auth0.md](auth0.md#common-pitfalls) |
| `Callback URL mismatch` / `Sign-in redirect URI mismatch` | [okta.md](okta.md#common-pitfalls) / [auth0.md](auth0.md#common-pitfalls) |
| Audit page shows nothing / `auditEnabled: false` | [audit.md](audit.md#troubleshooting) |
| `X-Audit-Scope: self` for an admin user | [audit.md](audit.md#troubleshooting) |
| Audit DB grows past `maxSizeMB` | [audit.md](audit.md#troubleshooting) |
| SSE drops every ~60 seconds | [watch-streams.md §7](watch-streams.md#7-troubleshooting) |
| Watch stream returns 404 immediately | [watch-streams.md §7](watch-streams.md#7-troubleshooting) |
| Watch stream cap reached | [watch-streams.md §7](watch-streams.md#7-troubleshooting) |
| Page updates feel polled, not live | [watch-streams.md §7](watch-streams.md#7-troubleshooting) |
| EKS "Drift not computed" | [eks-upgrade-readiness.md](eks-upgrade-readiness.md#troubleshooting) |
| EKS Upgrade Insights "load failed" | [eks-upgrade-readiness.md](eks-upgrade-readiness.md#troubleshooting) |
| Agent registration `tls: certificate required` | [agent-onboarding.md](agent-onboarding.md#troubleshooting) |
| Agent registration `SPKI pin mismatch` | [agent-onboarding.md](agent-onboarding.md#troubleshooting) |
| Agent registration `registration rejected` (HTTP 401) | [agent-onboarding.md](agent-onboarding.md#troubleshooting) |
| Agent connects, then drops every few seconds | [agent-onboarding.md](agent-onboarding.md#troubleshooting) |
| Cluster card stuck "unreachable" | [agent-onboarding.md](agent-onboarding.md#troubleshooting) |
| Periscope pod OOMKilled when listing chart versions | [below](#periscope-pod-oomkilled-when-listing-helm-chart-versions) |
| Artifact Hub flags HIGH/MEDIUM CVE on a clean image | [below](#artifact-hub-shows-highmedium-cve-that-upstream-rates-low) |
| Agent tunnel reconnect churn after the central LB changes IP | [below](#agent-tunnel-reconnect-churn-after-the-central-lb-rotates-ip) |
| Local microk8s: TLS cert error after switching networks | [below](#microk8s-apiserver-tls-cert-mismatch-after-switching-networks) |

---

## Production gotchas

### Periscope pod OOMKilled when listing helm chart versions

**Symptom**: SPA fetches `/api/clusters/{c}/helm/chart/versions`
against a public helm repo with a large `index.yaml`
(`prometheus-community`, `bitnami`, `ingress-nginx`, etc.) and
the Periscope pod restarts. `kubectl describe` shows
`Last State: Terminated, Reason: OOMKilled, Exit Code: 137`.

**Cause**: the handler reads the entire `index.yaml` into memory
and `yaml.Unmarshal`s it in one shot. For `prometheus-community`
(5–10 MB index.yaml), the burst hits ~600–700 MiB RSS — well past
the chart default `resources.limits.memory: 512Mi`. The pod is
OOM-killed mid-request; ingress-nginx surfaces this as a 503.

**Fix**: bump the memory limit while a streaming-parse follow-up
lands. 1Gi is comfortable headroom for every public helm repo we've
benchmarked:

```yaml
# values.yaml
resources:
  requests:
    memory: 256Mi
  limits:
    memory: 1Gi
```

Apply with `helm upgrade periscope ... --reuse-values --set resources.limits.memory=1Gi`.

**Notes**:

- The 5-minute `chartVersionsCache` only helps the *second* request —
  first fetch after pod start still pays the full burst.
- Affects all backends equally (in-cluster, agent, eks, kubeconfig).
- Steady-state Periscope RSS is ~200 MiB; the bump is for the chart-
  versions burst alone.
- The streaming-parse fix tracks under the helm-chart-versions
  endpoint refactor — once it lands, the 1Gi bump can revert.

---

### Cluster-shell pod stuck Pending behind an outbound proxy

**Symptom**: Operator clicks the **shell** button. The drawer opens,
the WebSocket connects, but the terminal sits at "starting…" for
>30 seconds. `kubectl -n periscope-system describe pod periscope-shell-*`
shows `Failed: ErrImagePull` / `ImagePullBackOff` against
`ghcr.io/gnana997/periscope-shell:<version>`.

**Cause**: the cluster's container runtime can't reach `ghcr.io` —
typically because the cluster is air-gapped, the egress runs through
a corporate proxy that doesn't allow `ghcr.io`, or the proxy needs
auth not configured on the kubelet.

**Fix**: mirror the shell image into the registry the cluster *can*
reach, then point the chart at it:

```yaml
# Server chart (values.yaml)
clusterShell:
  enabled: true
  image:
    repository: registry.internal.acme.com/periscope/periscope-shell
    tag: v1.1.5  # match your installed periscope version
    pullPolicy: IfNotPresent

# Agent chart (per managed cluster) — only needed if you use the
# agent backend and the kubelet on managed clusters also can't reach
# ghcr.io. Same value, applied separately because the agent and
# server can be sized for different network postures.
```

The shell image is opt-in (default `clusterShell.enabled: false`),
so operators who don't use cluster shell never pull it. Once
mirrored, operators on locked-down clusters get the same in-browser
experience without punching a hole through their egress policy.

**Notes**:

- The `clusterShell.podStartTimeoutSeconds` (default 30s) determines
  how long Periscope waits before returning `E_SHELL_POD_TIMEOUT` —
  bump it if your mirror is slow on first pull, but the underlying
  issue is registry reachability, not timeout tuning.
- The shell image is multi-arch (linux/amd64 + linux/arm64), so a
  one-time `crane copy` or equivalent into your mirror works for both.

---

### Agent tunnel reconnect churn after the central LB rotates IP

**Symptom**: The agent log on a managed cluster shows a steady cadence
of:

```
agent.dial_attempt   server=wss://...:8443/api/agents/connect
agent.dial_failed    err=...
agent.reconnect_in   seconds=8
```

The central server's cluster registry shows the cluster as
"unreachable" or flickering between connected/unreachable.

**Cause**: the agent's `serverURL` was set at install time. If the
LB / Ingress IP behind that URL changes — e.g. AWS NLB rotates a
node IP, a hostname-based serverURL DNS-cache TTL expires and resolves
new, or the central Periscope LB was reprovisioned — there's a brief
window where the agent's cached DNS / TCP connection is wrong.

**Why it usually self-heals**: the agent's reconnect supervisor
(`internal/tunnel`) backs off `[0, 1s, 3s, 8s]` with a max 30s
ceiling and re-dials from scratch each cycle. DNS re-resolves on
each dial. A 1-2 minute window of churn is the typical signature of
an LB rotation; longer means something else broke.

**When to act**:

- **Hostname-based `serverURL`**: usually nothing to do; DNS TTL
  expires and reconnect succeeds. If your DNS TTL is unusually long
  (>5 min), consider lowering it on the Periscope server's record
  to shorten the churn window.
- **IP-based `serverURL`** (rare in production — typically dev rigs
  or temporary topologies): you'll need to `helm upgrade` the agent
  with the new IP. After upgrade the agent reads the new value from
  the rendered env, reconnects on the next dial.

**Diagnostic**: turn on agent debug logging to see the per-attempt
detail (DNS resolution, TCP error, TLS handshake) — see
[agent-onboarding.md → Turn on debug logging on the agent](agent-onboarding.md#turn-on-debug-logging-on-the-agent).

---

## Local-development gotchas

### microk8s apiserver TLS cert mismatch after switching networks

**Symptom**: After moving your laptop between networks (wifi → hotspot,
office → home, etc.), `kubectl port-forward` against a local microk8s
cluster fails with:

```
error: error upgrading connection: error dialing backend: tls: failed
to verify certificate: x509: certificate is valid for 192.168.0.6,
172.17.0.1, 172.18.0.1, ..., not 172.20.10.3
```

The new IP (`172.20.10.3` here) is wherever your laptop landed after
the network swap.

**Cause**: this is a **snap-microk8s + mobile-laptop interaction, not
a Periscope issue.** Three ingredients combine to produce it:

1. microk8s's apiserver binds to `0.0.0.0` (every interface, including
   ones that come and go with your network).
2. The apiserver TLS cert was minted at install time with the host's
   IPs *at that moment* — your wifi IP became a SAN, but your hotspot
   IP didn't exist yet.
3. microk8s runs an interface-watcher daemon that **rewrites your
   kubeconfig's `server:` line** when it sees a new primary IP. So
   kubectl now dials the new IP, the apiserver answers, and Go's
   x509 verifier rejects the cert.

Production clusters don't combine these three. Cloud-managed K8s
(EKS, GKE, AKS) reach apiserver via stable DNS, cert covers the DNS
name. kind binds to localhost only, never touches your host IPs.
k3s defaults to localhost-only kubeconfig. **Only snap-microk8s
combined with a moving laptop trips all three.**

**Fix (non-destructive, ~30 seconds of apiserver downtime, no workload
disruption)**:

```bash
sudo microk8s refresh-certs -e server.crt
```

This regenerates the apiserver cert with current host IPs in the SAN
list. Pods keep running through the brief apiserver restart; kubelets
reconnect; in-flight `kubectl` calls retry transparently.

**Durable fix (one-time setup, ~10 minutes)**: pin the apiserver to a
stable hostname your network state can't change:

1. Add a hosts entry: `echo "127.0.0.1 periscope-dev.local" | sudo tee -a /etc/hosts`
2. Edit `/var/snap/microk8s/current/certs/csr.conf.template`, add `DNS.5 = periscope-dev.local`
3. `sudo microk8s refresh-certs -e server.crt`
4. Update kubeconfig's `server:` line to `https://periscope-dev.local:16443`
5. For Periscope-agent installs, set `agent.serverURL=wss://periscope-dev.local:31410` (or whatever your tunnel port is)

After this, you can swap networks freely without breaking kubectl,
helm, or the agent tunnel.

**Note for cluster operators**: the durable fix is what production
clusters do anyway (stable hostname, cert pinned to the hostname).
A laptop dev rig is the only place this gets tricky.

**See also**: [local-dev.md](local-dev.md) if it exists, otherwise this
entry is the canonical place.

---

## Periscope-binary-side: making slog the diagnostic surface

Almost every entry on this page can be confirmed (or ruled out) by
grepping the Periscope pod's slog stream for matching keys. The
binary writes structured JSON; relevant fields per scenario:

| Scenario | What to grep |
|---|---|
| OOM during chart-versions | `helm chart versions` (handler name) + check pod `RestartCount` |
| Cluster-shell pod won't start | `cluster_shell.pod_create`, `cluster_shell.pod_wait_ready` |
| Agent tunnel issues | `tunnel.*`, `agent.session_event`, `agent.dial_*` |
| OIDC / auth issues | `auth.*`, look for `groups` field on the line |
| Watch streams | `watch.stream_open`, `watch.stream_close` |
| Audit pipeline | `audit:`, especially around startup |

All log lines carry an `X-Request-Id` that the audit pipeline also
records on the corresponding audit row — one ID grepped across the
two surfaces gives a complete end-to-end trace.

For agent-backed clusters, the same request_id appears in **both**
the central server's stdout AND the agent's stdout (when
`agent.logLevel: debug`), so an end-to-end trace across three logs
is one `grep` of the same UUID.

---

## See also

| File | What's there |
|---|---|
| [pod-exec.md](pod-exec.md) §8 | Pod exec session troubleshooting (4 entries) |
| [cluster-shell.md](cluster-shell.md) §9 | Cluster shell troubleshooting (8 entries) |
| [agent-onboarding.md](agent-onboarding.md#troubleshooting) | Agent registration + tunnel troubleshooting |
| [audit.md](audit.md#troubleshooting) | Audit pipeline issues |
| [watch-streams.md](watch-streams.md#7-troubleshooting) | SSE watch stream issues |
| [eks-upgrade-readiness.md](eks-upgrade-readiness.md#troubleshooting) | EKS Upgrade Insights / AMI drift |
| [cluster-rbac.md](cluster-rbac.md#common-pitfalls) | Tier mode / RBAC binding mistakes |
| [okta.md](okta.md#common-pitfalls) | Okta-specific OIDC mistakes |
| [auth0.md](auth0.md#common-pitfalls) | Auth0-specific OIDC mistakes |
