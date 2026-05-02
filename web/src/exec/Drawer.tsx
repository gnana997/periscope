import { useCallback, useState } from "react";
import { useParams } from "react-router-dom";
import { cn } from "../lib/cn";
import { useNow } from "../hooks/useNow";
import { useExecSessions } from "./useExecSessions";
import { Tab } from "./Tab";
import { TerminalLazy as Terminal } from "./TerminalLazy";
import { CollapsedBar } from "./CollapsedBar";
import { EmptyPicker } from "./EmptyPicker";
import { SessionFootbar } from "./SessionFootbar";
import type { ExecSessionMeta } from "./types";

/**
 * Drawer — a true bottom panel in the app's vertical layout flow. When
 * open it shrinks the rest of the page rather than overlaying it; when
 * collapsed-with-sessions it presents a 24px CollapsedBar; when empty
 * and closed it disappears entirely.
 *
 * Three open states:
 *
 *   ┌─ open + sessions ─────────────────────────────────────────────┐
 *   │ [drag handle]                                                 │
 *   │ [tab] [tab]    ●connected · 02:14   info  ✕disconnect  ⌘` ▾  │
 *   │ [info expander row when toggled]                              │
 *   │                       xterm viewport                          │
 *   └───────────────────────────────────────────────────────────────┘
 *
 *   ┌─ open + empty (Ctrl+` from no-sessions state) ────────────────┐
 *   │ [drag handle]                                                 │
 *   │ open a shell                                       ⌘`     ▾  │
 *   │ [pod picker body — cluster, ns, filter, virtualized list]     │
 *   └───────────────────────────────────────────────────────────────┘
 *
 *   ┌─ closed + sessions (CollapsedBar, 24px) ──────────────────────┐
 *   │ ● kind-local · grafana-…       3 shells          ⌘`        ⌃ │
 *   └───────────────────────────────────────────────────────────────┘
 */

const IS_MAC =
  typeof navigator !== "undefined" &&
  /mac|iphone|ipad|ipod/i.test(navigator.platform);

function statusPill(s: ExecSessionMeta, now: number) {
  const uptime =
    s.closedAt != null
      ? Math.max(0, Math.floor((s.closedAt - s.createdAt) / 1000))
      : Math.max(0, Math.floor((now - s.createdAt) / 1000));
  const m = Math.floor(uptime / 60);
  const sec = uptime % 60;
  const time = `${String(m).padStart(2, "0")}:${String(sec).padStart(2, "0")}`;

  switch (s.status) {
    case "connecting":
      return { label: "connecting…", tone: "yellow" as const, sub: null };
    case "connected":
      return { label: "connected", tone: "green" as const, sub: time };
    case "reconnecting": {
      const attempt = s.reconnectAttempt ?? 1;
      return {
        label: "reconnecting",
        tone: "yellow" as const,
        sub: `${attempt}/4`,
      };
    }
    case "closed":
      return {
        label: "closed",
        tone: "neutral" as const,
        sub: s.exitCode != null ? `exit ${s.exitCode}` : null,
      };
    case "error":
      return {
        label: s.errorCode || "error",
        tone: "red" as const,
        sub: s.errorMessage ?? null,
      };
  }
}

export function Drawer() {
  const {
    sessions,
    activeSessionId,
    drawer,
    focusSession,
    closeSession,
    setDrawerOpen,
    setDrawerHeight,
    getClient,
  } = useExecSessions();

  const params = useParams<{ cluster?: string }>();

  const [infoOpen, setInfoOpen] = useState(false);
  const now = useNow();

  // --- drag-to-resize ---------------------------------------------------
  // Both pointermove + pointerup are scoped to a per-drag AbortController,
  // so cleanup is a single ac.abort() — no self-referencing useCallback,
  // no lingering listeners if the component unmounts mid-drag.
  const startDrag = useCallback(
    (e: React.PointerEvent) => {
      const startY = e.clientY;
      const startH = drawer.height;
      const ac = new AbortController();

      const handleMove = (ev: PointerEvent) => {
        // The drawer grows by moving the top edge UP, so subtract.
        setDrawerHeight(startH + (startY - ev.clientY));
      };
      const handleStop = () => {
        ac.abort();
        // Batched cleanup via removeProperty so we don't issue two
        // sequential style writes (react-doctor performance finding)
        // and don't clobber unrelated inline styles set elsewhere.
        document.body.style.removeProperty("cursor");
        document.body.style.removeProperty("user-select");
      };

      // Single setProperty call vs two style assignments. The browser
      // batches inside setProperty itself; the goal is one observable
      // write per drag-start.
      document.body.style.setProperty("cursor", "ns-resize");
      document.body.style.setProperty("user-select", "none");
      window.addEventListener("pointermove", handleMove, { signal: ac.signal });
      window.addEventListener("pointerup", handleStop, { signal: ac.signal });
    },
    [drawer.height, setDrawerHeight],
  );
  // ---------------------------------------------------------------------

  // No sessions and drawer closed → render nothing (no layout impact).
  if (sessions.length === 0 && !drawer.open) return null;

  // Sessions exist but drawer closed → 24px collapsed bar.
  if (sessions.length > 0 && !drawer.open) {
    return <CollapsedBar />;
  }

  const empty = sessions.length === 0;
  const active = empty
    ? null
    : sessions.find((s) => s.id === activeSessionId) ??
      sessions[sessions.length - 1];
  const activeClient = active ? getClient(active.id) : null;
  const pill = active ? statusPill(active, now) : null;
  const closeable =
    active && active.status !== "closed" && active.status !== "error";

  return (
    <aside
      className="relative flex shrink-0 flex-col border-t border-border-strong bg-surface"
      style={{ height: drawer.height }}
      role="region"
      aria-label="Active terminal sessions"
    >
      {/* drag handle — sits straddling the top edge */}
      <div
        onPointerDown={startDrag}
        className="group/handle absolute -top-1 left-0 right-0 z-10 h-2 cursor-ns-resize"
      >
        <div className="absolute left-1/2 top-1/2 flex -translate-x-1/2 -translate-y-1/2 gap-1 opacity-0 transition-opacity group-hover/handle:opacity-100">
          <span className="block size-1 rounded-full bg-ink-faint" />
          <span className="block size-1 rounded-full bg-ink-faint" />
          <span className="block size-1 rounded-full bg-ink-faint" />
        </div>
      </div>

      {/* chrome row */}
      <div className="flex h-7 shrink-0 items-stretch border-b border-border bg-surface-2/40">
        {empty ? (
          <div className="flex shrink min-w-0 items-center px-3 font-mono text-[11.5px] text-ink-muted">
            <svg
              width="11"
              height="11"
              viewBox="0 0 11 11"
              aria-hidden
              className="mr-1.5 text-ink-faint"
            >
              <path
                d="M2 3l2.5 2.5L2 8M5.5 8H9"
                stroke="currentColor"
                strokeWidth="1.3"
                fill="none"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            <span>open a shell</span>
          </div>
        ) : (
          <div className="flex shrink min-w-0 items-stretch overflow-x-auto">
            {sessions.map((s) => (
              <Tab
                key={s.id}
                session={s}
                active={s.id === active?.id}
                onFocus={() => focusSession(s.id)}
                onClose={() => closeSession(s.id)}
              />
            ))}
          </div>
        )}

        {/* active-session controls (only when sessions exist) */}
        {!empty && active && pill && (
          <div className="ml-auto flex shrink-0 items-center gap-1.5 border-l border-border bg-surface px-2">
            <PillBadge tone={pill.tone} label={pill.label} sub={pill.sub} />

            <ChromeButton
              active={infoOpen}
              onClick={() => setInfoOpen((v) => !v)}
              title="Toggle session info"
              aria-expanded={infoOpen}
            >
              <svg
                width="9"
                height="9"
                viewBox="0 0 9 9"
                className={cn(
                  "transition-transform",
                  infoOpen ? "rotate-90" : "rotate-0",
                )}
              >
                <path
                  d="M3 1.5l3 3-3 3"
                  stroke="currentColor"
                  strokeWidth="1.3"
                  fill="none"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
              </svg>
              <span>info</span>
            </ChromeButton>

            <ChromeButton
              onClick={() => closeSession(active.id)}
              disabled={!closeable}
              tone="danger"
              title="Disconnect session · or press Ctrl-D inside the shell to exit cleanly"
            >
              <svg width="9" height="9" viewBox="0 0 9 9" aria-hidden>
                <path
                  d="M2 2l5 5M7 2l-5 5"
                  stroke="currentColor"
                  strokeWidth="1.4"
                  strokeLinecap="round"
                />
              </svg>
              <span>disconnect</span>
            </ChromeButton>
          </div>
        )}

        {/* drawer-level controls (always present) */}
        <div
          className={cn(
            "flex shrink-0 items-center gap-1 px-2",
            !empty && "border-l border-border bg-surface",
            empty && "ml-auto bg-surface",
          )}
        >
          <KbdHint />
          <button
            type="button"
            onClick={() => setDrawerOpen(false)}
            className="flex size-5 items-center justify-center rounded-sm text-ink-faint transition-colors hover:bg-surface-2 hover:text-ink"
            aria-label="Collapse drawer"
            title={`Collapse drawer (${IS_MAC ? "⌘" : "Ctrl"}\`)`}
          >
            <svg width="11" height="11" viewBox="0 0 11 11" aria-hidden>
              <path
                d="M2 7l3.5-3 3.5 3"
                stroke="currentColor"
                strokeWidth="1.4"
                fill="none"
                strokeLinecap="round"
                strokeLinejoin="round"
                transform="rotate(180 5.5 5.5)"
              />
            </svg>
          </button>
        </div>
      </div>

      {/* status banner — reconnecting / no_shell / idle_warn / give-up */}
      {!empty && active && <SessionBanner session={active} />}

      {/* info expander — drops below the chrome row only when open */}
      {!empty && active && infoOpen && (
        <div className="grid shrink-0 grid-cols-[auto_1fr_auto_1fr] gap-x-4 gap-y-1 border-b border-border bg-surface-2/40 px-3 py-2 font-mono text-[10.5px]">
          <Field k="cluster" v={active.cluster} />
          <Field k="namespace" v={active.namespace} />
          <Field k="pod" v={active.pod} />
          <Field k="container" v={active.container || "(resolving…)"} />
          <Field
            k="session id"
            v={active.serverSessionId || "(awaiting hello)"}
          />
          <Field
            k="opened"
            v={new Date(active.createdAt).toLocaleTimeString([], {
              hour12: false,
            })}
          />
        </div>
      )}

      {/* body */}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        {empty ? (
          <EmptyPicker initialCluster={params.cluster} />
        ) : (
          <>
            {sessions.map((s) => {
              const client = getClient(s.id);
              if (!client) return null;
              const isActive = s.id === active?.id;
              return (
                <div
                  key={s.id}
                  className={cn("absolute inset-0", isActive ? "block" : "hidden")}
                >
                  <Terminal client={client} active={isActive} />
                </div>
              );
            })}
            {!activeClient && (
              <div className="flex size-full flex-col items-center justify-center gap-1 font-mono text-[12px] text-ink-faint">
                <span>no active session</span>
              </div>
            )}
          </>
        )}
      </div>

      {/* footbar — container picker + readouts. Self-hides for
          single-container pods (component returns null). */}
      {!empty && active && <SessionFootbar session={active} />}
    </aside>
  );
}

function ChromeButton({
  children,
  onClick,
  disabled,
  active,
  tone,
  title,
  ...rest
}: {
  children: React.ReactNode;
  onClick: () => void;
  disabled?: boolean;
  active?: boolean;
  tone?: "danger";
  title?: string;
} & Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "onClick" | "disabled">) {
  const dangerHover =
    tone === "danger"
      ? "hover:border-red/60 hover:bg-red-soft hover:text-red"
      : "hover:border-border-strong hover:text-ink";

  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      title={title}
      className={cn(
        "flex h-5 items-center gap-1 rounded border px-1.5 font-mono text-[10.5px] transition-colors",
        active
          ? "border-border-strong bg-surface-2/60 text-ink"
          : "border-transparent text-ink-muted",
        !disabled && dangerHover,
        disabled && "cursor-not-allowed opacity-40",
      )}
      {...rest}
    >
      {children}
    </button>
  );
}

function KbdHint() {
  return (
    <span
      title="Toggle drawer"
      aria-hidden
      className="hidden shrink-0 items-center gap-0.5 sm:flex"
    >
      <kbd className="rounded border border-border bg-surface-2/40 px-1 py-px font-mono text-[9.5px] leading-none text-ink-faint">
        {IS_MAC ? "⌘" : "Ctrl"}
      </kbd>
      <kbd className="rounded border border-border bg-surface-2/40 px-1 py-px font-mono text-[9.5px] leading-none text-ink-faint">
        `
      </kbd>
    </span>
  );
}

function Field({ k, v }: { k: string; v: string }) {
  return (
    <>
      <span className="uppercase tracking-[0.08em] text-ink-faint">{k}</span>
      <span className="truncate text-ink-muted">{v}</span>
    </>
  );
}

// SessionBanner renders a contextual banner under the chrome row when the
// active session needs to surface lifecycle state — reconnecting, gave-up,
// idle warning, or a friendly error like E_NO_SHELL.
//
// The banner uses a CSS-only delayed fade-in so silent <800ms reconnects
// never paint at all — the banner element renders, but `fadein` doesn't
// elapse before status flips back. No JS timer juggling.
function SessionBanner({ session }: { session: ExecSessionMeta }) {
  const { reconnectNow, giveUpReconnect, closeSession } = useExecSessions();
  const now = useNow();

  // Idle-warn banner — server told us inactivity will close the session.
  // Render while we're still inside the warning window. Show even when
  // status has flipped to closed, briefly, so the user sees why.
  const idleWarnUntil =
    session.lastIdleWarnAt && session.idleWarnSecondsRemaining
      ? session.lastIdleWarnAt + session.idleWarnSecondsRemaining * 1000
      : 0;
  const showIdleWarn =
    session.status === "connected" &&
    idleWarnUntil > 0 &&
    now < idleWarnUntil;

  // No-shell error: backend marked the close with a friendlier code.
  const showNoShell =
    session.status === "error" && session.errorCode === "E_NO_SHELL";

  // Forbidden error: K8s rejected the exec upgrade because the user's
  // role lacks pods/exec. Tier-mode read/write users hit this on Ctrl+E.
  const showForbidden =
    session.status === "error" && session.errorCode === "E_FORBIDDEN";

  // Reconnect-failed (gave up after MAX_RECONNECT_ATTEMPTS).
  const showGivenUp =
    session.status === "error" &&
    (session.errorCode === "E_RECONNECT_FAILED" ||
      session.errorCode === "E_RECONNECT_GAVE_UP");

  const showReconnecting = session.status === "reconnecting";

  if (!showIdleWarn && !showNoShell && !showForbidden && !showGivenUp && !showReconnecting) {
    return null;
  }

  if (showIdleWarn) {
    const secondsLeft = Math.max(
      1,
      Math.ceil((idleWarnUntil - now) / 1000),
    );
    return (
      <BannerShell tone="yellow" instant>
        <span>
          session will close in{" "}
          <span className="tabular-nums">{secondsLeft}s</span> due to
          inactivity. type any key to keep it alive.
        </span>
      </BannerShell>
    );
  }

  if (showNoShell) {
    return (
      <BannerShell tone="red" instant>
        <span>
          this container has no shell on PATH. pick another container
          or pod.
        </span>
        <BannerButton onClick={() => closeSession(session.id)}>
          close
        </BannerButton>
      </BannerShell>
    );
  }

  if (showForbidden) {
    return (
      <BannerShell tone="red" instant>
        <span>
          {session.errorMessage ??
            "your role does not allow exec into this pod. contact your cluster admin."}
        </span>
        <BannerButton onClick={() => closeSession(session.id)}>close</BannerButton>
      </BannerShell>
    );
  }

  if (showGivenUp) {
    return (
      <BannerShell tone="red" instant>
        <span>
          {session.errorMessage ?? "couldn't reconnect."} the shell on
          the apiserver is gone — reconnecting opens a fresh session.
        </span>
        <BannerButton onClick={() => closeSession(session.id)}>
          close
        </BannerButton>
      </BannerShell>
    );
  }

  // showReconnecting
  const attempt = session.reconnectAttempt ?? 1;
  return (
    <BannerShell tone="yellow">
      <span className="flex items-center gap-1.5">
        <RetryGlyph />
        <span>
          reconnecting{" "}
          <span className="text-ink-faint">
            (attempt <span className="tabular-nums">{attempt}</span>/4)
          </span>
        </span>
      </span>
      <BannerButton onClick={() => reconnectNow(session.id)}>
        retry now
      </BannerButton>
      <BannerButton onClick={() => giveUpReconnect(session.id)} tone="muted">
        give up
      </BannerButton>
    </BannerShell>
  );
}

function BannerShell({
  tone,
  instant,
  children,
}: {
  tone: "yellow" | "red";
  /** When true, banner appears instantly (errors). When false, banner
   *  fades in after 800ms so silent reconnects don't paint at all. */
  instant?: boolean;
  children: React.ReactNode;
}) {
  const surface =
    tone === "yellow"
      ? "border-yellow/40 bg-yellow-soft text-yellow"
      : "border-red/40 bg-red-soft text-red";
  return (
    <div
      className={cn(
        "flex h-7 shrink-0 items-center gap-2 border-b px-3 font-mono text-[11px]",
        surface,
        !instant && "animate-[fadein_200ms_ease-in_800ms_both]",
      )}
      style={
        instant
          ? undefined
          : { opacity: 0, animationFillMode: "forwards" as const }
      }
    >
      {children}
    </div>
  );
}

function BannerButton({
  children,
  onClick,
  tone,
}: {
  children: React.ReactNode;
  onClick: () => void;
  tone?: "muted";
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "ml-auto flex h-5 items-center rounded border px-1.5 font-mono text-[10.5px] transition-colors",
        tone === "muted"
          ? "ml-1 border-border bg-surface text-ink-muted hover:border-border-strong hover:text-ink"
          : "border-current bg-surface/80 hover:bg-surface",
      )}
    >
      {children}
    </button>
  );
}

function RetryGlyph() {
  return (
    <svg
      width="11"
      height="11"
      viewBox="0 0 11 11"
      aria-hidden
      className="animate-spin"
    >
      <path
        d="M9.2 5.5a3.7 3.7 0 1 1-1.1-2.6"
        stroke="currentColor"
        strokeWidth="1.4"
        fill="none"
        strokeLinecap="round"
      />
      <path
        d="M9.5 1.7v2.6h-2.6"
        stroke="currentColor"
        strokeWidth="1.4"
        fill="none"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function PillBadge({
  tone,
  label,
  sub,
}: {
  tone: "yellow" | "green" | "red" | "neutral";
  label: string;
  sub: string | null;
}) {
  const dot =
    tone === "green"
      ? "bg-green"
      : tone === "yellow"
        ? "bg-yellow"
        : tone === "red"
          ? "bg-red"
          : "bg-ink-faint/50";
  const text =
    tone === "green"
      ? "text-green"
      : tone === "yellow"
        ? "text-yellow"
        : tone === "red"
          ? "text-red"
          : "text-ink-muted";
  const surface =
    tone === "green"
      ? "border-green/40 bg-green-soft"
      : tone === "yellow"
        ? "border-yellow/40 bg-yellow-soft"
        : tone === "red"
          ? "border-red/40 bg-red-soft"
          : "border-border bg-surface-2/40";

  return (
    <div
      className={cn(
        "flex h-5 shrink-0 items-center gap-1.5 rounded border px-1.5 font-mono text-[10.5px] leading-none",
        surface,
        text,
      )}
    >
      <span
        aria-hidden
        className={cn(
          "block size-1.5 rounded-full",
          dot,
          tone === "yellow" && "animate-pulse",
        )}
      />
      <span>{label}</span>
      {sub && (
        <>
          <span className="text-ink-faint">·</span>
          <span className="tabular-nums text-ink-muted">{sub}</span>
        </>
      )}
    </div>
  );
}
