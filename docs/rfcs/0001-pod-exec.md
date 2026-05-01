# RFC 0001 — Pod Exec Support

| | |
|---|---|
| **Status** | Draft |
| **Owner** | @gnana997 |
| **Started** | 2026-05-01 |
| **Targets** | v1 (ship), v2 (identity-correctness), v3 (MCP exposure) |
| **Related** | `GROUND_RULES.md` |

---

## 1. Summary

Periscope ships an in-browser interactive shell into Kubernetes pods. A user clicks
**Shell** on a pod (or container), a terminal opens in their browser, and they get a
bidirectional TTY into the container — keystrokes, output, terminal resize, signals,
and graceful exit, all over a single WebSocket between the browser and the Periscope
backend.

The backend bridges that WebSocket to the apiserver's exec stream using `client-go`'s
`FallbackExecutor` (WebSocket v5 preferred, SPDY fallback). Sessions auto-close on
inactivity and auto-restart silently when the user returns. Identity is passed through
end-to-end: v1 records the Okta-authenticated user in app-level audit, v2 enforces
Kubernetes RBAC against the user's real IAM principal, v3 exposes the same operation
as an MCP tool with no additional code.

This is the single hardest piece of v1 and the one most visible to users. Getting the
abstraction right here is what makes v2 a credential swap and v3 a thin shim.

---

## 2. Motivation

**User-facing.** Rancher's exec UX — buried in a dropdown, must close-and-reclick to
reconnect after any disconnect — is the single most-cited frustration from the audience
this product targets (developers and SREs migrating off Rancher). Periscope's
differentiator on this surface is "feels like a real terminal, not a web widget."

**Architectural.** Pod exec is the most demanding operation in the v1 scope:
bidirectional, long-lived, latency-sensitive, identity-sensitive. If the
`(ctx, credProvider, args) → result` discipline survives exec, every other operation is
trivially conformant. Conversely, if exec is a special-case detour around the
abstraction, v2 and v3 are rewrites instead of swaps.

**Pitch-defining.** The keyless-auth pitch holds only if exec — the most powerful
operation in v1 — never requires the dashboard to hold privileged user credentials
in v2. The handoff is the proof.

---

## 3. Goals and non-goals

### Goals

- Bidirectional terminal access to a chosen container in a chosen pod, in a chosen
  cluster, from the browser. xterm.js-based UI.
- One typed Go function `ExecPod(ctx, provider, args) → (result, error)` that v1
  HTTP handlers, v2 SSO handlers, and v3 MCP tools all call unchanged.
- Identity correctness per phase: v1 audit-only, v2 K8s-RBAC-enforced, v3 inherits.
- Resilient session lifecycle: server-side idle close, client-side visibility close,
  silent auto-restart on transient drops, banner UX for hard disconnects.
- Compatibility: prefer K8s 1.30+ WebSocket subprotocol (`v5.channel.k8s.io`), fall
  back to SPDY for older clusters; per-cluster circuit breaker to avoid the
  handshake-then-fallback tax on every connection.
- Hard caps on concurrent sessions per user and per cluster. Audit log entries on
  every session start/end.
- Never log stdin payload. Optional stdout capture is a separate concern (off by
  default).

### Non-goals

- **True session resumability across disconnects.** Kubernetes does not support this;
  when the WS dies the shell process dies. Auto-restart is a fresh shell, clearly
  signaled in the UI.
- **tmux/screen-based session persistence.** Possible v2.x opt-in if the container
  has tmux installed; not v1.
- **File copy via exec.** Separate feature, separate RFC.
- **Multi-user shared sessions** (collaborative shells). Not in scope.
- **Stdout recording for compliance.** Hook designed in; implementation deferred.
- **Application-layer authorization decisions.** K8s RBAC is the single source of
  truth, per ground rules.

---

## 4. User experience

### Entry points

1. **Pod detail page → Shell tab.** Container picker if pod has multiple containers
   (preselected to the only or first non-init container).
2. **URL-addressable.** `/clusters/{cluster}/pods/{ns}/{name}/exec?container=app`
   opens directly to the terminal. Sharable link (does not share session, opens a
   new one for the receiver).

### Happy path

1. Click **Shell** → terminal panel slides in, displays `Connecting to <pod>/<container>…`.
2. Within ~500ms, the prompt of the container's default shell appears.
3. User types, hits enter, sees output. Resize the panel → terminal reflows.
4. User clicks **Disconnect** or closes the tab → session ends gracefully, exit code
   shown if available.

### Inactivity / reconnection

- **Tab hidden ≥ 5 min** (default, configurable): client closes WS voluntarily. On
  return, banner: *"Session paused. Reconnect for a fresh shell?"* with **Reconnect**
  button (auto-clicks after 1s if user opted into auto-restart).
- **Server idle ≥ 10 min** (default, configurable): server emits a 30-second warning
  message into the terminal (`session will close in 30s due to inactivity`), then
  closes. Banner appears as above.
- **Network drop < 5 s**: client silently auto-restarts. Banner is suppressed; a
  subtle toast confirms reconnect to set expectations ("Reconnected — fresh shell").
- **Network drop ≥ 5 s**: banner with reconnect countdown and **Reconnect now**
  button.
- **Reconnect failure** (3 attempts, exponential backoff): banner switches to
  *"Couldn't reconnect. Try again?"* with manual button.

### Error states (visible to user)

- **No shell in container** (distroless): friendly message *"This container has no
  /bin/sh or /bin/bash. Pick another container or pod."* with container picker
  re-opened.
- **Forbidden** (v2): *"Your identity (`<email>`) doesn't have `pods/exec` in the
  `<ns>` namespace."* No retry button.
- **Cluster unreachable**: *"Cluster `<name>` isn't reachable right now."* with
  retry.
- **Session cap hit**: *"You already have 5 active shells open. Close one to start
  another."* Lists active sessions with **Disconnect** buttons.

### Observability for the user

- A small status pill above the terminal: `connected · 02:14` (uptime), `idle 04:32`
  (countdown to server-idle close starts at 5 min remaining).
- Expandable "session info" reveals: cluster, namespace, pod, container, identity
  used (for v2 users this matters: it shows the human's IAM principal, proving
  pass-through).

---

## 5. Architecture

```
Browser                                     Periscope Backend                    apiserver
─────────                                   ─────────────────                    ─────────

┌──────────────────────────┐                ┌──────────────────────┐             ┌──────────────┐
│ xterm.js                 │                │ chi router           │             │              │
│   ├─ FitAddon            │  Upgrade       │   └─ /exec           │             │              │
│   └─ WebLinksAddon       │  (HTTP+Auth)   │       │              │             │              │
│                          │ ─────────────► │       ▼              │             │              │
│ ExecClient (custom)      │                │ Auth middleware      │             │              │
│   ├─ WebSocket           │                │ Audit middleware     │             │              │
│   ├─ frame router        │  binary ⇄      │ ┌──────────────────┐ │ rest.Config │   /exec      │
│   │   (binary=data,      │  text JSON ◄══►│ │ ExecSession      │ │             │   handler    │
│   │    text=control)     │                │ │  ┌─────────────┐ │ │             │              │
│   └─ Reconnect           │                │ │  │ FallbackExec│ │ │ WSv5 / SPDY │              │
│      supervisor          │                │ │  └──────┬──────┘ │ │ ─────────►  │              │
│                          │                │ │         │        │ │             │              │
│                          │                │ │  Activity tracker│ │             │              │
│                          │                │ │  Idle timer      │ │             │              │
│                          │                │ └──────────────────┘ │             │              │
└──────────────────────────┘                │ slog audit sink      │             │              │
                                            └──────────────────────┘             └──────────────┘
                                                       │
                                                       ▼
                                                  Audit log
                                            (slog handler routes)
```

### Backend components

**`internal/k8s/exec.go`** — the typed operation.

```go
type ExecPodArgs struct {
    Cluster       string
    Namespace     string
    Pod           string
    Container     string
    Command       []string         // e.g. ["/bin/sh","-c","exec /bin/bash 2>/dev/null || exec /bin/sh"]
    TTY           bool
    Stdin         io.Reader        // nil if no stdin requested
    Stdout        io.Writer
    Stderr        io.Writer        // may equal Stdout when merged
    TerminalSize  <-chan remotecommand.TerminalSize  // nil if no TTY
    Policy        ExecutorPolicy   // injected; see §8
}

type ExecResult struct {
    ExitCode int    // -1 if unknown
    Reason   string // "completed" | "client_close" | "idle" | ...
}

func ExecPod(ctx context.Context, provider credentials.Provider, args ExecPodArgs) (ExecResult, error)
```

This is the same shape as `OpenPodLogStream` and other operations. v3 MCP exposes it
by wrapping the same call; the only difference is the streams are connected to the
MCP transport instead of an HTTP WebSocket.

**`internal/exec/session.go`** — session orchestration. Owns the per-session
goroutines: stdin pump, stdout pump, idle tracker, heartbeat, control-frame router.
Wires the streams between the browser WebSocket and `ExecPod`.

**`internal/exec/policy.go`** — per-cluster circuit breaker (see §8).

**`internal/exec/registry.go`** — in-memory session registry for cap enforcement and
admin "kill session" later. Keyed by `actor.sub`. No persistence in v1.

**HTTP handler** — thin: validates query params, applies caps, calls
`session.New(args).Run(ctx, ws)`.

**`audit.Begin / audit.End`** — slog wrappers; emit one record at session start (with
all metadata except byte counts and reason), one at end (with the rest).

### Frontend components

**`web/src/lib/exec.ts`** — `ExecClient` class. Wraps WebSocket. Handles binary↔text
frame routing, reconnect supervisor (visibility tracking, backoff), control-frame
codec.

**`web/src/components/exec/Terminal.tsx`** — xterm.js wrapper. Mounts terminal,
attaches FitAddon, calls `ExecClient` for IO.

**`web/src/components/exec/ExecPanel.tsx`** — chrome around the terminal: status
pill, banner host, reconnect button, session info expander.

**`web/src/pages/ExecPage.tsx`** — full-page route at
`/clusters/{cluster}/pods/{ns}/{name}/exec`.

**Pod detail integration** — Shell tab embeds `ExecPanel` inline; same component as
the full page route.

xterm.js is lazy-loaded (the `@xterm/xterm` bundle is ~200kb gzipped); the Shell tab
suspends until the chunk arrives.

---

## 6. Wire protocol

### Browser ↔ Periscope (single WebSocket)

We use **WebSocket frame type as discriminator** — binary for data, text (JSON) for
control. This avoids re-inventing channel-byte framing on the browser side and gives
us a clean, debuggable control plane.

#### Browser → server

| Frame type | Payload | Meaning |
|---|---|---|
| **binary** | raw bytes | stdin |
| **text** | `{"type":"resize","cols":80,"rows":24}` | terminal resize |
| **text** | `{"type":"signal","name":"SIGINT"}` | future; v1 sends Ctrl-C as a stdin byte |
| **text** | `{"type":"close"}` | graceful close request |

#### Server → browser

| Frame type | Payload | Meaning |
|---|---|---|
| **binary** | raw bytes | stdout + stderr **merged** server-side |
| **text** | `{"type":"hello","sessionId":"...","container":"app","shell":"/bin/sh","subprotocol":"v5.channel.k8s.io"}` | sent immediately after upgrade succeeds |
| **text** | `{"type":"idle_warn","secondsRemaining":30}` | inactivity warning |
| **text** | `{"type":"closed","reason":"idle\|client\|server\|forbidden\|...","exitCode":0}` | sent before WS close |
| **text** | `{"type":"error","code":"E_NO_SHELL","message":"…"}` | unrecoverable error |

**Why merged stdout+stderr?** Terminal users can't visually distinguish them anyway,
the TTY interleaves them naturally, and merging on the server lets us use a single
binary stream. If a future use case (e.g., debugger UI) needs them separated, we
introduce a query param `?separateStreams=1` and add a 1-byte channel prefix in
binary mode. Not v1.

**Frame size.** Default WebSocket message limit on the server: 1 MiB. Stdin frames
are typically <1 KiB. Stdout chunks from apiserver come in 4 KiB blocks; we forward
unchanged. Anything >1 MiB is a protocol error and closes the session.

### Periscope ↔ apiserver

Handled by `client-go`. We pass our `Stdin/Stdout/Stderr io.Reader/Writer` and a
`TerminalSizeQueue`; `FallbackExecutor` does the channel-byte framing and protocol
negotiation. We never touch the wire format directly.

---

## 7. Session lifecycle

### Server-side state machine

```
                  ┌─────────────┐
                  │  STARTING   │  validate, look up pod, init audit
                  └──────┬──────┘
                         │ ok
                         ▼
                  ┌─────────────┐
   stdin/stdout──►│   RUNNING   │◄──┐
                  └──────┬──────┘   │ activity
                         │          │ resets timer
            idle timer ──┤          │
                         ▼          │
                  ┌─────────────┐   │
                  │ IDLE_WARN   │───┘ activity returns
                  │  (30s grace)│
                  └──────┬──────┘
                         │ no activity
                         ▼
                  ┌─────────────┐
                  │   CLOSING   │  send {type:closed}, flush, close apiserver stream
                  └──────┬──────┘
                         ▼
                  ┌─────────────┐
                  │   CLOSED    │  audit.End, registry.Remove
                  └─────────────┘
```

Other transitions to CLOSING: client `{type:"close"}`, WS heartbeat miss, apiserver
EOF, context cancellation, container exit.

### Client-side state machine

```
   ┌──────────────┐ ws upgrade ┌──────────────┐
   │  CONNECTING  │ ──────────►│   ATTACHED   │
   └──────────────┘            └───┬─────┬────┘
        ▲                          │     │ visibilitychange:hidden
        │ user reconnects          │     ▼
        │                          │ ┌──────────────┐
   ┌────┴─────────┐                │ │  HIDDEN      │ start 5min timer
   │ DISCONNECTED │                │ └──┬───────────┘
   │  (banner)    │                │    │ visible
   └────┬─────────┘                │    │
        ▲                          │    ▼
        │ retries exhausted        │ (resume — same session)
        │                          │
        │                ws close  │
   ┌────┴─────────┐ ◄──────────────┘
   │ RECONNECTING │  exp backoff [0, 1s, 3s, 8s]
   └──────────────┘
```

`TERMINATED` (from any state) when the user closes the tab or clicks Disconnect.

### Heartbeat

WebSocket-level ping every 20 s, pong timeout 10 s. Implemented at the
`coder/websocket` library level; missed pong → connection close with code 1006,
client transitions to RECONNECTING.

### Idle definition

Server treats **any byte received on stdin OR sent to stdout** as activity. Pure
heartbeats do not reset the timer. This is deliberately conservative: a session
with no terminal output is not "active" just because it's holding a connection.

Client tracks `document.visibilityState`. When hidden, starts a 5-minute timer; on
expiry, voluntarily closes the WS with code `4001 client_idle`. On `visible`, if
state is `DISCONNECTED` and `auto_reconnect_on_focus` is true (default), opens a new
session immediately.

### Auto-restart vs auto-resume

Already established but worth restating in the doc: *every reconnect is a new exec
session.* The server never holds a "paused" session; once closed, it's gone. The UI
must communicate this for any user trying to reason about state ("I had `cd /app`
running — is my pwd preserved?" — no, it isn't).

---

## 8. Compatibility: WS v5 with SPDY fallback + per-cluster circuit breaker

`client-go` exposes `NewFallbackExecutor(primary, fallback, fallbackPredicate)`. We
configure primary = `NewWebSocketExecutor` (v5 subprotocol), fallback =
`NewSPDYExecutor`. By default this is per-attempt: every connection to a SPDY-only
cluster pays the WS handshake → fail → SPDY-retry tax.

**`ExecutorPolicy`** is a per-cluster object held in
`internal/exec/policy.go`:

```go
type ExecutorPolicy struct {
    mu                 sync.Mutex
    consecutiveWSFails int
    pinnedToSPDYUntil  time.Time
    threshold          int           // default 3
    pinDuration        time.Duration // default 30m
}

func (p *ExecutorPolicy) Choose() (mode string)   // "ws_then_spdy" or "spdy_only"
func (p *ExecutorPolicy) RecordResult(mode string, err error)
```

A registry maps cluster name → `*ExecutorPolicy` and survives reloads. Optional
boot-time probe: one zero-payload exec against `kube-system/coredns-*` per cluster
to set initial mode (off by default, requires extra RBAC).

This optimization matters in mixed environments where some clusters have been
upgraded and others haven't. It does **not** turn into "pin to SPDY forever":
after the pin expires, the next connection probes WS again. Self-healing.

---

## 9. Identity, authentication, authorization

### v1 (shared IRSA, or kubeconfig)

| Layer | Responsibility |
|---|---|
| Browser → Periscope | Periscope's existing Okta cookie (set at login). The exec endpoint refuses to upgrade the WebSocket without a valid session. |
| Periscope → apiserver | The shared K8s identity from the Provider (IRSA-mapped role, or kubeconfig user). Identical for every Okta user. |
| K8s authz | The shared identity has `pods/exec` per the v1 IRSA scope. The cluster authorizes uniformly for all users. |
| Audit | Periscope app-level only, keyed on Okta `sub`. K8s audit records the dashboard SA. |

No Periscope-side authorization decision. Okta authentication is sufficient. This is
deliberate per the ground rules: app-level RBAC tables are forbidden.

### v2 EKS (per-user IDC pass-through)

| Layer | Responsibility |
|---|---|
| Browser → Periscope | Same Okta cookie. |
| Periscope → AWS | Mints STS creds from the user's IDC token (already established in v2). |
| Periscope → apiserver | `rest.Config` carries the user's STS-derived bearer token. K8s sees the user's real IAM principal. |
| K8s authz | RBAC against the user's identity. Denies if `pods/exec` is not granted in the namespace. |
| Audit | Periscope app-level + K8s audit log on the cluster (shows the human directly). |

**No exec-side code changes from v1 → v2.** The `Provider` interface returns
different credentials; `ExecPod` is identical. This is the proof that the
abstraction holds.

### v2 kubeconfig (still shared)

Per the ground rules, kubeconfig clusters operate in shared-credential mode in v2
as well. Exec behaves identically to v1 kubeconfig. Documented in the README.

### Optional v2.x: kubeconfig + impersonation

For operators who run kubeconfig-backed clusters and want per-user RBAC without
moving to EKS, we leave a hook in the `Provider` to wrap the clientset with
[K8s native impersonation](https://kubernetes.io/docs/reference/access-authn-authz/user-impersonation/)
(`Impersonate-User`, `Impersonate-Groups`). The kubeconfig identity must have the
`impersonate` verb. Opt-in via cluster config:

```yaml
clusters:
  - name: dev-local
    backend: kubeconfig
    kubeconfigPath: ~/.kube/config
    impersonate: true   # opt-in; kubeconfig user must have impersonate verb
```

Not built in v1. The hook exists to ensure we don't paint into a corner.

---

## 10. Audit

Every session emits two structured `slog` records: one at session start, one at
session end. Both carry `category=audit` for easy routing.

### Schema

```go
slog.Info("pod_exec",
    "category",       "audit",
    "event",          "session_start" | "session_end",
    "session_id",     uuid,
    "actor.sub",      okta.Sub,
    "actor.email",    okta.Email,
    "cluster",        cluster,
    "namespace",      ns,
    "pod",            pod,
    "container",      container,
    "command",        cmdJSON,             // []string, joined by space for human read
    "tty",            true,
    "k8s_identity",   "shared-irsa-v1" | "user-idc:<arn>" | "kubeconfig:<user>",
    "transport",      "ws_v5" | "spdy",
    // session_end only:
    "started_at",     t0,
    "ended_at",       t1,
    "duration_ms",    dur,
    "close_reason",   "client" | "idle" | "server_error" | "forbidden" | "container_exit",
    "exit_code",      0,
    "bytes_stdin",    n,                   // count, never content
    "bytes_stdout",   n,
)
```

### Routing

Default: stdlib `slog.JSONHandler` to stderr. The container's existing log pipeline
captures it. No new infrastructure.

Future: a custom `slog.Handler` filters records with `category=audit` and writes
them to a separate sink (file, S3, SIEM). This is a config change, not a code
change. Compliance teams can attach their own sink.

### What is **not** logged

- **Stdin payload** — passwords, secrets, multi-factor codes get typed there.
- **Stdout payload** by default. A `--audit-stdout` flag exists to capture stdout to
  a per-session file (rotating, capped at 10 MB) for compliance environments. Off
  by default. v1.x feature, not v1.0.

### Cross-referencing

`session_id` (UUIDv4) is the join key. v2 K8s audit records the same value as a
custom audit annotation (`audit.periscope.io/session-id`) we set via
`rest.Config.WrapTransport`, letting compliance teams pivot between Periscope's
audit log and the cluster's audit log on a single ID.

---

## 11. Configuration

### Global (main config — proposed `periscope.yaml` or env equivalents)

```yaml
exec:
  enabled: true
  serverIdleSeconds: 600          # 10 min
  clientHiddenSeconds: 300        # 5 min
  maxSessionsPerUser: 5
  maxSessionsTotal: 50
  wsFailThreshold: 3
  spdyPinDuration: 30m
  defaultShell: ["/bin/sh", "-c", "exec /bin/bash 2>/dev/null || exec /bin/sh"]
  auditStdout: false
  auditStdoutMaxBytes: 10485760   # 10 MiB per session if enabled
  bootProbe: false                # off; if on, one /bin/true probe per cluster on startup
```

### Per-cluster overrides (`clusters.yaml`)

```yaml
clusters:
  - name: prod-eks
    backend: eks
    arn: arn:aws:eks:us-east-1:123:cluster/prod
    exec:
      enabled: true
      serverIdleSeconds: 1800     # 30 min for long-running ops debugging
      maxSessionsPerUser: 10

  - name: locked-down
    backend: eks
    arn: arn:aws:eks:us-east-1:123:cluster/locked
    exec:
      enabled: false              # disable exec entirely on this cluster
```

When `exec.enabled: false` at the cluster level, the API returns `403` with code
`E_EXEC_DISABLED`. The Shell tab is hidden in the UI for that cluster.

---

## 12. Error taxonomy

All server errors are sent on the WebSocket as
`{"type":"error","code":"E_*","message":"...","retryable":true|false}` and mirror to
audit.

| Code | Cause | Retryable | UX |
|---|---|---|---|
| `E_AUTH` | Okta cookie missing/expired | no | redirect to `/login` |
| `E_FORBIDDEN` | K8s denied `pods/exec` (v2) | no | "Your identity lacks pods/exec on `<ns>`" |
| `E_EXEC_DISABLED` | Cluster config disables exec | no | hide Shell tab |
| `E_CAP_USER` | User session cap hit | no | list of active sessions + close buttons |
| `E_CAP_CLUSTER` | Cluster total cap hit | yes (rate-limited) | "cluster busy, try again" |
| `E_NO_SHELL` | Container has no `/bin/sh` | no | container picker reopens |
| `E_NOT_FOUND` | Pod or container vanished | no | redirect to pods list |
| `E_CLUSTER_UNREACHABLE` | apiserver TCP/TLS error | yes | retry button |
| `E_PROTOCOL` | Both WS and SPDY failed | yes | retry; if persistent, support |
| `E_IDLE` | Server idle timeout | yes | reconnect banner |
| `E_HEARTBEAT` | Missed pongs | yes | silent auto-reconnect |
| `E_INTERNAL` | Unexpected server error | yes | toast + retry |

The client distinguishes `retryable` from non-retryable to decide whether to show a
**Reconnect** button.

---

## 13. Security considerations

- **TLS required** for the exec endpoint. The handler refuses to upgrade on plain
  HTTP unless explicitly running in dev mode (`--dev`).
- **Origin check** on WebSocket upgrade. Default: same-origin only. Configurable
  allowlist for embedded use cases (none in v1).
- **CSRF**: WS upgrade is a `GET` request; the auth cookie is `SameSite=Lax`. The
  origin check above is the primary CSRF defense. The Okta session is in an
  HttpOnly cookie, never readable from JS.
- **Stdin payload privacy**: explicitly never logged. Code review must enforce this.
  A linter rule (custom or `go vet` analyzer) checking that no `slog.Info` call
  references the stdin pipe is a v1.x add.
- **Concurrent session caps**: enforced at upgrade time and decremented on close.
  Prevents one tab-storm from saturating the backend.
- **Resource cleanup**: every session goroutine takes a `context.Context`. The HTTP
  handler's request context cancels on client disconnect. `ExecPod` propagates
  cancellation to the apiserver stream. `defer registry.Remove(sessionID)` covers
  panic paths.
- **Rate limiting on connection attempts**: 10 WS upgrades per actor per minute.
  Prevents reconnect-loop runaway from a buggy client.
- **Container with privileged escalation paths** (e.g., a container that runs as
  root and has `CAP_SYS_ADMIN`): out of scope. K8s authz governs whether the user
  can exec at all; what they do once inside is the cluster's problem.
- **No app-level RBAC**: per ground rules. K8s decides.

---

## 14. Phasing

| PR | Scope | Lines (est.) | Depends on |
|---|---|---|---|
| **PR0** | chi router migration. No exec code yet. Existing routes converted from `http.ServeMux` to `chi.Router`. **`credentials.Wrap` is preserved as-is** — it returns an `http.HandlerFunc`, which plugs straight into chi. Provider stays an explicit argument per ground rules ("`context.Context` carries cancellation and deadlines, not credentials"). Scaffolding `RequestID`, `Recoverer`, `RealIP`, and an `AuditBegin` no-op stub are added as standard chi middlewares (none read or carry credentials). | ~250 | — |
| **PR1** | Backend MVP: `internal/k8s/exec.go` (`ExecPod` + `ExecPodArgs`), `internal/exec/session.go`, `internal/exec/registry.go`, HTTP route `/api/clusters/{cluster}/pods/{ns}/{name}/exec`, `coder/websocket` upgrade, `FallbackExecutor`, audit `slog` start/end. **No idle, no reconnect, no caps.** Behind `--feature-exec` flag. | ~600 | PR0 |
| **PR2** | Frontend MVP: `web/src/lib/exec.ts` (`ExecClient`), `Terminal.tsx`, `ExecPanel.tsx`, `ExecPage.tsx`, Shell tab on pod detail. xterm.js lazy chunk. Manual disconnect only. | ~500 | PR1 |
| **PR3** | Lifecycle: server idle timer + IDLE_WARN, client visibility timer, heartbeat, auto-restart UX (banner, backoff, silent <5s drops). | ~400 | PR2 |
| **PR4** | Robustness: `ExecutorPolicy` circuit breaker, per-user/cluster caps, audit polish (`session_id`, transport field), error taxonomy, config plumbing, per-cluster overrides. | ~400 | PR3 |
| **v2** | When v2 SSO Provider lands: zero changes to exec. Verify K8s RBAC enforces. Add `k8s_identity` audit field automatically (Provider supplies it). | trivial | v2 SSO |
| **v2.x** | Optional kubeconfig impersonation. `Provider` for kubeconfig backend supports `Impersonate-User` headers. | small | v1 |
| **v3** | Expose `ExecPod` as MCP tool. Wraps stdin/stdout to MCP transport. Same function. | small | v3 MCP runtime |

PR0 lands first as a pure prep PR — no behavior change — to keep PR1 reviewable.

---

## 15. Alternatives considered

### Direct browser → apiserver

The browser holds AWS / kubeconfig credentials, talks to the apiserver directly,
Periscope only serves the SPA. **Rejected**: ships credentials to the browser;
breaks the keyless-auth pitch on the v1 path; impossible to audit at the dashboard
layer.

### `@xterm/addon-attach`

The official xterm addon for WebSocket attach. **Rejected** for our protocol:
addon-attach assumes a single-stream text protocol with no out-of-band controls.
Our control plane (resize, hello, idle_warn, error) needs structured frames.
Writing our own attach is ~50 lines and removes the dependency.

### SSE for stdout, plain HTTP for stdin

**Rejected**: stdin via HTTP is not low-latency (one connection per keystroke is
absurd; one persistent POST has worse browser semantics than a WebSocket). Two
half-duplex channels also require a session-correlation token, doubling the
attack surface.

### gRPC-Web

**Rejected**: pulls in a non-trivial dep tree (envoy proxy, code generation) for
one feature. WebSocket suffices.

### Server-side persistent sessions (tmux-mode by default)

A daemon on the server keeps the apiserver exec stream alive across browser
disconnects, presenting "true" session resume. **Rejected for v1**: requires
detached server-side state, complicates auth (whose identity owns the resumed
session?), and the apiserver itself doesn't support reattach to a running exec.
Documented as a v2.x opt-in, only when the container has tmux/screen.

### Channel-byte framing on the browser WebSocket

Mirror the K8s channel protocol byte-by-byte. **Rejected** in favor of binary-vs-text
frame discrimination: cleaner debug story (`wscat` shows control as JSON), no need
to parse byte prefixes in JS, and the WS spec gives us frame typing for free.

---

## 16. Resolved decisions

These were open during initial review and have since been resolved.

1. **Default shell command:** `sh -c "command -v bash >/dev/null 2>&1 && exec bash; exec sh"`.
   Uses unqualified program names so the container runtime (runc/crun) performs
   PATH lookup — matters for images that ship a shell at `/usr/bin/sh` without
   the `/bin/sh` symlink (Wolfi, newer distroless, custom slim images). Probes
   `bash` with `command -v` *before* `exec`-ing it because POSIX-strict shells
   (busybox sh, dash) terminate the shell with status 127 when an `exec`
   target isn't found, *before* a `||` branch can run — so the elegant
   `exec bash || exec sh` idiom is silently broken on alpine/busybox images.
2. **Session info pill shows K8s identity in v1.** Always reads `shared-irsa-v1` in
   v1; honest and educates users about what identity actually executes their
   commands. Becomes load-bearing in v2.
3. **Audit log retention** is out of scope for this RFC. Operators handle retention
   via their existing log pipeline. Compliance-grade external sinks come later via
   a custom `slog.Handler`.
4. **Container picker default:** use the pod's
   `kubectl.kubernetes.io/default-container` annotation if present, otherwise the
   first non-init container.
5. **xterm.js theme** matches Periscope's existing dark/light theme tokens. Small
   frontend-design polish pass after PR2 lands.
6. **`session_id` cross-reference** (`audit.periscope.io/session-id` injected via
   `rest.Config.WrapTransport`) ships in PR1 so it's there from day one — no
   schema migration when v2 K8s audit log joins start happening.

---

## 17. Acceptance criteria

The feature is complete (v1) when:

1. A logged-in Okta user can open a shell into any pod's container in any registered
   cluster they can reach, from a Pod detail page, with sub-second perceived latency
   to first prompt on a healthy network.
2. Closing the browser tab terminates the apiserver stream within 5 seconds.
3. Server idle timer fires accurately at the configured threshold; warning shows
   30 s prior; user typing during the warning resets the timer.
4. Client visibility close fires accurately when a tab is hidden past the threshold.
5. Auto-restart on transient (<5 s) drops produces a fresh shell with no manual
   click; banner UX appears for hard drops.
6. Per-cluster circuit breaker pins a SPDY-only cluster after the configured
   threshold and self-recovers when the apiserver supports v5 again.
7. Every session start and end emits a structured audit record with the schema in
   §10. Stdin payload appears in zero log lines under any test.
8. Concurrent session caps are enforced; the user-facing message lists active
   sessions with disconnect buttons.
9. Distroless container produces `E_NO_SHELL`, not a hung connection or stack
   trace.
10. `ExecPod`'s function signature is unchanged from v1 to v2 in code review of
    the v2 PR.

---

## 18. Appendix — references

- [KEP-4006: SPDY → WebSockets transition](https://github.com/kubernetes/enhancements/tree/master/keps/sig-api-machinery/4006-transition-spdy-to-websockets)
- [Kubernetes 1.31: Streaming transitions from SPDY to WebSockets](https://kubernetes.io/blog/2024/08/20/websockets-transition/)
- [User impersonation — Kubernetes docs](https://kubernetes.io/docs/reference/access-authn-authz/user-impersonation/)
- [`coder/websocket` (formerly `nhooyr.io/websocket`)](https://github.com/coder/websocket)
- [xterm.js](https://xtermjs.org/) and [`@xterm/addon-fit`](https://www.npmjs.com/package/@xterm/addon-fit)
- [`client-go` `remotecommand` package — `NewFallbackExecutor`, `NewWebSocketExecutor`](https://pkg.go.dev/k8s.io/client-go/tools/remotecommand)
- Reference implementations studied: kubernetes/dashboard, derailed/k9s, headlamp-k8s/headlamp
