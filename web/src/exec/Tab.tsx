import { useEffect, useState } from "react";
import { cn } from "../lib/cn";
import { clusterStripeColor } from "./clusterColor";
import type { ExecSessionMeta } from "./types";

interface TabProps {
  session: ExecSessionMeta;
  active: boolean;
  onFocus: () => void;
  onClose: () => void;
}

const STATUS_DOT: Record<ExecSessionMeta["status"], string> = {
  connecting: "bg-yellow",
  connected: "bg-green",
  closed: "bg-ink-faint/50",
  error: "bg-red",
};

const STATUS_LABEL: Record<ExecSessionMeta["status"], string> = {
  connecting: "connecting",
  connected: "connected",
  closed: "session ended",
  error: "errored",
};

function formatUptime(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const m = Math.floor(total / 60);
  const s = total % 60;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}

export function Tab({ session, active, onFocus, onClose }: TabProps) {
  // Pulse the status dot for ~600ms after each stdout burst.
  const [pulse, setPulse] = useState(false);
  useEffect(() => {
    if (session.status !== "connected") {
      setPulse(false);
      return;
    }
    setPulse(true);
    const t = window.setTimeout(() => setPulse(false), 600);
    return () => window.clearTimeout(t);
  }, [session.lastActivityAt, session.status]);

  // Re-render once a second so the uptime in the tooltip stays fresh.
  const [, force] = useState(0);
  useEffect(() => {
    if (session.status !== "connected") return;
    const id = window.setInterval(() => force((n) => n + 1), 1000);
    return () => window.clearInterval(id);
  }, [session.status]);

  const stripe = clusterStripeColor(session.cluster);
  const uptime = session.closedAt
    ? formatUptime(session.closedAt - session.createdAt)
    : formatUptime(Date.now() - session.createdAt);

  return (
    <button
      type="button"
      onClick={onFocus}
      title={[
        `${session.cluster} · ${session.namespace} · ${session.pod}`,
        session.container ? `container: ${session.container}` : null,
        `${STATUS_LABEL[session.status]} · ${uptime}`,
      ]
        .filter(Boolean)
        .join("\n")}
      className={cn(
        "group relative flex h-7 max-w-[260px] shrink-0 items-center gap-1.5 whitespace-nowrap border-r border-border pl-2.5 pr-1.5 font-mono text-[11.5px] transition-colors",
        active
          ? "bg-surface text-ink"
          : "bg-surface-2/60 text-ink-muted hover:bg-surface-2 hover:text-ink",
      )}
    >
      {/* cluster color stripe — left edge, full tab height */}
      <span
        aria-hidden
        className="absolute inset-y-0 left-0 w-[2px]"
        style={{ background: stripe }}
      />

      {/* status dot */}
      <span
        aria-hidden
        className={cn(
          "block size-1.5 shrink-0 rounded-full transition-opacity",
          STATUS_DOT[session.status],
          pulse && session.status === "connected" && "opacity-100",
          !pulse && session.status === "connected" && "opacity-70",
        )}
      />

      {/* cluster prefix — tiny inline pill, hidden on very narrow viewports */}
      <span className="hidden shrink-0 rounded-sm bg-surface-2/80 px-1 py-px font-mono text-[9.5px] uppercase tracking-[0.04em] text-ink-faint sm:inline-block">
        {session.cluster}
      </span>

      {/* pod name */}
      <span className="min-w-0 truncate">{session.pod}</span>

      {/* close X — visible on hover or when active */}
      <span
        role="button"
        tabIndex={-1}
        onClick={(e) => {
          e.stopPropagation();
          onClose();
        }}
        aria-label={`Close session for ${session.pod}`}
        className={cn(
          "ml-0.5 flex size-4 shrink-0 items-center justify-center rounded-sm text-ink-faint transition-all hover:bg-surface hover:text-ink",
          active ? "opacity-100" : "opacity-0 group-hover:opacity-100",
        )}
      >
        <svg width="8" height="8" viewBox="0 0 8 8" aria-hidden>
          <path
            d="M1.5 1.5l5 5M6.5 1.5l-5 5"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinecap="round"
          />
        </svg>
      </span>

      {/* active accent — burnt-orange underline */}
      {active && (
        <span
          aria-hidden
          className="absolute inset-x-0 -bottom-px h-px bg-accent"
        />
      )}
    </button>
  );
}
