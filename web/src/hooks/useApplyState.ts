// useApplyState — pure state machine for the SSA apply lifecycle.
//
//   Idle           ─submit()──────▶ Submitting
//   Idle           ─dryRun()──────▶ DryRunning
//   Submitting     ─ok─────────────▶ Success
//   Submitting     ─409 + conflict─▶ ForceRequired
//   Submitting     ─other──────────▶ Error
//   DryRunning     ─ok─────────────▶ Success
//   DryRunning     ─409 + conflict─▶ ForceRequired
//   DryRunning     ─other──────────▶ Error
//   ForceRequired  ─force()────────▶ Forcing
//   ForceRequired  ─cancel()───────▶ Idle
//   Forcing        ─ok─────────────▶ Success
//   Forcing        ─409 + conflict─▶ ForceRequired   (race; banner refreshed)
//   Forcing        ─other──────────▶ Error
//   Success        ─dismiss()──────▶ Idle
//   Error          ─retry()────────▶ Submitting
//   Error          ─dismiss()──────▶ Idle
//
// The reducer is pure. Side effects (api.applyResource calls, timers,
// react-query invalidation) live in useApplyLifecycle, which wraps
// this reducer and exposes action callbacks that combine dispatch +
// side effect. Components NEVER call dispatch directly; they call
// hook actions or read state.
//
// Manager category is the full lib/managers ManagerCategory union so
// the conflict banner can render the registry's classification
// verbatim. The reducer is otherwise dependency-free.

import type { ManagerCategory } from "../lib/managers";

export type ConflictManager = {
  name: string;
  category?: ManagerCategory;
};

export type ConflictInfo = {
  managers: ConflictManager[];
  fields: string[];
};

export type ApplyError = {
  status: number;
  message: string;
};

export type ApplyResult =
  | { ok: true }
  | { ok: false; status: number; message: string; conflict?: ConflictInfo };

export type ApplyState =
  | { kind: "Idle" }
  | { kind: "DryRunning" }
  | { kind: "Submitting" }
  | { kind: "ForceRequired"; conflict: ConflictInfo }
  | { kind: "Forcing"; conflict: ConflictInfo }
  | { kind: "Error"; error: ApplyError }
  | { kind: "Success" };

export type ApplyAction =
  | { type: "submit" }
  | { type: "dryRun" }
  | { type: "force" }
  | { type: "cancel" }
  | { type: "retry" }
  | { type: "dismiss" }
  | { type: "resolved"; result: ApplyResult };

export const initialApplyState: ApplyState = { kind: "Idle" };

export function applyStateReducer(
  state: ApplyState,
  action: ApplyAction,
): ApplyState {
  switch (state.kind) {
    case "Idle":
      if (action.type === "submit") return { kind: "Submitting" };
      if (action.type === "dryRun") return { kind: "DryRunning" };
      return state;

    case "Submitting":
    case "DryRunning":
    case "Forcing":
      if (action.type === "resolved") return resolveFrom(action.result);
      return state;

    case "ForceRequired":
      if (action.type === "force") {
        return { kind: "Forcing", conflict: state.conflict };
      }
      if (action.type === "cancel") return { kind: "Idle" };
      return state;

    case "Success":
      if (action.type === "dismiss" || action.type === "cancel") {
        return { kind: "Idle" };
      }
      return state;

    case "Error":
      if (action.type === "retry") return { kind: "Submitting" };
      if (action.type === "dismiss" || action.type === "cancel") {
        return { kind: "Idle" };
      }
      return state;
  }
}

// resolveFrom maps an ApplyResult to the next state. Shared between
// Submitting, DryRunning, and Forcing branches because they react
// identically: ok → Success, parseable 409 → ForceRequired, anything
// else → Error. The "parseable 409" guard sends an info-less 409 to
// Error rather than rendering an empty banner — without manager +
// field info, a force-apply button has nothing meaningful to say.
function resolveFrom(result: ApplyResult): ApplyState {
  if (result.ok) return { kind: "Success" };
  if (result.status === 409 && result.conflict) {
    return { kind: "ForceRequired", conflict: result.conflict };
  }
  return {
    kind: "Error",
    error: { status: result.status, message: result.message },
  };
}
