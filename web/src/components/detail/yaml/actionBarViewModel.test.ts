// actionBarViewModel — exhaustive coverage of the ApplyState → view
// mapping. One test per reducer state.

import { describe, expect, it } from "vitest";
import { actionBarViewModel } from "./actionBarViewModel";
import type { ConflictInfo } from "../../../hooks/useApplyState";

const CONFLICT: ConflictInfo = {
  managers: [{ name: "argo-cd", category: "CONTROLLER" }],
  fields: ["spec.replicas"],
};

describe("actionBarViewModel", () => {
  it("Idle: not busy, default labels, no chip, banner not owning", () => {
    const v = actionBarViewModel({ kind: "Idle" });
    expect(v).toEqual({
      busy: false,
      applyLabel: "apply",
      dryRunLabel: "dry-run",
      statusChip: { kind: "none" },
      bannerOwnsState: false,
    });
  });

  it("DryRunning: busy, dry-run button shows 'dry-running…', chip is 'validating…'", () => {
    const v = actionBarViewModel({ kind: "DryRunning" });
    expect(v).toMatchObject({
      busy: true,
      applyLabel: "apply",
      dryRunLabel: "dry-running…",
      statusChip: { kind: "in-flight", label: "validating…" },
      bannerOwnsState: false,
    });
  });

  it("Submitting: busy, apply button shows 'applying…', chip is 'applying…'", () => {
    const v = actionBarViewModel({ kind: "Submitting" });
    expect(v).toMatchObject({
      busy: true,
      applyLabel: "applying…",
      dryRunLabel: "dry-run",
      statusChip: { kind: "in-flight", label: "applying…" },
      bannerOwnsState: false,
    });
  });

  it("Forcing: busy, apply button shows 'forcing…', banner owns state", () => {
    // While the force-apply is in flight the conflict banner stays
    // visible (with its own loading indicator on the Force button);
    // the action bar's chip steps aside so the operator doesn't see
    // two competing in-flight indicators.
    const v = actionBarViewModel({ kind: "Forcing", conflict: CONFLICT });
    expect(v).toMatchObject({
      busy: true,
      applyLabel: "forcing…",
      bannerOwnsState: true,
    });
  });

  it("ForceRequired: not actionable from bar, banner owns state, no chip", () => {
    const v = actionBarViewModel({ kind: "ForceRequired", conflict: CONFLICT });
    expect(v).toEqual({
      busy: true,
      applyLabel: "apply",
      dryRunLabel: "dry-run",
      statusChip: { kind: "none" },
      bannerOwnsState: true,
    });
  });

  it("Success: not busy, chip is 'applied', default labels", () => {
    const v = actionBarViewModel({ kind: "Success" });
    expect(v).toMatchObject({
      busy: false,
      applyLabel: "apply",
      dryRunLabel: "dry-run",
      statusChip: { kind: "applied" },
      bannerOwnsState: false,
    });
  });

  it("Error: banner owns state, bar disabled, no chip duplication", () => {
    const v = actionBarViewModel({
      kind: "Error",
      error: { status: 422, message: "validation failed" },
    });
    expect(v).toEqual({
      busy: true,
      applyLabel: "apply",
      dryRunLabel: "dry-run",
      statusChip: { kind: "none" },
      bannerOwnsState: true,
    });
  });
});
