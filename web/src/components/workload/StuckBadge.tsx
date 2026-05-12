// StuckBadge — compact red badge for the Workloads pages.
//
// The badge is dumb: every decision (reason, sinceMs) was made by the
// backend in internal/k8s/stuck.go. This component only renders.
// `formatStuckTooltip` lives in stuckTooltip.ts (and is re-exported
// here for callsites that already had it on this path) so this file
// stays component-only — required by react-refresh.

import type { StuckState } from "../../lib/types";
import { formatStuckTooltip } from "./stuckTooltip";
import { cn } from "../../lib/cn";


interface Props {
  stuck: StuckState;
  className?: string;
}

export function StuckBadge({ stuck, className }: Props) {
  const tooltip = formatStuckTooltip(stuck);
  return (
    <span
      className={cn(
        "inline-flex items-center font-mono text-[11.5px] uppercase tracking-wide text-red",
        className,
      )}
      title={tooltip}
      aria-label={tooltip}
      role="status"
    >
      · stuck ·
    </span>
  );
}
