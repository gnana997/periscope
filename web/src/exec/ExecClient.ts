import type {
  ClosedFrame,
  ErrorFrame,
  HelloFrame,
  IdleWarnFrame,
  InboundControlFrame,
  OutboundControlFrame,
  SessionStatus,
} from "./types";

/**
 * ExecClient wraps the browser-facing pod-exec WebSocket. It is pure
 * TypeScript with no React dependency; the React layer subscribes via the
 * .on*() listeners.
 *
 * Wire format (RFC 0001 §6):
 *   binary frames  →  stdin (out)  /  stdout+stderr merged (in)
 *   text frames    →  JSON control envelopes
 *
 * Lifecycle (PR3 — reconnect supervisor):
 *
 *   new ExecClient    →  status=connecting
 *   hello frame       →  status=connected, reconnectAttempt=0
 *   closed frame      →  status=closed (terminal — no reconnect)
 *   error frame       →  status=error  (terminal — no reconnect)
 *   WS dropped        →  status=reconnecting, schedule new WS via backoff
 *     [0, 1000, 3000, 8000] ms; rebuild WebSocket with same URL
 *     on success      →  status=connecting → connected (hello received)
 *     after 4 fails   →  status=error, code=E_RECONNECT_FAILED
 *
 * The same ExecClient instance survives reconnects so xterm stays bound
 * to a stable object across transient drops; scrollback is preserved.
 */

export interface ExecClientOptions {
  /** Build absolute WS URL for the session. Caller controls protocol/host. */
  url: string;
}

type Listener<T> = (payload: T) => void;

const RECONNECT_BACKOFF_MS = [0, 1000, 3000, 8000];
const MAX_RECONNECT_ATTEMPTS = RECONNECT_BACKOFF_MS.length;

export class ExecClient {
  private url: string;
  private ws: WebSocket;
  private _status: SessionStatus = "connecting";
  private _serverSessionId = "";
  private _container = "";
  private _exitCode: number | undefined;
  private _closeReason: string | undefined;
  private _errorCode: string | undefined;
  private _errorMessage: string | undefined;

  // Reconnect state.
  private userClose = false;
  // True when the server explicitly told us not to retry — closed frame,
  // non-retryable error frame. Distinguishes "user pressed disconnect"
  // from "we can't recover from this."
  private terminal = false;
  private reconnectAttempt = 0;
  private reconnectCount = 0;
  private reconnectTimer: number | null = null;

  private resizeDebounceTimer: number | null = null;
  private pendingResize: { cols: number; rows: number } | null = null;

  // Listener arrays. Stable across reconnects — they belong to the
  // ExecClient instance, not the underlying WebSocket.
  private stdoutListeners: Listener<Uint8Array>[] = [];
  private helloListeners: Listener<HelloFrame>[] = [];
  private closedListeners: Listener<ClosedFrame>[] = [];
  private errorListeners: Listener<ErrorFrame>[] = [];
  private idleWarnListeners: Listener<IdleWarnFrame>[] = [];
  private statusListeners: Listener<SessionStatus>[] = [];
  private reconnectListeners: Listener<{ attempt: number; max: number }>[] = [];

  constructor(opts: ExecClientOptions) {
    this.url = opts.url;
    this.ws = this.openSocket();
  }

  // ------------------------------------------------------------------
  // public state accessors
  // ------------------------------------------------------------------
  get status(): SessionStatus {
    return this._status;
  }
  get serverSessionId(): string {
    return this._serverSessionId;
  }
  get container(): string {
    return this._container;
  }
  get exitCode(): number | undefined {
    return this._exitCode;
  }
  get closeReason(): string | undefined {
    return this._closeReason;
  }
  get errorCode(): string | undefined {
    return this._errorCode;
  }
  get errorMessage(): string | undefined {
    return this._errorMessage;
  }
  get reconnectAttempts(): number {
    return this.reconnectAttempt;
  }
  get totalReconnects(): number {
    return this.reconnectCount;
  }
  get maxReconnectAttempts(): number {
    return MAX_RECONNECT_ATTEMPTS;
  }

  // ------------------------------------------------------------------
  // outgoing
  // ------------------------------------------------------------------

  sendStdin(data: Uint8Array | string): void {
    if (this.ws.readyState !== WebSocket.OPEN) return;
    // Always send through a fresh ArrayBuffer. TS 6 types treat
    // Uint8Array<ArrayBufferLike> as possibly-SharedArrayBuffer-backed,
    // which WebSocket.send refuses; this copy strips that ambiguity and
    // is fast for keystroke-sized payloads.
    const src = typeof data === "string" ? new TextEncoder().encode(data) : data;
    const buf = new ArrayBuffer(src.byteLength);
    new Uint8Array(buf).set(src);
    this.ws.send(buf);
  }

  /**
   * Resize is debounced — drag-resizing the drawer fires many resize events
   * but only the final one matters to the apiserver.
   */
  sendResize(cols: number, rows: number): void {
    if (cols <= 0 || rows <= 0) return;
    this.pendingResize = { cols, rows };
    if (this.resizeDebounceTimer !== null) return;
    this.resizeDebounceTimer = window.setTimeout(() => {
      this.resizeDebounceTimer = null;
      const r = this.pendingResize;
      this.pendingResize = null;
      if (!r || this.ws.readyState !== WebSocket.OPEN) return;
      this.sendControl({ type: "resize", cols: r.cols, rows: r.rows });
    }, 80);
  }

  /**
   * Graceful close requested by the user. Cancels any pending reconnect,
   * sends the close control frame, and closes the WebSocket. The result
   * status is "closed" — never auto-reconnects.
   */
  close(): void {
    this.userClose = true;
    this.terminal = true;
    this.cancelReconnectTimer();
    if (this.ws.readyState === WebSocket.OPEN) {
      try {
        this.sendControl({ type: "close" });
      } catch {
        // ignored
      }
      this.ws.close(1000, "client close");
    } else if (this.ws.readyState === WebSocket.CONNECTING) {
      this.ws.close();
    }
    this.transition("closed");
  }

  /**
   * Skip the current reconnect backoff and try immediately. No-op if not
   * currently in reconnecting state.
   */
  reconnectNow(): void {
    if (this._status !== "reconnecting") return;
    this.cancelReconnectTimer();
    this.attemptReconnect();
  }

  /**
   * Abandon reconnection and surface the failure as a terminal error.
   * Used by the "give up" banner action.
   */
  giveUp(): void {
    this.cancelReconnectTimer();
    this.terminal = true;
    this._errorCode = this._errorCode ?? "E_RECONNECT_GAVE_UP";
    this._errorMessage = this._errorMessage ?? "you stopped reconnect attempts";
    this.transition("error");
  }

  private sendControl(frame: OutboundControlFrame): void {
    this.ws.send(JSON.stringify(frame));
  }

  // ------------------------------------------------------------------
  // socket lifecycle
  // ------------------------------------------------------------------

  /** Opens a new WebSocket and wires the same listeners. */
  private openSocket(): WebSocket {
    const ws = new WebSocket(this.url);
    ws.binaryType = "arraybuffer";
    ws.addEventListener("message", (ev) => this.onMessage(ev));
    ws.addEventListener("close", (ev) => this.onClose(ev));
    ws.addEventListener("error", () => this.onTransportError());
    return ws;
  }

  private onMessage(ev: MessageEvent): void {
    if (ev.data instanceof ArrayBuffer) {
      const bytes = new Uint8Array(ev.data);
      for (const l of this.stdoutListeners) l(bytes);
      return;
    }
    if (typeof ev.data !== "string") return;
    let frame: InboundControlFrame | null = null;
    try {
      frame = JSON.parse(ev.data) as InboundControlFrame;
    } catch {
      return;
    }
    switch (frame.type) {
      case "hello":
        this._serverSessionId = frame.sessionId;
        this._container = frame.container;
        // Successful hello on a reconnect resets the attempt counter so
        // a future drop gets the full backoff budget again.
        if (this.reconnectAttempt > 0) {
          this.reconnectCount += 1;
          this.reconnectAttempt = 0;
        }
        this.transition("connected");
        for (const l of this.helloListeners) l(frame);
        break;
      case "closed":
        this._exitCode = frame.exitCode;
        this._closeReason = frame.reason;
        // Server-acknowledged closes are terminal — never reconnect.
        this.terminal = true;
        this.transition("closed");
        for (const l of this.closedListeners) l(frame);
        break;
      case "error":
        this._errorCode = frame.code;
        this._errorMessage = frame.message;
        // PR3: respect retryable=false explicitly. retryable=true would
        // suggest reconnect-after-error semantics; we default to terminal
        // so an unknown error doesn't loop us forever.
        if (frame.retryable !== true) {
          this.terminal = true;
        }
        this.transition("error");
        for (const l of this.errorListeners) l(frame);
        break;
      case "idle_warn":
        for (const l of this.idleWarnListeners) l(frame);
        break;
    }
  }

  private onClose(ev: CloseEvent): void {
    // Already-terminal sessions don't reconnect — this includes user
    // close, server-acknowledged closed, and non-retryable error frames.
    if (this.terminal) {
      if (this._status !== "closed" && this._status !== "error") {
        this.transition("closed");
      }
      return;
    }
    if (this.userClose) {
      this.transition("closed");
      return;
    }
    // Transport drop without a closed frame — schedule reconnect.
    this._closeReason = ev.reason || "transport_close";
    this.scheduleReconnect();
  }

  private onTransportError(): void {
    // 'error' fires before 'close' on transport failures. We let onClose
    // drive the reconnect — it has the actual close code.
    if (this._status === "connecting" && this.reconnectAttempt === 0) {
      // Initial connect failed before even opening; classify the error
      // up-front so the UI knows to show "couldn't reach the backend"
      // immediately rather than silently retrying. The follow-up close
      // event will then schedule a reconnect.
      this._errorCode = this._errorCode ?? "E_TRANSPORT";
      this._errorMessage = this._errorMessage ?? "couldn't reach the backend";
    }
  }

  // ------------------------------------------------------------------
  // reconnect
  // ------------------------------------------------------------------

  private scheduleReconnect(): void {
    if (this.reconnectAttempt >= MAX_RECONNECT_ATTEMPTS) {
      this.terminal = true;
      this._errorCode = "E_RECONNECT_FAILED";
      this._errorMessage = `couldn't reconnect after ${MAX_RECONNECT_ATTEMPTS} attempts`;
      this.transition("error");
      return;
    }
    const delay = RECONNECT_BACKOFF_MS[this.reconnectAttempt];
    this.transition("reconnecting");
    for (const l of this.reconnectListeners) {
      l({ attempt: this.reconnectAttempt + 1, max: MAX_RECONNECT_ATTEMPTS });
    }
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.attemptReconnect();
    }, delay);
  }

  private attemptReconnect(): void {
    this.reconnectAttempt += 1;
    // openSocket creates a new WS — we don't await onopen; the existing
    // hello-frame listener will transition us to connected once the
    // server responds.
    this.ws = this.openSocket();
    this.transition("connecting");
  }

  private cancelReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      window.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private transition(next: SessionStatus): void {
    if (this._status === next) return;
    this._status = next;
    for (const l of this.statusListeners) l(next);
  }

  // ------------------------------------------------------------------
  // subscribe
  // ------------------------------------------------------------------

  onStdout(fn: Listener<Uint8Array>): () => void {
    this.stdoutListeners.push(fn);
    return () => {
      this.stdoutListeners = this.stdoutListeners.filter((x) => x !== fn);
    };
  }
  onHello(fn: Listener<HelloFrame>): () => void {
    this.helloListeners.push(fn);
    return () => {
      this.helloListeners = this.helloListeners.filter((x) => x !== fn);
    };
  }
  onClosed(fn: Listener<ClosedFrame>): () => void {
    this.closedListeners.push(fn);
    return () => {
      this.closedListeners = this.closedListeners.filter((x) => x !== fn);
    };
  }
  onError(fn: Listener<ErrorFrame>): () => void {
    this.errorListeners.push(fn);
    return () => {
      this.errorListeners = this.errorListeners.filter((x) => x !== fn);
    };
  }
  onIdleWarn(fn: Listener<IdleWarnFrame>): () => void {
    this.idleWarnListeners.push(fn);
    return () => {
      this.idleWarnListeners = this.idleWarnListeners.filter((x) => x !== fn);
    };
  }
  onStatus(fn: Listener<SessionStatus>): () => void {
    this.statusListeners.push(fn);
    return () => {
      this.statusListeners = this.statusListeners.filter((x) => x !== fn);
    };
  }
  /**
   * Called each time a reconnect is scheduled, with the next attempt
   * index (1-based) and the max. Useful for the Drawer banner copy.
   */
  onReconnectScheduled(fn: Listener<{ attempt: number; max: number }>): () => void {
    this.reconnectListeners.push(fn);
    return () => {
      this.reconnectListeners = this.reconnectListeners.filter((x) => x !== fn);
    };
  }
}

/**
 * Build the WebSocket URL for an exec session against the periscope
 * backend. Uses ws/wss based on the current page protocol.
 */
export function buildExecURL(params: {
  cluster: string;
  namespace: string;
  pod: string;
  container?: string;
  command?: string[];
  tty?: boolean;
}): string {
  const proto = window.location.protocol === "https:" ? "wss" : "ws";
  const host = window.location.host;
  const path = `/api/clusters/${encodeURIComponent(
    params.cluster,
  )}/pods/${encodeURIComponent(params.namespace)}/${encodeURIComponent(
    params.pod,
  )}/exec`;
  const q = new URLSearchParams();
  if (params.container) q.set("container", params.container);
  if (params.command && params.command.length > 0) {
    // Server expects base64(JSON-array). Use URL-safe base64 with no padding.
    const json = JSON.stringify(params.command);
    const b64 = btoa(json)
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");
    q.set("command", b64);
  }
  if (params.tty === false) q.set("tty", "false");
  const qs = q.toString();
  return `${proto}://${host}${path}${qs ? "?" + qs : ""}`;
}
