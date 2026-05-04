# RFC 0004 Tier 2 — exec-over-agent-tunnel e2e harness

Confirms that the full path —
`SPA → server → loopback CONNECT proxy → tunnel → agent → apiserver` —
carries an exec session end-to-end against a real apiserver. Tier 1
proves it in-process; Tier 2 proves it on a real kubelet via kind.

## Run

```sh
make poc-exec-tunnel             # cold ≈ 3-5 min, warm ≈ 60-90 s
make poc-exec-tunnel-clean       # nuke the kind cluster
```

## Topology

```
┌─────────────────────────────  kind: periscope-poc  ─────────────────────────┐
│                                                                              │
│  ns/periscope                                                                │
│  ┌─────────────────────┐         ┌─────────────────────────┐                │
│  │  periscope          │ ◀ wss   │  periscope-agent        │                │
│  │  - SPA  :8080       │ ─ http ─│  - mTLS tunnel client    │                │
│  │  - tunnel :8443     │         │  - SA token injector     │                │
│  └─────────────────────┘         └─────────────────────────┘                │
│         ▲                                  │                                 │
│         │                                  ▼                                 │
│         │                        ┌─────────────────────┐                    │
│         │                        │  kube-apiserver     │                    │
│         │                        │  (kind control plane)│                    │
│         │                        └─────────────────────┘                    │
└─────────┼────────────────────────────────────────────────────────────────────┘
          │
          ▼
   kubectl port-forward 18080:8080
          │
          ▼
   probe.go (host) — sends stdin, asserts stdout echo, asserts clean close
```

Server backend: `agent` for `kind-periscope-poc` — i.e., the server
reaches its OWN cluster's apiserver via the agent tunnel. Unusual for
production (you'd use `backend: in-cluster` for the host cluster) but
proves the agent path with a single kind.

Auth: dev mode with a static `periscope_session=dev` cookie. The dev
group is mapped to admin tier so `/api/agents/tokens` is reachable.

## What it asserts

`probe.go` opens the SPA's exec WebSocket against a busybox pod and:

1. Reads the `{type:hello}` control frame (proves the apiserver-side
   exec stream is up).
2. Sends `echo PERISCOPE-POC-OK-d4c3b2a1\n` on stdin.
3. Reads stdout until the token appears (proves stdin → apiserver →
   stdout round-trips through the tunnel).
4. Sends `{type:close}` and asserts a `{type:closed}` frame with
   `exitCode: 0` (proves clean termination).

Exits 0 on full success, non-zero with a diagnostic on any failure.

## What it doesn't assert (deferred)

- **SPDY transport**: WS is the v1.30+ default and what the SPA uses
  by default; SPDY validation lives in Tier 1's
  `TestTunnelCarriesSPDYExec`.
- **Resize**: covered by Tier 1's
  `TestTunnelCarriesWebSocketExec_Resize`.
- **Tunnel drop chaos**: covered by Tier 1's
  `TestTunnelCarriesWebSocketExec_TunnelDropMidStream`.

The Tier 2 harness focuses on the integration claim: the FULL chain
works against a real apiserver. The protocol-surface tests stay in
Tier 1 where they're cheap and run on every PR.

## Files

- `kind.yaml` — single-node kind config.
- `clusters.yaml` — Periscope cluster registry (one entry,
  `backend: agent`).
- `server-values.yaml` — periscope server helm values (dev mode auth +
  agent backend + tier mapping).
- `agent-values.yaml` — periscope-agent helm values (in-cluster
  Service URLs, registrationToken filled by `run.sh`).
- `probe.go` — exec WS client, ~150 LoC.
- `run.sh` — orchestrator. Idempotent re kind cluster lifecycle.

## When something fails

Common issues + where to look:

| Symptom | Look at |
|---|---|
| `kind create` fails | docker daemon, disk space, existing `periscope-poc` cluster |
| Server pod CrashLoopBackOff | `kubectl -n periscope logs deploy/periscope` |
| Agent never registers (`tunnel.agent_connected` log missing) | `kubectl -n periscope logs deploy/periscope-agent` — likely registrationURL or serverURL DNS / TLS issue |
| Mint endpoint returns 401/403 | server-values.yaml authorization tier mapping (dev → admin) |
| Probe times out on hello | check `kubectl -n periscope logs deploy/periscope` for exec_handler errors; the apiserver-side stream may have failed before sending hello |
| Probe exits with non-zero exitCode | shell startup issue inside busybox; rare with sleep/sh defaults |

## Future Tier 2 expansions

If the integration changes (e.g., flip `exec.enabled: true` for
`backend: agent` in the chart defaults), extend `probe.go` to also:

1. Drive a resize JSON and assert the apiserver received it (already
   tested in Tier 1 against the in-process tunnel; e2e adds the kubelet
   side).
2. Run the full WS path AND a SPDY-fallback path (force WS rejection
   server-side via env var, assert client-go falls through to SPDY).

Both add coverage but cost test time; deferred until needed.
