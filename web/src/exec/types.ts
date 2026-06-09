/**
 * Wire types for the pod-exec WebSocket protocol.
 *
 * Browser ↔ Periscope (RFC 0001 6):
 *   binary frames  →  stdin (in)  / stdout+stderr merged (out)
 *   text frames    →  JSON control messages
 */

export type SessionStatus =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "closed"
  | "error";

// Server → browser control frames
export interface HelloFrame {
  type: "hello";
  sessionId: string;
  container: string;
  shell?: string;
  subprotocol?: string;
}

export interface ClosedFrame {
  type: "closed";
  reason?: string;
  exitCode?: number;
}

export interface ErrorFrame {
  type: "error";
  code: string;
  message: string;
  retryable?: boolean;
  /** Agent-upstream error category. Populated only on
   *  code === "E_AGENT_UPSTREAM" frames; the drawer banner switches
   *  on this to render network/tls/timeout-specific copy. */
  category?: "network" | "tls" | "timeout" | "no_agent" | "unknown";
  /** Cluster the agent reported the failure for. Populated alongside
   *  category on E_AGENT_UPSTREAM frames. */
  cluster?: string;
  /** End-to-end trace id matching the agent's slog line. Surfaced in
   *  the banner so operators can grep the exact failure across server
   *  audit DB, server stdout, and agent stdout. */
  traceId?: string;
  /** Raw underlying error string from the agent's reverse proxy. Shown
   *  in the info expander, not the banner headline. */
  detail?: string;
}

export interface IdleWarnFrame {
  type: "idle_warn";
  secondsRemaining: number;
}

// Browser → server control frames
export interface ResizeFrame {
  type: "resize";
  cols: number;
  rows: number;
}

export interface CloseFrame {
  type: "close";
}

export type InboundControlFrame =
  | HelloFrame
  | ClosedFrame
  | ErrorFrame
  | IdleWarnFrame;

export type OutboundControlFrame = ResizeFrame | CloseFrame;

/** Session kind discriminator.
 *
 *  - "pod-exec"     classic per-pod kubectl exec stream (RFC 0001).
 *  - "cluster-shell" cluster-wide kubectl REPL with impersonation
 *    (issue #104). Backend creates an ephemeral pod per session and
 *    attaches via the same hello/stdin/stdout/closed/error frame
 *    protocol, so the client / drawer / terminal stack is shared.
 *    namespace/pod/container fields are empty for this kind.
 *  - "node-shell"   in-browser SSM shell into a node's EC2 host with
 *    per-user AWS impersonation (issue #105). Same frame protocol; the
 *    `node` field carries the node name, namespace/pod/container empty.
 */
export type SessionKind = "pod-exec" | "cluster-shell" | "node-shell";

/** Cluster-shell session mode (issue #104).
 *
 *  - "bash"          interactive bash session inside the shell pod.
 *  - "kubectl-only"  restricted REPL that only accepts kubectl/helm
 *                    (REPL ships after #104; the server currently
 *                    rejects this with E_NOT_IMPLEMENTED).
 */
export type ClusterShellMode = "bash" | "kubectl-only";

// In-memory representation of a session in the React context.
export interface ExecSessionMeta {
  /** Local UUID used as a stable React key. The server emits its own
   *  session_id in the hello frame; we keep both for cross-reference. */
  id: string;
  /** Discriminator — which session flavor this is. */
  kind: SessionKind;
  /** From the server's hello frame. Empty until hello received. */
  serverSessionId: string;
  cluster: string;
  /** Empty for kind="cluster-shell" — the session is cluster-scoped. */
  namespace: string;
  /** Empty for kind="cluster-shell". */
  pod: string;
  /** Container name. Empty for kind="cluster-shell", or until the hello
   *  frame resolves it for pod-exec. */
  container: string;
  /** What the user originally asked for (may be "" → server resolves).
   *  Empty for kind="cluster-shell". */
  requestedContainer: string;
  /** Only set for kind="cluster-shell". */
  mode?: ClusterShellMode;
  /** Node name. Only set for kind="node-shell". */
  node?: string;
  status: SessionStatus;
  createdAt: number;
  closedAt?: number;
  closeReason?: string;
  exitCode?: number;
  errorCode?: string;
  errorMessage?: string;
  /** Mirrors ErrorFrame.category for E_AGENT_UPSTREAM frames. */
  errorCategory?: "network" | "tls" | "timeout" | "no_agent" | "unknown";
  /** Cluster name carried on the error frame (agent-stamped). */
  errorCluster?: string;
  /** End-to-end trace id from the agent's slog line. */
  errorTraceId?: string;
  /** Raw underlying error string for the info expander. */
  errorDetail?: string;
  /** Last time stdout was received — used for tab activity pulse. */
  lastActivityAt: number;
  /** Unix-ms timestamp the most recent idle_warn arrived. The Drawer
   *  renders a yellow inactivity banner while
   *  Date.now() - lastIdleWarnAt < idleWarnSecondsRemaining * 1000. */
  lastIdleWarnAt?: number;
  /** Seconds the server said remained when emitting idle_warn. */
  idleWarnSecondsRemaining?: number;
  /** Reconnect attempt counter — surfaced in the banner copy
   *  ("reconnecting (attempt 2/4)"). Reset on successful reconnect. */
  reconnectAttempt?: number;
  /** Total number of times this session has reconnected since opening.
   *  Useful for the audit / debugging panel. */
  reconnectCount?: number;
}
