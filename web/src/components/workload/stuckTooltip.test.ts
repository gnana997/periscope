import { describe, it, expect } from "vitest";
import { formatStuckTooltip } from "./stuckTooltip";
import type { StuckState } from "../../lib/types";

const MIN = 60 * 1000;
const HR = 60 * MIN;
const DAY = 24 * HR;

describe("formatStuckTooltip", () => {
  it("formats progress-deadline-exceeded with minutes duration", () => {
    const s: StuckState = { reason: "progress-deadline-exceeded", sinceMs: 14 * MIN };
    expect(formatStuckTooltip(s)).toBe(
      "rollout exceeded progress deadline · controller has stopped retrying · 14m since",
    );
  });

  it("formats progress-deadline-exceeded with hours duration", () => {
    const s: StuckState = { reason: "progress-deadline-exceeded", sinceMs: 2 * HR };
    expect(formatStuckTooltip(s)).toBe(
      "rollout exceeded progress deadline · controller has stopped retrying · 2h since",
    );
  });

  it("formats stalled with minutes", () => {
    const s: StuckState = { reason: "stalled", sinceMs: 12 * MIN };
    expect(formatStuckTooltip(s)).toBe("rollout stalled · 12m since last progress");
  });

  it("formats stalled with days", () => {
    const s: StuckState = { reason: "stalled", sinceMs: 3 * DAY };
    expect(formatStuckTooltip(s)).toBe("rollout stalled · 3d since last progress");
  });

  it("falls through gracefully on unknown reason", () => {
    const s = { reason: "future-reason" as unknown as StuckState["reason"], sinceMs: 5 * MIN };
    expect(formatStuckTooltip(s as StuckState)).toBe("rollout stuck · 5m since");
  });

  it("clamps sub-second durations to 0s", () => {
    const s: StuckState = { reason: "stalled", sinceMs: 0 };
    expect(formatStuckTooltip(s)).toBe("rollout stalled · 0s since last progress");
  });
});
