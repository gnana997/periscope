import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { ExecClient, buildExecURL } from "./ExecClient";
import type { ExecSessionMeta } from "./types";

/**
 * App-root context that owns active pod-exec sessions. Sessions outlive
 * route components — opening a shell from one page and navigating away
 * does NOT tear it down.
 *
 * Ground rules: no Redux, no zustand. State lives in the provider's
 * useState/useRef. ExecClient instances live in a ref keyed by session id
 * (because the client itself is non-serializable and would crash React's
 * structural compare otherwise).
 */

export const SESSION_CAP = 5;
const STORAGE_HEIGHT = "periscope.exec.drawerHeight";
const STORAGE_OPEN = "periscope.exec.drawerOpen";

export interface OpenSessionInput {
  cluster: string;
  namespace: string;
  pod: string;
  /** Empty string → server resolves via default-container annotation. */
  container?: string;
  command?: string[];
  tty?: boolean;
}

export type OpenSessionResult =
  | { ok: true; session: ExecSessionMeta }
  | { ok: false; reason: "cap_reached" | "exists"; existingId?: string };

interface DrawerState {
  /** Drawer expanded vs collapsed. Hidden when sessions.length === 0. */
  open: boolean;
  /** Pixels. Min 160, max 80% viewport. */
  height: number;
}

interface ExecSessionsContextValue {
  sessions: ExecSessionMeta[];
  activeSessionId: string | null;
  drawer: DrawerState;
  openSession: (input: OpenSessionInput) => OpenSessionResult;
  focusSession: (id: string) => void;
  closeSession: (id: string) => void;
  setDrawerOpen: (open: boolean) => void;
  setDrawerHeight: (height: number) => void;
  toggleDrawer: () => void;
  /** Look up the live ExecClient for rendering a Terminal. */
  getClient: (id: string) => ExecClient | null;
  /** Banner action: skip reconnect backoff and try now. */
  reconnectNow: (id: string) => void;
  /** Banner action: abandon reconnection — flips status to error. */
  giveUpReconnect: (id: string) => void;
}

// Visibility-close window: any session closed by the visibility timer
// within this many ms of the user returning is auto-restarted. Beyond
// this window, the user has to click reconnect on the banner.
const AUTO_RESTART_WINDOW_MS = 60_000;
// How long the tab can stay hidden before we voluntarily close live
// sessions (RFC 0001 §7).
const VISIBILITY_HIDE_LIMIT_MS = 5 * 60_000;

interface VisibilityRecent {
  params: OpenSessionInput;
  closedAt: number;
}

const Ctx = createContext<ExecSessionsContextValue | null>(null);

function loadDrawerState(): DrawerState {
  let height = 320;
  let open = true;
  try {
    const h = window.localStorage.getItem(STORAGE_HEIGHT);
    if (h) {
      const n = parseInt(h, 10);
      if (Number.isFinite(n)) height = clampHeight(n);
    }
    const o = window.localStorage.getItem(STORAGE_OPEN);
    if (o === "0") open = false;
  } catch {
    // localStorage may be blocked (e.g. some embed contexts) — ignore.
  }
  return { open, height };
}

function clampHeight(h: number): number {
  // 70vh leaves enough breathing room for the rest of the page once the
  // drawer is part of layout flow. 80vh felt brutal on tall pod tables.
  const max = Math.floor(window.innerHeight * 0.7);
  return Math.max(160, Math.min(max, h));
}

function makeId(): string {
  // crypto.randomUUID is ubiquitous in modern browsers; fallback for
  // ancient ones isn't needed in this project.
  return crypto.randomUUID();
}

function findExistingFor(
  sessions: ExecSessionMeta[],
  input: OpenSessionInput,
): ExecSessionMeta | undefined {
  return sessions.find(
    (s) =>
      s.status !== "closed" &&
      s.status !== "error" &&
      s.cluster === input.cluster &&
      s.namespace === input.namespace &&
      s.pod === input.pod &&
      (input.container ?? "") === s.requestedContainer,
  );
}

export function ExecSessionsProvider({ children }: { children: ReactNode }) {
  const [sessions, setSessions] = useState<ExecSessionMeta[]>([]);
  const [activeSessionId, setActiveSessionId] = useState<string | null>(null);
  const [drawer, setDrawer] = useState<DrawerState>(() => loadDrawerState());

  const clients = useRef<Map<string, ExecClient>>(new Map());

  const updateSession = useCallback(
    (id: string, patch: Partial<ExecSessionMeta>) => {
      setSessions((prev) =>
        prev.map((s) => (s.id === id ? { ...s, ...patch } : s)),
      );
    },
    [],
  );

  const openSession = useCallback(
    (input: OpenSessionInput): OpenSessionResult => {
      const existing = findExistingFor(sessions, input);
      if (existing) {
        setActiveSessionId(existing.id);
        setDrawer((d) => ({ ...d, open: true }));
        return { ok: false, reason: "exists", existingId: existing.id };
      }
      // Count only sessions that still hold a connection toward the cap.
      const live = sessions.filter(
        (s) => s.status === "connecting" || s.status === "connected",
      );
      if (live.length >= SESSION_CAP) {
        return { ok: false, reason: "cap_reached" };
      }

      const id = makeId();
      const url = buildExecURL({
        cluster: input.cluster,
        namespace: input.namespace,
        pod: input.pod,
        container: input.container,
        command: input.command,
        tty: input.tty ?? true,
      });
      const client = new ExecClient({ url });
      clients.current.set(id, client);

      const meta: ExecSessionMeta = {
        id,
        serverSessionId: "",
        cluster: input.cluster,
        namespace: input.namespace,
        pod: input.pod,
        container: input.container ?? "",
        requestedContainer: input.container ?? "",
        status: "connecting",
        createdAt: Date.now(),
        lastActivityAt: Date.now(),
      };

      // Subscribe lifecycle events to mirror state into React.
      client.onStatus((status) => {
        updateSession(id, { status });
      });
      client.onHello((frame) => {
        updateSession(id, {
          serverSessionId: frame.sessionId,
          container: frame.container || meta.requestedContainer,
          status: "connected",
        });
      });
      client.onClosed((frame) => {
        updateSession(id, {
          status: "closed",
          closedAt: Date.now(),
          closeReason: frame.reason,
          exitCode: frame.exitCode,
        });
      });
      client.onError((frame) => {
        updateSession(id, {
          status: "error",
          closedAt: Date.now(),
          errorCode: frame.code,
          errorMessage: frame.message,
        });
      });
      client.onStdout(() => {
        // Avoid re-rendering on every stdout chunk by stamping at most ~5x/s.
        const now = Date.now();
        setSessions((prev) => {
          const cur = prev.find((s) => s.id === id);
          if (!cur) return prev;
          if (now - cur.lastActivityAt < 200) return prev;
          return prev.map((s) =>
            s.id === id ? { ...s, lastActivityAt: now } : s,
          );
        });
      });
      client.onIdleWarn((frame) => {
        updateSession(id, {
          lastIdleWarnAt: Date.now(),
          idleWarnSecondsRemaining: frame.secondsRemaining,
        });
      });
      client.onReconnectScheduled(({ attempt }) => {
        updateSession(id, { reconnectAttempt: attempt });
      });

      setSessions((prev) => [...prev, meta]);
      setActiveSessionId(id);
      setDrawer((d) => ({ ...d, open: true }));
      return { ok: true, session: meta };
    },
    [sessions, updateSession],
  );

  const focusSession = useCallback((id: string) => {
    setActiveSessionId(id);
    setDrawer((d) => ({ ...d, open: true }));
  }, []);

  const closeSession = useCallback((id: string) => {
    const client = clients.current.get(id);
    if (client) client.close();
    clients.current.delete(id);
    setSessions((prev) => {
      const next = prev.filter((s) => s.id !== id);
      // If we just closed the active tab, fall through to the next live one,
      // or just clear if none remain.
      setActiveSessionId((cur) => {
        if (cur !== id) return cur;
        const fallback = next.find(
          (s) => s.status === "connecting" || s.status === "connected",
        );
        return fallback ? fallback.id : next[0]?.id ?? null;
      });
      return next;
    });
  }, []);

  const setDrawerOpen = useCallback((open: boolean) => {
    setDrawer((d) => ({ ...d, open }));
    try {
      window.localStorage.setItem(STORAGE_OPEN, open ? "1" : "0");
    } catch {
      /* ignore */
    }
  }, []);

  const setDrawerHeight = useCallback((height: number) => {
    const clamped = clampHeight(height);
    setDrawer((d) => ({ ...d, height: clamped }));
    try {
      window.localStorage.setItem(STORAGE_HEIGHT, String(clamped));
    } catch {
      /* ignore */
    }
  }, []);

  const toggleDrawer = useCallback(() => {
    setDrawer((d) => {
      const next = !d.open;
      try {
        window.localStorage.setItem(STORAGE_OPEN, next ? "1" : "0");
      } catch {
        /* ignore */
      }
      return { ...d, open: next };
    });
  }, []);

  // Keyboard shortcut: Cmd/Ctrl + ` toggles the drawer (VSCode/iTerm muscle
  // memory). Only when sessions exist; otherwise let it fall through.
  //
  // Two robustness details:
  //   1. We match e.code === "Backquote" in addition to e.key === "`" so
  //      non-US keyboard layouts (where the physical key produces a
  //      different e.key) still trigger the shortcut.
  //   2. Capture phase + stopPropagation so the shortcut wins against
  //      xterm.js, which otherwise would consume the keystroke as
  //      terminal stdin once the terminal has focus.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      const meta = e.metaKey || e.ctrlKey;
      if (!meta) return;
      if (e.key !== "`" && e.code !== "Backquote") return;
      if (e.shiftKey || e.altKey) return;
      // No sessions guard intentionally removed — opening the drawer with
      // an empty session list lands on the EmptyPicker, which is the
      // primary entry point for "I want to shell into a pod from here."
      e.preventDefault();
      e.stopPropagation();
      toggleDrawer();
    }
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [toggleDrawer]);

  // Clamp drawer height on viewport resize.
  useEffect(() => {
    function onResize() {
      setDrawer((d) => {
        const clamped = clampHeight(d.height);
        return clamped === d.height ? d : { ...d, height: clamped };
      });
    }
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  const getClient = useCallback((id: string) => {
    return clients.current.get(id) ?? null;
  }, []);

  const reconnectNow = useCallback((id: string) => {
    clients.current.get(id)?.reconnectNow();
  }, []);

  const giveUpReconnect = useCallback((id: string) => {
    clients.current.get(id)?.giveUp();
  }, []);

  // Visibility timer: when the tab has been hidden continuously for
  // VISIBILITY_HIDE_LIMIT_MS, voluntarily close every live session and
  // remember their params. On return, auto-restart anything closed
  // within AUTO_RESTART_WINDOW_MS — same shell wiring, fresh kernel
  // session.
  const recentsRef = useRef<VisibilityRecent[]>([]);
  const hiddenSinceRef = useRef<number | null>(null);
  const hiddenTimerRef = useRef<number | null>(null);

  useEffect(() => {
    function liveSessionsSnapshot(): {
      id: string;
      params: OpenSessionInput;
    }[] {
      return sessionsRef.current
        .filter((s) => s.status === "connecting" || s.status === "connected")
        .map((s) => ({
          id: s.id,
          params: {
            cluster: s.cluster,
            namespace: s.namespace,
            pod: s.pod,
            container: s.requestedContainer || undefined,
          },
        }));
    }

    function fireHiddenClose() {
      const live = liveSessionsSnapshot();
      const closedAt = Date.now();
      for (const { id, params } of live) {
        recentsRef.current.push({ params, closedAt });
        const c = clients.current.get(id);
        if (c) c.close();
      }
      // Trim recents that aren't worth restarting any more.
      recentsRef.current = recentsRef.current.filter(
        (r) => closedAt - r.closedAt < AUTO_RESTART_WINDOW_MS,
      );
    }

    function onVisibility() {
      if (document.visibilityState === "hidden") {
        hiddenSinceRef.current = Date.now();
        if (hiddenTimerRef.current !== null) {
          window.clearTimeout(hiddenTimerRef.current);
        }
        hiddenTimerRef.current = window.setTimeout(
          fireHiddenClose,
          VISIBILITY_HIDE_LIMIT_MS,
        );
      } else {
        // Coming back. Cancel any pending hidden-close (we made it back
        // in time), then auto-restart any sessions the timer DID close
        // during this hidden window.
        hiddenSinceRef.current = null;
        if (hiddenTimerRef.current !== null) {
          window.clearTimeout(hiddenTimerRef.current);
          hiddenTimerRef.current = null;
        }
        const now = Date.now();
        const fresh = recentsRef.current.filter(
          (r) => now - r.closedAt < AUTO_RESTART_WINDOW_MS,
        );
        recentsRef.current = [];
        for (const r of fresh) {
          openSessionRef.current?.(r.params);
        }
      }
    }
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      document.removeEventListener("visibilitychange", onVisibility);
      if (hiddenTimerRef.current !== null) {
        window.clearTimeout(hiddenTimerRef.current);
      }
    };
    // We deliberately don't depend on sessions / openSession here; the
    // refs below capture the latest values without causing the listener
    // to re-bind on every state change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Refs that mirror the live values for use inside the visibility
  // listener (which must not re-bind on every render).
  const sessionsRef = useRef(sessions);
  sessionsRef.current = sessions;
  const openSessionRef = useRef(openSession);
  openSessionRef.current = openSession;

  const value = useMemo<ExecSessionsContextValue>(
    () => ({
      sessions,
      activeSessionId,
      drawer,
      openSession,
      focusSession,
      closeSession,
      setDrawerOpen,
      setDrawerHeight,
      toggleDrawer,
      getClient,
      reconnectNow,
      giveUpReconnect,
    }),
    [
      sessions,
      activeSessionId,
      drawer,
      openSession,
      focusSession,
      closeSession,
      setDrawerOpen,
      setDrawerHeight,
      toggleDrawer,
      getClient,
      reconnectNow,
      giveUpReconnect,
    ],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useExecSessions(): ExecSessionsContextValue {
  // React 19's `use()` reads context with the same semantics as
  // useContext but is allowed inside conditionals and loops, so it's the
  // forward-looking choice for new code (RFC 0001 §7 and the
  // react-doctor recommendation).
  const v = use(Ctx);
  if (!v) {
    throw new Error("useExecSessions must be used within ExecSessionsProvider");
  }
  return v;
}
