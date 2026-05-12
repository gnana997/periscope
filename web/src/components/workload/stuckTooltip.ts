// stuckTooltip — pure label helper shared by <StuckBadge> and the
// workload-describe banners. Split out of StuckBadge.tsx so that file
// only exports a component (react-refresh constraint) while this
// module is freely reusable from anywhere that needs the wording.

import type { StuckState } from "../../lib/types";
import { formatDurationMs } from "../../lib/format";

/** Plain-language label used in both the badge tooltip and the
 *  detail-pane banner. Pure — vitest pins the exact wording so the
 *  badge and banner stay in sync. */
export function formatStuckTooltip(stuck: StuckState): string {
  const dur = formatDurationMs(stuck.sinceMs);
  switch (stuck.reason) {
    case "progress-deadline-exceeded":
      return `rollout exceeded progress deadline · controller has stopped retrying · ${dur} since`;
    case "stalled":
      return `rollout stalled · ${dur} since last progress`;
    default:
      // Unknown future reason — render gracefully instead of throwing.
      return `rollout stuck · ${dur} since`;
  }
}
