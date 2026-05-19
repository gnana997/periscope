// applyStateReducer — exhaustive state-transition coverage for the
// SSA apply / conflict-banner state machine. Tests target the pure
// reducer; the hook glue (useApplyLifecycle) is tested separately.
//
// Source of truth for the state diagram: top-of-file comment in
// useApplyState.ts.

import { describe, expect, it } from "vitest";
import {
  applyStateReducer,
  initialApplyState,
  type ApplyState,
  type ConflictInfo,
} from "./useApplyState";

const SAMPLE_CONFLICT: ConflictInfo = {
  managers: [
    { name: "argo-cd", category: "CONTROLLER" },
    { name: "alice@example.com", category: "HUMAN" },
  ],
  fields: ["spec.replicas", "metadata.labels.team"],
};

// Helper: drives a state through a sequence of actions and returns
// the final state. Lets each test read as "starting at X, do Y, Z;
// assert end state."
function drive(
  start: ApplyState,
  ...actions: Parameters<typeof applyStateReducer>[1][]
): ApplyState {
  return actions.reduce(
    (s, a) => applyStateReducer(s, a),
    start as ApplyState,
  );
}

describe("applyStateReducer", () => {
  describe("Idle", () => {
    it("initial state is Idle", () => {
      expect(initialApplyState).toEqual({ kind: "Idle" });
    });

    it("submit → Submitting", () => {
      const next = applyStateReducer(initialApplyState, { type: "submit" });
      expect(next).toEqual({ kind: "Submitting" });
    });

    it("dryRun → DryRunning", () => {
      const next = applyStateReducer(initialApplyState, { type: "dryRun" });
      expect(next).toEqual({ kind: "DryRunning" });
    });

    it("ignores force / cancel / retry / dismiss while Idle", () => {
      // No-op transitions that shouldn't happen via the UI, but the
      // reducer must be defensive: never advance to a state with
      // missing required data (e.g. Forcing without a conflict).
      const idle: ApplyState = { kind: "Idle" };
      expect(applyStateReducer(idle, { type: "force" })).toEqual(idle);
      expect(applyStateReducer(idle, { type: "cancel" })).toEqual(idle);
      expect(applyStateReducer(idle, { type: "retry" })).toEqual(idle);
      expect(applyStateReducer(idle, { type: "dismiss" })).toEqual(idle);
    });

    it("ignores a stray 'resolved' while Idle (defensive)", () => {
      // A late-arriving resolve from a previously-cancelled submit
      // must not jolt Idle into another state.
      const idle: ApplyState = { kind: "Idle" };
      const next = applyStateReducer(idle, {
        type: "resolved",
        result: { ok: true },
      });
      expect(next).toEqual(idle);
    });
  });

  describe("Submitting", () => {
    const submitting: ApplyState = { kind: "Submitting" };

    it("resolved(ok) → Success", () => {
      // Success is a real state — the hook handles the next step
      // (invalidate cache + dismiss for commit; auto-clear timer for
      // dry-run). The reducer just records that the apply landed.
      const next = applyStateReducer(submitting, {
        type: "resolved",
        result: { ok: true },
      });
      expect(next).toEqual({ kind: "Success" });
    });

    it("resolved(409 with conflict) → ForceRequired carrying conflict info", () => {
      const next = applyStateReducer(submitting, {
        type: "resolved",
        result: {
          ok: false,
          status: 409,
          message: "Apply conflict",
          conflict: SAMPLE_CONFLICT,
        },
      });
      expect(next).toEqual({
        kind: "ForceRequired",
        conflict: SAMPLE_CONFLICT,
      });
    });

    it("resolved(409 without parseable conflict info) → Error (defensive)", () => {
      // The apiserver always returns conflict structure on 409, but a
      // network proxy or unparseable error body could strip it. If we
      // can't show the operator who owns what, ForceRequired is
      // useless — fall through to Error so they at least see a
      // meaningful message instead of a banner with empty fields.
      const next = applyStateReducer(submitting, {
        type: "resolved",
        result: { ok: false, status: 409, message: "Apply conflict" },
      });
      expect(next).toEqual({
        kind: "Error",
        error: { status: 409, message: "Apply conflict" },
      });
    });

    it.each([400, 403, 422, 500, 502, 503])(
      "resolved(%i) → Error",
      (status) => {
        const next = applyStateReducer(submitting, {
          type: "resolved",
          result: { ok: false, status, message: `status ${status}` },
        });
        expect(next).toEqual({
          kind: "Error",
          error: { status, message: `status ${status}` },
        });
      },
    );

    it("ignores submit / dryRun / force / cancel / retry / dismiss while Submitting", () => {
      // Can't cancel an in-flight request from the reducer; only the
      // apply() promise resolving advances the state. Re-clicking
      // submit during an in-flight apply must not start a second one.
      expect(applyStateReducer(submitting, { type: "submit" })).toEqual(submitting);
      expect(applyStateReducer(submitting, { type: "dryRun" })).toEqual(submitting);
      expect(applyStateReducer(submitting, { type: "force" })).toEqual(submitting);
      expect(applyStateReducer(submitting, { type: "cancel" })).toEqual(submitting);
      expect(applyStateReducer(submitting, { type: "retry" })).toEqual(submitting);
      expect(applyStateReducer(submitting, { type: "dismiss" })).toEqual(submitting);
    });
  });

  describe("DryRunning", () => {
    const dryRunning: ApplyState = { kind: "DryRunning" };

    it("resolved(ok) → Success", () => {
      const next = applyStateReducer(dryRunning, {
        type: "resolved",
        result: { ok: true },
      });
      expect(next).toEqual({ kind: "Success" });
    });

    it("resolved(409 with conflict) → ForceRequired (dry-run conflict info is useful)", () => {
      // Dry-run hit a conflict; the operator may decide to force-commit
      // from here. Same banner as a real-apply conflict; the Force
      // button just becomes a force-apply (not a force-dry-run, which
      // would be meaningless).
      const next = applyStateReducer(dryRunning, {
        type: "resolved",
        result: {
          ok: false,
          status: 409,
          message: "Apply conflict",
          conflict: SAMPLE_CONFLICT,
        },
      });
      expect(next).toEqual({
        kind: "ForceRequired",
        conflict: SAMPLE_CONFLICT,
      });
    });

    it("resolved(422) → Error (admission webhook rejection during dry-run)", () => {
      const next = applyStateReducer(dryRunning, {
        type: "resolved",
        result: { ok: false, status: 422, message: "denied by webhook" },
      });
      expect(next).toEqual({
        kind: "Error",
        error: { status: 422, message: "denied by webhook" },
      });
    });

    it("ignores submit / dryRun / force / cancel / retry / dismiss while DryRunning", () => {
      expect(applyStateReducer(dryRunning, { type: "submit" })).toEqual(dryRunning);
      expect(applyStateReducer(dryRunning, { type: "dryRun" })).toEqual(dryRunning);
      expect(applyStateReducer(dryRunning, { type: "force" })).toEqual(dryRunning);
      expect(applyStateReducer(dryRunning, { type: "cancel" })).toEqual(dryRunning);
      expect(applyStateReducer(dryRunning, { type: "retry" })).toEqual(dryRunning);
      expect(applyStateReducer(dryRunning, { type: "dismiss" })).toEqual(dryRunning);
    });
  });

  describe("ForceRequired", () => {
    const forceRequired: ApplyState = {
      kind: "ForceRequired",
      conflict: SAMPLE_CONFLICT,
    };

    it("force → Forcing carrying the same conflict info", () => {
      const next = applyStateReducer(forceRequired, { type: "force" });
      expect(next).toEqual({ kind: "Forcing", conflict: SAMPLE_CONFLICT });
    });

    it("cancel → Idle (conflict info discarded)", () => {
      const next = applyStateReducer(forceRequired, { type: "cancel" });
      expect(next).toEqual({ kind: "Idle" });
    });

    it("ignores submit / dryRun / retry / dismiss / resolved while ForceRequired", () => {
      expect(applyStateReducer(forceRequired, { type: "submit" })).toEqual(forceRequired);
      expect(applyStateReducer(forceRequired, { type: "dryRun" })).toEqual(forceRequired);
      expect(applyStateReducer(forceRequired, { type: "retry" })).toEqual(forceRequired);
      expect(applyStateReducer(forceRequired, { type: "dismiss" })).toEqual(forceRequired);
      expect(
        applyStateReducer(forceRequired, {
          type: "resolved",
          result: { ok: true },
        }),
      ).toEqual(forceRequired);
    });
  });

  describe("Forcing", () => {
    const forcing: ApplyState = {
      kind: "Forcing",
      conflict: SAMPLE_CONFLICT,
    };

    it("resolved(ok) → Success", () => {
      const next = applyStateReducer(forcing, {
        type: "resolved",
        result: { ok: true },
      });
      expect(next).toEqual({ kind: "Success" });
    });

    it("resolved(409) → ForceRequired with refreshed conflict (race condition)", () => {
      // Another manager re-claimed something between Submit and
      // Force. The apiserver returns a fresh 409 with the updated
      // conflict shape. Banner refreshes; operator decides again.
      const refreshed: ConflictInfo = {
        managers: [{ name: "argo-cd", category: "CONTROLLER" }],
        fields: ["spec.replicas"],
      };
      const next = applyStateReducer(forcing, {
        type: "resolved",
        result: {
          ok: false,
          status: 409,
          message: "Apply conflict",
          conflict: refreshed,
        },
      });
      expect(next).toEqual({ kind: "ForceRequired", conflict: refreshed });
    });

    it("resolved(500) → Error", () => {
      const next = applyStateReducer(forcing, {
        type: "resolved",
        result: { ok: false, status: 500, message: "internal" },
      });
      expect(next).toEqual({
        kind: "Error",
        error: { status: 500, message: "internal" },
      });
    });

    it("ignores submit / dryRun / force / cancel / retry / dismiss while Forcing", () => {
      expect(applyStateReducer(forcing, { type: "submit" })).toEqual(forcing);
      expect(applyStateReducer(forcing, { type: "dryRun" })).toEqual(forcing);
      expect(applyStateReducer(forcing, { type: "force" })).toEqual(forcing);
      expect(applyStateReducer(forcing, { type: "cancel" })).toEqual(forcing);
      expect(applyStateReducer(forcing, { type: "retry" })).toEqual(forcing);
      expect(applyStateReducer(forcing, { type: "dismiss" })).toEqual(forcing);
    });
  });

  describe("Success", () => {
    const success: ApplyState = { kind: "Success" };

    it("dismiss → Idle", () => {
      // The hook fires this either via a 1500ms timer (dry-run success)
      // or directly after onCommitSuccess runs (commit success).
      const next = applyStateReducer(success, { type: "dismiss" });
      expect(next).toEqual({ kind: "Idle" });
    });

    it("cancel → Idle (defensive, e.g. operator clicks Cancel during the success flash)", () => {
      const next = applyStateReducer(success, { type: "cancel" });
      expect(next).toEqual({ kind: "Idle" });
    });

    it("ignores submit / dryRun / force / retry / resolved while Success", () => {
      // The Success state is brief and informational. Action attempts
      // during it are no-ops; the operator should wait for the
      // auto-dismiss or hit Cancel.
      expect(applyStateReducer(success, { type: "submit" })).toEqual(success);
      expect(applyStateReducer(success, { type: "dryRun" })).toEqual(success);
      expect(applyStateReducer(success, { type: "force" })).toEqual(success);
      expect(applyStateReducer(success, { type: "retry" })).toEqual(success);
      expect(
        applyStateReducer(success, {
          type: "resolved",
          result: { ok: true },
        }),
      ).toEqual(success);
    });
  });

  describe("Error", () => {
    const errored: ApplyState = {
      kind: "Error",
      error: { status: 422, message: "validation failed" },
    };

    it("retry → Submitting (always; force-on-retry is intentionally not modelled)", () => {
      // Retry is a plain Submit. If the underlying conflict reproduces,
      // the operator will land back in ForceRequired and can decide
      // again. This loses one round-trip in the rare "Force errored,
      // I want to force again" case in exchange for a much smaller
      // state machine (no lastIntent tracking).
      const next = applyStateReducer(errored, { type: "retry" });
      expect(next).toEqual({ kind: "Submitting" });
    });

    it("dismiss → Idle", () => {
      const next = applyStateReducer(errored, { type: "dismiss" });
      expect(next).toEqual({ kind: "Idle" });
    });

    it("cancel → Idle (treated identically to dismiss)", () => {
      const next = applyStateReducer(errored, { type: "cancel" });
      expect(next).toEqual({ kind: "Idle" });
    });

    it("ignores submit / dryRun / force / resolved while Error", () => {
      // Operator must consciously choose retry or dismiss; we don't
      // allow a stale submit() click to start a new attempt without
      // first clearing the visible error.
      expect(applyStateReducer(errored, { type: "submit" })).toEqual(errored);
      expect(applyStateReducer(errored, { type: "dryRun" })).toEqual(errored);
      expect(applyStateReducer(errored, { type: "force" })).toEqual(errored);
      expect(
        applyStateReducer(errored, {
          type: "resolved",
          result: { ok: true },
        }),
      ).toEqual(errored);
    });
  });

  describe("end-to-end happy-path traces", () => {
    it("submit → 409 → force → ok → dismiss: through Success back to Idle", () => {
      const end = drive(
        initialApplyState,
        { type: "submit" },
        {
          type: "resolved",
          result: {
            ok: false,
            status: 409,
            message: "Apply conflict",
            conflict: SAMPLE_CONFLICT,
          },
        },
        { type: "force" },
        { type: "resolved", result: { ok: true } },
        { type: "dismiss" },
      );
      expect(end).toEqual({ kind: "Idle" });
    });

    it("submit → 409 → cancel: Idle without forcing", () => {
      const end = drive(
        initialApplyState,
        { type: "submit" },
        {
          type: "resolved",
          result: {
            ok: false,
            status: 409,
            message: "Apply conflict",
            conflict: SAMPLE_CONFLICT,
          },
        },
        { type: "cancel" },
      );
      expect(end).toEqual({ kind: "Idle" });
    });

    it("dryRun → ok → dismiss: through Success back to Idle (auto-clear simulated)", () => {
      const end = drive(
        initialApplyState,
        { type: "dryRun" },
        { type: "resolved", result: { ok: true } },
        { type: "dismiss" },
      );
      expect(end).toEqual({ kind: "Idle" });
    });

    it("dryRun → 409 → force → ok → dismiss: dry-run conflict resolved by commit-force", () => {
      // Operator clicks dry-run, gets a conflict warning, decides to
      // force-commit anyway. The Force action from ForceRequired
      // launches a commit (not another dry-run); the hook is
      // responsible for that side effect.
      const end = drive(
        initialApplyState,
        { type: "dryRun" },
        {
          type: "resolved",
          result: {
            ok: false,
            status: 409,
            message: "Apply conflict",
            conflict: SAMPLE_CONFLICT,
          },
        },
        { type: "force" },
        { type: "resolved", result: { ok: true } },
        { type: "dismiss" },
      );
      expect(end).toEqual({ kind: "Idle" });
    });

    it("submit → 500 → retry → ok → dismiss: error recovered via retry", () => {
      const end = drive(
        initialApplyState,
        { type: "submit" },
        {
          type: "resolved",
          result: { ok: false, status: 500, message: "internal" },
        },
        { type: "retry" },
        { type: "resolved", result: { ok: true } },
        { type: "dismiss" },
      );
      expect(end).toEqual({ kind: "Idle" });
    });
  });
});
