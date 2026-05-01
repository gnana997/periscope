/**
 * Wire types for the pod-exec WebSocket protocol.
 *
 * Browser ↔ Periscope (RFC 0001 §6):
 *   binary frames  →  stdin (in)  / stdout+stderr merged (out)
 *   text frames    →  JSON control messages
 */

export type SessionStatus = "connecting" | "connected" | "closed" | "error";

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

// In-memory representation of a session in the React context.
export interface ExecSessionMeta {
  /** Local UUID used as a stable React key. The server emits its own
   *  session_id in the hello frame; we keep both for cross-reference. */
  id: string;
  /** From the server's hello frame. Empty until hello received. */
  serverSessionId: string;
  cluster: string;
  namespace: string;
  pod: string;
  /** Container name. May be empty until the hello frame resolves it. */
  container: string;
  /** What the user originally asked for (may be "" → server resolves). */
  requestedContainer: string;
  status: SessionStatus;
  createdAt: number;
  closedAt?: number;
  closeReason?: string;
  exitCode?: number;
  errorCode?: string;
  errorMessage?: string;
  /** Last time stdout was received — used for tab activity pulse. */
  lastActivityAt: number;
}
