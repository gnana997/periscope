// actionBarViewModel — pure mapping from the unified ApplyState to
// the props the ActionBar renders. Mirrors the bannerViewModel
// pattern: testable without React, lets the JSX stay dumb.
//
// The action bar cares about three things:
//   1. Which (if any) action is currently in flight, so we render the
//      spinner / "dry-running…" / "applying…" label on the right button.
//   2. Whether everything else should be disabled.
//   3. The "applied" / error chip visibility in the status segment.

import type { ApplyState } from "../../../hooks/useApplyState";

export type ActionBarView = {
  /** "make a change first" gate is the caller's job (depends on dirty). */
  busy: boolean;
  /** Render apply button label. */
  applyLabel: "apply" | "applying…" | "forcing…";
  /** Render dry-run button label. */
  dryRunLabel: "dry-run" | "dry-running…";
  /** Status chip the bar renders to the right of the schema pill. */
  statusChip: StatusChip;
  /**
   * When true, the action-bar JSX should treat conflict/error as
   * being handled by the dedicated conflict banner — i.e. don't also
   * render the chip + don't surface the error inside the bar's
   * status area. Used when the unified state machine is in a state
   * the banner owns.
   */
  bannerOwnsState: boolean;
};

export type StatusChip =
  | { kind: "none" }
  | { kind: "applied" }
  | { kind: "in-flight"; label: "validating…" | "applying…" }
  | { kind: "error"; message: string };

export function actionBarViewModel(state: ApplyState): ActionBarView {
  switch (state.kind) {
    case "Idle":
      return {
        busy: false,
        applyLabel: "apply",
        dryRunLabel: "dry-run",
        statusChip: { kind: "none" },
        bannerOwnsState: false,
      };

    case "DryRunning":
      return {
        busy: true,
        applyLabel: "apply",
        dryRunLabel: "dry-running…",
        statusChip: { kind: "in-flight", label: "validating…" },
        bannerOwnsState: false,
      };

    case "Submitting":
      return {
        busy: true,
        applyLabel: "applying…",
        dryRunLabel: "dry-run",
        statusChip: { kind: "in-flight", label: "applying…" },
        bannerOwnsState: false,
      };

    case "Forcing":
      return {
        busy: true,
        applyLabel: "forcing…",
        dryRunLabel: "dry-run",
        statusChip: { kind: "in-flight", label: "applying…" },
        bannerOwnsState: true,
      };

    case "ForceRequired":
      // Banner is visible; action bar buttons disabled. The status
      // chip stays empty — the banner is the operator's surface.
      return {
        busy: true,
        applyLabel: "apply",
        dryRunLabel: "dry-run",
        statusChip: { kind: "none" },
        bannerOwnsState: true,
      };

    case "Success":
      // Brief "applied" chip; the hook auto-dismisses (timer for
      // dry-run, onCommitSuccess closes the editor for commit).
      return {
        busy: false,
        applyLabel: "apply",
        dryRunLabel: "dry-run",
        statusChip: { kind: "applied" },
        bannerOwnsState: false,
      };

    case "Error":
      // Banner is visible (Retry / Dismiss). Bar buttons disabled
      // until the operator dismisses or retries.
      return {
        busy: true,
        applyLabel: "apply",
        dryRunLabel: "dry-run",
        statusChip: { kind: "none" },
        bannerOwnsState: true,
      };
  }
}
