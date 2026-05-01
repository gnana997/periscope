import { cn } from "../lib/cn";
import { useExecSessions } from "./ExecSessionsContext";
import { clusterStripeColor } from "./clusterColor";
import type { ExecSessionMeta, SessionStatus } from "./types";

/**
 * CollapsedBar — the always-visible 24px strip at the bottom of the app
 * when sessions exist but the drawer is collapsed. Click anywhere to
 * expand back to the full drawer.
 *
 * Replaces the earlier floating ClosedPill: a real strip honors the
 * "drawer is part of the layout" mental model, and gives operators a
 * persistent indicator that shells are running, where they are, and how
 * many.
 */

const PRIORITY: Record<SessionStatus, number> = {
  error: 4,
  connecting: 3,
  connected: 2,
  closed: 1,
};

function aggregateStatus(sessions: ExecSessionMeta[]): SessionStatus {
  let worst: SessionStatus = "closed";
  for (const s of sessions) {
    if (PRIORITY[s.status] > PRIORITY[worst]) worst = s.status;
  }
  return worst;
}

const STATUS_DOT: Record<SessionStatus, string> = {
  connecting: "bg-yellow",
  connected: "bg-green",
  closed: "bg-ink-faint/50",
  error: "bg-red",
};

const IS_MAC =
  typeof navigator !== "undefined" &&
  /mac|iphone|ipad|ipod/i.test(navigator.platform);

export function CollapsedBar() {
  const { sessions, activeSessionId, setDrawerOpen } = useExecSessions();
  const live = sessions.filter(
    (s) => s.status === "connecting" || s.status === "connected",
  );
  const featured =
    sessions.find((s) => s.id === activeSessionId) ??
    live[0] ??
    sessions[sessions.length - 1];
  const aggregate = aggregateStatus(sessions);

  return (
    <button
      type="button"
      onClick={() => setDrawerOpen(true)}
      title="Expand terminal drawer"
      className="group flex h-6 shrink-0 items-center gap-3 border-t border-border bg-surface-2/60 px-3 text-[10.5px] text-ink-muted transition-colors hover:bg-surface-2 hover:text-ink"
    >
      {/* aggregate status dot */}
      <span aria-hidden className="flex shrink-0 items-center gap-1.5">
        <span
          className={cn(
            "block size-1.5 rounded-full",
            STATUS_DOT[aggregate],
            aggregate === "connecting" && "animate-pulse",
          )}
        />
      </span>

      {/* featured session — only when there's a meaningful one */}
      {featured && (
        <span className="flex min-w-0 items-center gap-1.5 font-mono">
          <span
            aria-hidden
            className="block h-2 w-[2px] shrink-0 rounded-full"
            style={{ background: clusterStripeColor(featured.cluster) }}
          />
          <span className="text-ink-faint">{featured.cluster}</span>
          <span className="text-ink-faint">·</span>
          <span className="min-w-0 truncate text-ink">{featured.pod}</span>
        </span>
      )}

      {/* count + live ratio */}
      <span className="ml-auto flex shrink-0 items-center gap-3 font-mono">
        <span>
          <span className="tabular-nums">{sessions.length}</span>{" "}
          {sessions.length === 1 ? "shell" : "shells"}
          {live.length !== sessions.length && (
            <span className="text-ink-faint">
              {" "}
              (<span className="tabular-nums">{live.length}</span> live)
            </span>
          )}
        </span>

        <span aria-hidden className="hidden items-center gap-0.5 sm:flex">
          <kbd className="rounded border border-border bg-surface px-1 py-px text-[9.5px] leading-none text-ink-faint">
            {IS_MAC ? "⌘" : "Ctrl"}
          </kbd>
          <kbd className="rounded border border-border bg-surface px-1 py-px text-[9.5px] leading-none text-ink-faint">
            `
          </kbd>
        </span>

        <svg
          width="10"
          height="10"
          viewBox="0 0 10 10"
          aria-hidden
          className="text-ink-faint transition-transform group-hover:-translate-y-px"
        >
          <path
            d="M2 6l3-3 3 3"
            stroke="currentColor"
            strokeWidth="1.4"
            fill="none"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </span>
    </button>
  );
}
