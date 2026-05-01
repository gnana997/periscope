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
 * Lifecycle:
 *   ws opens       →  status=connecting
 *   hello frame    →  status=connected
 *   closed frame   →  status=closed
 *   error frame    →  status=error (terminal; close)
 *   transport drop →  status=error
 */

export interface ExecClientOptions {
  /** Build absolute WS URL for the session. Caller controls protocol/host. */
  url: string;
}

type Listener<T> = (payload: T) => void;

export class ExecClient {
  private ws: WebSocket;
  private _status: SessionStatus = "connecting";
  private _serverSessionId = "";
  private _container = "";
  private _exitCode: number | undefined;
  private _closeReason: string | undefined;
  private _errorCode: string | undefined;
  private _errorMessage: string | undefined;

  private resizeDebounceTimer: number | null = null;
  private pendingResize: { cols: number; rows: number } | null = null;

  // Listener arrays. Keep simple — these are not high-fan-out events.
  private stdoutListeners: Listener<Uint8Array>[] = [];
  private helloListeners: Listener<HelloFrame>[] = [];
  private closedListeners: Listener<ClosedFrame>[] = [];
  private errorListeners: Listener<ErrorFrame>[] = [];
  private idleWarnListeners: Listener<IdleWarnFrame>[] = [];
  private statusListeners: Listener<SessionStatus>[] = [];

  constructor(opts: ExecClientOptions) {
    this.ws = new WebSocket(opts.url);
    this.ws.binaryType = "arraybuffer";

    this.ws.addEventListener("message", (ev) => this.onMessage(ev));
    this.ws.addEventListener("close", (ev) => this.onClose(ev));
    this.ws.addEventListener("error", () => this.onTransportError());
    // Note: ws.onopen does not change status; we wait for the server's
    // hello frame so we know the apiserver actually accepted the exec.
  }

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

  // --- send -------------------------------------------------------------

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
   * Graceful close: send the close control frame so the server can record
   * "client" as the close_reason, then drop the WS.
   */
  close(): void {
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
  }

  private sendControl(frame: OutboundControlFrame): void {
    this.ws.send(JSON.stringify(frame));
  }

  // --- receive ----------------------------------------------------------

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
        this.transition("connected");
        for (const l of this.helloListeners) l(frame);
        break;
      case "closed":
        this._exitCode = frame.exitCode;
        this._closeReason = frame.reason;
        this.transition("closed");
        for (const l of this.closedListeners) l(frame);
        break;
      case "error":
        this._errorCode = frame.code;
        this._errorMessage = frame.message;
        this.transition("error");
        for (const l of this.errorListeners) l(frame);
        break;
      case "idle_warn":
        for (const l of this.idleWarnListeners) l(frame);
        break;
    }
  }

  private onClose(ev: CloseEvent): void {
    if (this._status !== "closed" && this._status !== "error") {
      // No closed frame received before the WS dropped — flag as error.
      this._closeReason = ev.reason || "transport_close";
      this._errorCode = this._errorCode ?? "E_TRANSPORT";
      this.transition("error");
    }
  }

  private onTransportError(): void {
    if (this._status === "connecting") {
      this._errorCode = "E_TRANSPORT";
      this._errorMessage = "couldn't reach the backend";
      this.transition("error");
    }
    // Once connected, transport errors come through onClose.
  }

  private transition(next: SessionStatus): void {
    if (this._status === next) return;
    this._status = next;
    for (const l of this.statusListeners) l(next);
  }

  // --- subscribe --------------------------------------------------------

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
