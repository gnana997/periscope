// conflictBannerViewModel — pure ApplyState → render-contract mapping
// for ConflictBanner. Lives in its own file so the React component
// file (ConflictBanner.tsx) only exports the JSX wrapper — required
// for Vite's React-refresh to work across edits.

import type { ApplyState, ConflictManager } from "../../../hooks/useApplyState";

export type BannerAction = "force" | "cancel" | "retry" | "dismiss";

export type BannerButton = {
  label: string;
  action: BannerAction;
  disabled: boolean;
  loading: boolean;
};

export type BannerView =
  | { visible: false }
  | {
      visible: true;
      variant: "force-required" | "forcing" | "error";
      // Populated for force-required + forcing variants:
      managers?: string[];
      fields?: string[];
      // Populated for error variant:
      error?: { status: number; message: string };
      primary: BannerButton;
      secondary: BannerButton;
    };

export function bannerViewModel(state: ApplyState): BannerView {
  switch (state.kind) {
    case "Idle":
    case "Submitting":
    case "DryRunning":
    case "Success":
      // Banner is for the conflict / error surface only. Idle and the
      // two in-flight states are owned by the action bar; Success is
      // brief and auto-clears via the lifecycle hook.
      return { visible: false };

    case "ForceRequired":
      return {
        visible: true,
        variant: "force-required",
        managers: state.conflict.managers.map(formatManager),
        fields: state.conflict.fields,
        primary: {
          label: "Force apply",
          action: "force",
          disabled: false,
          loading: false,
        },
        secondary: {
          label: "Cancel",
          action: "cancel",
          disabled: false,
          loading: false,
        },
      };

    case "Forcing":
      return {
        visible: true,
        variant: "forcing",
        managers: state.conflict.managers.map(formatManager),
        fields: state.conflict.fields,
        primary: {
          label: "Force apply",
          action: "force",
          disabled: true,
          loading: true,
        },
        secondary: {
          label: "Cancel",
          action: "cancel",
          disabled: true,
          loading: false,
        },
      };

    case "Error":
      return {
        visible: true,
        variant: "error",
        error: state.error,
        primary: {
          label: "Retry",
          action: "retry",
          disabled: false,
          loading: false,
        },
        secondary: {
          label: "Dismiss",
          action: "dismiss",
          disabled: false,
          loading: false,
        },
      };
  }
}

// formatManager renders a ConflictManager as a display name. Exported
// for direct testing; used by bannerViewModel above to build the
// human-readable manager list shown in the banner body.
export function formatManager(m: ConflictManager): string {
  return m.category ? `${m.name} (${m.category.toLowerCase()})` : m.name;
}
