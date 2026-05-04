# RFC 0004 — Exec over the agent tunnel: validation harness

| | |
|---|---|
| **Status** | Draft |
| **Owner** | @gnana997 |
| **Started** | 2026-05-04 |
| **Targets** | v1.x.0 (collapse #43 into #42 if the harness passes) |
| **Related** | #42 (agent-backend epic), #43 (exec-on-agent POC), PR #46 (agent-backend implementation), RFC 0001 (Pod Exec Support), `docs/architecture/agent-tunnel.md` |

---

## 1. Summary

Issue #43 was opened as a follow-up to #42, deferring `kubectl exec` on agent-managed clusters until a 1-day POC confirmed whether bidirectional streaming flows transparently through `rancher/remotedialer`. PR #46 has since landed and **already wires the full code path**: `internal/k8s/exec.go` calls `remotecommand.NewWebSocketExecutor` / `NewSPDYExecutor` with a fallback policy, and `internal/k8s/agent_transport.go` swaps in a tunnel-bound `http.RoundTripper` for `backend: agent` clusters. The exec layer doesn't know it's tunneled — that's the whole point.

So this is no longer a "design the integration" POC. It's a **validation harness**: prove the existing path works end-to-end with both transports, under realistic chaos, against a real apiserver, and with strong enough automation that the v1.x.0 release can flip `exec.enabled: true` for `backend: agent` with confidence. If the harness passes, #43 collapses into #42 — no separate v1.x.1, no SPDY framing layer.

External evidence supporting this collapse:

- `rancher/remotedialer` exposes `Session.Dial(ctx, "tcp", addr) → net.Conn` — a TCP-level tunnel; application protocol (SPDY, WS, HTTP/2) is opaque to it.
- Rancher itself uses remotedialer in production for kubectl-shell / exec on downstream clusters.
- Kubernetes 1.30+ has WebSocket exec on by default (`TranslateStreamCloseWebsocketRequests`), and client-go ships `NewFallbackExecutor` (WS → SPDY) — which Periscope already uses (`internal/k8s/exec.go`).

---

## 2. Goals

Gates for shipping exec on `backend: agent` in v1.x.0:

1. WebSocket exec (`v5.channel.k8s.io`) survives the tunnel — stdin / stdout / stderr / resize / close all flow correctly with a TTY.
2. SPDY exec survives the tunnel — same coverage, exercised explicitly so we don't lose the fallback path silently.
3. Tunnel drop mid-exec produces a clean session close on the SPA side: no zombie session, no goroutine leak.
4. Large stdout (≥ 1 MiB), interactive stdin, and rapid resize storms don't deadlock or corrupt frames.
5. The whole thing runs hands-free in CI (Tier 1) and reproducibly on a developer laptop (Tier 2).

## 3. Non-goals

- Performance benchmarks. We're proving correctness, not chasing throughput numbers.
- Multi-replica / HA peer routing — out of scope per #42.
- A new framing layer. Expected to be unnecessary; this RFC only describes how we'd detect being wrong (see §7, Bail-out criteria).

---

## 4. Approach

Two tiers, both committed to the repo.

### 4.1 Tier 1 — In-process integration test (runs on every PR)

Single Go test file driving the real Periscope exec stack against a fake apiserver, with the real tunnel server + client running in the same process. No kind, no docker, no envtest.

**File:** `internal/k8s/exec_tunnel_test.go` (new).

Topology in one process:

```
ExecPod (real)
  → buildAgentRestConfig (real, internal/k8s/agent_transport.go)
  → tunnel.NewRoundTripper (real, internal/tunnel/transport.go)
  → tunnel.Server.DialerFor (real, internal/tunnel/server.go)
       │ (multiplex over websocket loopback)
  → tunnel.Client (real, internal/tunnel/client.go)
  → fakeAPIServer (httptest.Server) speaking the exec subprotocol
```

**fakeAPIServer responsibilities:**

- Accept `POST /api/v1/namespaces/{ns}/pods/{pod}/exec?...&command=...` and switch on the requested subprotocol header.
- For `v5.channel.k8s.io`: perform the WebSocket upgrade (using `gorilla/websocket`, already a direct dep) and run a tiny scripted shell that:
  - echoes whatever lands on STDIN (channel 0) back on STDOUT (channel 1)
  - emits a known stderr line on channel 2 when it sees a magic stdin byte
  - acknowledges resize frames (channel 4) by echoing the new dims on stdout
  - honours the close signal (channel 255) and returns exit 0 via the error channel (3)
- For `v4.channel.k8s.io` (SPDY): the same scripted behaviour over `moby/spdystream` (indirect dep via client-go) — wrap with `httpstream.NewResponseUpgrader` from `k8s.io/apimachinery/pkg/util/httpstream/spdy`.
- A toggle to make the WS upgrade fail with HTTP 400 (`upgrade required: spdy`) so we can force the `NewFallbackExecutor` path and assert that SPDY actually carries the session.

**Test cases (table-driven):**

| # | Transport | Stdin | Stdout assert | Resize | Close path | Tunnel chaos |
|---|-----------|-------|---------------|--------|------------|--------------|
| 1 | WS v5 | `"hello\n"` | echoes `"hello"` | none | client `{type:close}` | none |
| 2 | WS v5 | binary 1 MiB | full echo, no truncation | none | clean | none |
| 3 | WS v5 | `"r"` then resize 200×50 | resize ack | yes | clean | none |
| 4 | SPDY v4 | `"hello\n"` | echoes | none | clean | none |
| 5 | SPDY v4 | 1 MiB | full echo | none | clean | none |
| 6 | WS→SPDY fallback | `"hello\n"` | `result.Transport=spdy` | none | clean | fakeAPI rejects WS |
| 7 | WS v5 | streaming | partial stdout received | none | session goroutine exits ≤ 2s with `tunnel closed` error | tunnel client `Close()` mid-stream |
| 8 | WS v5 | none | hello frame received | none | session closes on client disconnect | client websocket close before exec start |

Each case asserts:

- `ExecResult.Transport` (`ws_v5` / `spdy`) propagates correctly out of `ExecPod`.
- The `internal/exec/session.Run` reader / writer / heartbeat / idle goroutines exit cleanly — verified with `goleak.VerifyNone(t)` (`go.uber.org/goleak` is already a direct dep) at end of each case.
- For the tunnel-drop case, the websocket close frame reason code matches `closed.tunnel` or equivalent.

**Existing utilities to reuse (no duplicates):**

- `internal/tunnel/server_test.go` — pattern for standing up `tunnel.Server` with stubbed authorizer + `httptest`.
- `internal/tunnel/transport_test.go` — pattern for exercising `NewRoundTripper` over a tunneled dial.
- `internal/k8s/agent_transport_test.go` — pattern for installing a test `AgentTunnelLookup` via `SetAgentTunnelLookup`.

**Things deliberately not invented:**

- No new public API; the harness is test-only.
- No mocks of `remotecommand.Executor` — we drive the real one (the entire point).
- No envtest. envtest spins up a real apiserver but not kubelet, so it can't actually serve exec; the fake apiserver is the right tool here.

**Run cost:** each case ≈ 0.5–1 s; full table < 30 s; runs on every PR via existing `make test` (`go test ./...`).

### 4.2 Tier 2 — Real kind e2e (`make poc-exec-tunnel`, opt-in)

Higher-fidelity validation against a real apiserver + kubelet, exercised on demand and as a release gate. Not in CI on every PR (cost), but trivial to run locally and runnable via a manually-triggered GitHub Actions workflow.

**Files (new):**

- `hack/poc-exec-tunnel/Makefile.fragment` — included by top-level Makefile.
- `hack/poc-exec-tunnel/kind.yaml` — single-node kind config.
- `hack/poc-exec-tunnel/run.sh` — orchestrates the whole thing.
- `hack/poc-exec-tunnel/agent-values.yaml` — values for the existing `deploy/helm/periscope-agent/` chart.
- `hack/poc-exec-tunnel/clusters.yaml` — Periscope cluster registry with one entry, `backend: agent`.
- `hack/poc-exec-tunnel/probe.go` — small Go program that drives the exec WebSocket end-to-end as a real client.

**Top-level Makefile addition:**

```make
include hack/poc-exec-tunnel/Makefile.fragment

poc-exec-tunnel: ## #43 POC: end-to-end exec through agent tunnel on kind
	./hack/poc-exec-tunnel/run.sh
```

**`run.sh` flow (idempotent, ≈ 3–4 min cold, ≈ 30 s warm):**

1. `kind create cluster --name periscope-poc --config hack/poc-exec-tunnel/kind.yaml` (skip if exists).
2. `make image kind-load KIND_NAME=periscope-poc` to load both `periscope` and `periscope-agent` images (existing `Dockerfile` / `Dockerfile.agent`).
3. Apply a `busybox` Deployment in `default` as the exec target.
4. Start `periscope` server outside kind on the host, pointed at `clusters.yaml`, listening on `:8080` (SPA) + `:8443` (tunnel). Background it; capture PID.
5. `POST /api/agents/tokens` to mint a bootstrap token (admin auth via the existing dev OIDC bypass).
6. `helm install periscope-agent deploy/helm/periscope-agent -f hack/poc-exec-tunnel/agent-values.yaml --set bootstrapToken=<token> --set serverURL=wss://host.docker.internal:8443/api/agents/connect` inside kind.
7. Poll `GET /api/clusters` until the cluster reports `connected: true` (60 s timeout).
8. Run `probe.go` against `ws://localhost:8080/api/clusters/poc-cluster/pods/default/<busybox>/exec?...&tty=true`.
9. probe.go asserts:
   - hello frame received with the resolved container name
   - sends `echo periscope-poc-token\n`, expects `periscope-poc-token` back on stdout
   - sends a resize JSON, expects no error
   - sends close JSON, expects `closed` frame with exit code 0
   - repeats with WS disabled at the server (env var) to validate SPDY through the tunnel
10. Tear-down: `helm uninstall`, kill server PID. Leave the kind cluster (cheaper for re-runs); add `make poc-exec-tunnel-clean` to delete it.

**Pass / fail signal:** `run.sh` exits 0 on success, prints colored PASS/FAIL summary, dumps `kubectl logs` from the agent pod and the tail of the server log on failure.

**CI integration:** add `.github/workflows/poc-exec-tunnel.yaml` (`workflow_dispatch:`) that runs the same script in a GitHub-hosted ubuntu runner with kind preinstalled. Not blocking — purely for "run the POC against this branch" on demand. Becomes a release gate once we promote it.

---

## 5. Critical files to modify

| File | Why |
|------|-----|
| `internal/k8s/exec_tunnel_test.go` | **New.** Tier 1 in-process integration test. |
| `Makefile` | Add `poc-exec-tunnel` and `poc-exec-tunnel-clean` targets. |
| `hack/poc-exec-tunnel/` | **New directory.** All Tier 2 artifacts. |
| `.github/workflows/poc-exec-tunnel.yaml` | **New.** `workflow_dispatch` job that runs Tier 2. |

Nothing in `internal/exec/`, `internal/k8s/exec.go`, or `internal/tunnel/` should change. If something *needs* to change there, it's a finding — see §7.

## 6. Existing functions / utilities the POC reuses

| Symbol | Location | Role |
|---|---|---|
| `ExecPod` | `internal/k8s/exec.go` | Driven directly by Tier 1. |
| `streamWebSocket` (uses `remotecommand.NewWebSocketExecutor`) | `internal/k8s/exec.go` | Under test. |
| `streamSPDY` (uses `remotecommand.NewSPDYExecutor`) | `internal/k8s/exec.go` | Under test. |
| `runWithFallback` | `internal/k8s/exec.go` | The WS → SPDY fallback policy under test. |
| `SetAgentTunnelLookup` | `internal/k8s/agent_transport.go` | Tier 1 plugs the in-process tunnel here. |
| `buildAgentRestConfig` | `internal/k8s/agent_transport.go` | Produces the `rest.Config` whose Transport rides the tunnel. |
| `tunnel.Server` / `tunnel.Client` | `internal/tunnel/server.go`, `internal/tunnel/client.go` | Real Server + Client used in Tier 1, no mocking. |
| `NewRoundTripper` | `internal/tunnel/transport.go` | Installs the tunnel `DialFunc` into `http.Transport`. |
| `session.Run` | `internal/exec/session.go` | Exercised end-to-end in Tier 2 via the real WebSocket handler at `cmd/periscope/exec_handler.go`. |
| `goleak` | `go.uber.org/goleak` (direct dep) | Goroutine-leak assertions in Tier 1. |
| `gorilla/websocket` | direct dep | Powers the fake apiserver's WS exec. |
| `moby/spdystream` | indirect dep via client-go | Powers the fake apiserver's SPDY exec. |

---

## 7. Bail-out criteria

If any Tier 1 case fails consistently or Tier 2 surfaces a real issue, **stop** and triage before changing any production code. The likely failure modes and what they imply:

- **WS upgrade rejected at the apiserver after tunnel dial** — almost certainly a TLS / Host-header issue in `buildAgentRestConfig`, not a tunnel problem. Fix in `internal/k8s/agent_transport.go`.
- **SPDY upgrade fails but WS works** — `gorilla/websocket` message-size cap or remotedialer flow-control biting on long-lived bidirectional streams. Drop to a small framing helper around the tunnel `net.Conn` (the original Phase 2b path from #43). Estimated ≈ 200 LoC.
- **Stdout truncation** — `gorilla/websocket` `SetReadLimit` on the tunnel side. Patchable in `internal/tunnel/server.go`.
- **Goroutine leak after tunnel drop** — context-propagation bug in `session.Run` or `client.Run`. Fix at the point of leak.

Each of these unblocks a specific code change; none require redesign.

---

## 8. Verification

Tier 1 (every PR):

```
make test                                          # full unit suite, includes new exec_tunnel_test.go
go test -run ExecTunnel -race ./internal/k8s/...   # tighter loop
```

Tier 2 (on demand / release):

```
make poc-exec-tunnel              # full kind run, ≈ 3–4 min cold, ≈ 30 s warm
make poc-exec-tunnel-clean        # nuke the kind cluster
```

Manual SPA smoke (final confidence on a release candidate):

1. `make poc-exec-tunnel` to bring up the stack.
2. Open `http://localhost:8080`, click into the agent-backed cluster, open the busybox pod, click **Open Shell**.
3. Confirm: prompt appears, typing echoes, `Ctrl-D` closes cleanly, no orange "exec disconnected" toast.
4. Repeat with browser DevTools Network tab inspecting the WebSocket frames — confirm v5 channel bytes (channel prefix 0/1/2/4) are flowing.

---

## 9. Decision after POC

If Tier 1 + Tier 2 both pass, in the same PR series:

1. Default `exec.enabled: true` for `backend: agent` clusters in `deploy/helm/periscope/values.yaml`.
2. Remove the "exec not yet supported on agent-managed clusters" tooltip from the SPA — `web/src/components/exec/OpenShellButton.tsx` already hides cleanly when `execEnabled` is true; likely just a values-file flip + chart docs.
3. Add the agent-backed exec case to the existing smoke matrix in #42.
4. Close #43 and reference this RFC + the validation harness as the regression guard.

Resulting v1.x.0 release ships with a fully-functional agent backend including exec — collapsing #43 into #42 with no separate v1.x.1.
