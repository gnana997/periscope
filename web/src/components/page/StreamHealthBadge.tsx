// StreamHealthBadge — small inline indicator that shows whether a list
// page is receiving live updates via SSE, retrying after a blip, or
// has fallen back to polling.
//
// Lives in the PageHeader trailing area, sized to sit alongside the
// existing failing/pending chips. Renders nothing when status is
// undefined — most list pages don't have streams enabled and the
// badge is irrelevant for them.
//
// Visual rules (subtle by default, louder on degraded states):
//
//   live              -> small green pill, always shown so users know
//                        the dashboard is current. "live" text + dot.
//   connecting        -> amber pill with pulsing dot. First open OR
//                        ctx switch (cluster, namespace).
//   reconnecting      -> amber pill with pulsing dot. After a transport
//                        blip or server-emitted disruption.
//   polling_fallback  -> grey pill, "polling" text. Stream is dead;
//                        UI is still correct but laggier (15-30s).
//
// All four use the same compact layout so they don't shift the header
// as state transitions. Functional design, theme-consistent — share
// with design and iterate when ready.

import { cn } from "../../lib/cn";
import type { StreamStatus } from "../../hooks/useResourceStream";

interface StreamHealthBadgeProps {
  status: StreamStatus | undefined;
}

export function StreamHealthBadge({ status }: StreamHealthBadgeProps) {
  if (!status) return null;

  const config = STATUS_CONFIG[status];

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 font-mono text-[11px]",
        config.classes,
      )}
      title={config.title}
      role="status"
      aria-live="polite"
    >
      <span
        className={cn(
          "block size-1.5 rounded-full bg-current",
          config.pulse && "animate-pulse",
        )}
      />
      {config.label}
    </span>
  );
}

const STATUS_CONFIG: Record<
  StreamStatus,
  { label: string; title: string; classes: string; pulse: boolean }
> = {
  live: {
    label: "live",
    title: "Live updates from the cluster",
    classes: "border-green/40 bg-green-soft text-green",
    pulse: false,
  },
  connecting: {
    label: "connecting",
    title: "Opening the live update stream",
    classes: "border-yellow/40 bg-yellow-soft text-yellow",
    pulse: true,
  },
  reconnecting: {
    label: "reconnecting",
    title: "Live update stream interrupted; reconnecting",
    classes: "border-yellow/40 bg-yellow-soft text-yellow",
    pulse: true,
  },
  polling_fallback: {
    label: "polling",
    title:
      "Live updates unavailable; refreshing on a polling interval instead",
    classes: "border-border-strong bg-surface text-ink-muted",
    pulse: false,
  },
};
