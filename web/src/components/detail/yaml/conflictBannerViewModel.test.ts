// bannerViewModel — pure mapping from ApplyState to render contract.
// Covers visibility, variant selection, manager formatting, button
// labels, and the disabled/loading flag matrix across all five
// reducer states.
//
// The JSX (ConflictBanner component) is intentionally thin and not
// tested here — its only job is to consume the BannerView this
// function produces.

import { describe, expect, it } from "vitest";
import {
  bannerViewModel,
  formatManager,
  type BannerView,
} from "./conflictBannerViewModel";
import type { ApplyState, ConflictInfo } from "../../../hooks/useApplyState";

const CONFLICT: ConflictInfo = {
  managers: [
    { name: "argo-cd", category: "CONTROLLER" },
    { name: "alice@example.com", category: "HUMAN" },
  ],
  fields: ["spec.replicas", "metadata.labels.team"],
};

describe("formatManager", () => {
  it("with category: 'name (category-lowercased)'", () => {
    expect(formatManager({ name: "argo-cd", category: "CONTROLLER" })).toBe(
      "argo-cd (controller)",
    );
    expect(
      formatManager({ name: "alice@example.com", category: "HUMAN" }),
    ).toBe("alice@example.com (human)");
    expect(
      formatManager({ name: "helm", category: "HELM" }),
    ).toBe("helm (helm)");
    expect(
      formatManager({ name: "flux-source-controller", category: "GITOPS" }),
    ).toBe("flux-source-controller (gitops)");
  });

  it("without category: bare name", () => {
    expect(formatManager({ name: "periscope-spa" })).toBe("periscope-spa");
  });
});

describe("bannerViewModel", () => {
  describe("hidden variants", () => {
    it("Idle → not visible", () => {
      const v = bannerViewModel({ kind: "Idle" });
      expect(v).toEqual<BannerView>({ visible: false });
    });

    it("Submitting → not visible", () => {
      // The action bar handles the in-flight indicator; the banner
      // shows nothing until the apply resolves.
      const v = bannerViewModel({ kind: "Submitting" });
      expect(v).toEqual<BannerView>({ visible: false });
    });

    it("DryRunning → not visible", () => {
      const v = bannerViewModel({ kind: "DryRunning" });
      expect(v).toEqual<BannerView>({ visible: false });
    });

    it("Success → not visible (brief flash owned by the action bar)", () => {
      const v = bannerViewModel({ kind: "Success" });
      expect(v).toEqual<BannerView>({ visible: false });
    });
  });

  describe("ForceRequired", () => {
    const state: ApplyState = { kind: "ForceRequired", conflict: CONFLICT };

    it("variant + managers + fields + enabled buttons", () => {
      const v = bannerViewModel(state);
      expect(v).toEqual<BannerView>({
        visible: true,
        variant: "force-required",
        managers: ["argo-cd (controller)", "alice@example.com (human)"],
        fields: ["spec.replicas", "metadata.labels.team"],
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
      });
    });

    it("preserves manager order and field order from the conflict info", () => {
      const reverseConflict: ConflictInfo = {
        managers: [
          { name: "alice@example.com", category: "HUMAN" },
          { name: "argo-cd", category: "CONTROLLER" },
        ],
        fields: ["metadata.labels.team", "spec.replicas"],
      };
      const v = bannerViewModel({
        kind: "ForceRequired",
        conflict: reverseConflict,
      });
      expect(v).toMatchObject({
        managers: ["alice@example.com (human)", "argo-cd (controller)"],
        fields: ["metadata.labels.team", "spec.replicas"],
      });
    });
  });

  describe("Forcing", () => {
    const state: ApplyState = { kind: "Forcing", conflict: CONFLICT };

    it("same conflict info as ForceRequired; primary loading; both buttons disabled", () => {
      const v = bannerViewModel(state);
      expect(v).toEqual<BannerView>({
        visible: true,
        variant: "forcing",
        managers: ["argo-cd (controller)", "alice@example.com (human)"],
        fields: ["spec.replicas", "metadata.labels.team"],
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
      });
    });
  });

  describe("Error", () => {
    it("4xx: variant + error info + retry/dismiss buttons", () => {
      const state: ApplyState = {
        kind: "Error",
        error: { status: 422, message: "validation failed: replicas < 0" },
      };
      const v = bannerViewModel(state);
      expect(v).toEqual<BannerView>({
        visible: true,
        variant: "error",
        error: { status: 422, message: "validation failed: replicas < 0" },
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
      });
    });

    it("5xx: same shape, different status", () => {
      // Per the apply state machine: status alone doesn't change
      // variant. The banner copy might style 4xx differently from
      // 5xx (validation vs server error) — that's the JSX's job;
      // the view model just hands over status + message.
      const state: ApplyState = {
        kind: "Error",
        error: { status: 503, message: "service unavailable" },
      };
      const v = bannerViewModel(state);
      expect(v).toMatchObject({
        visible: true,
        variant: "error",
        error: { status: 503, message: "service unavailable" },
      });
    });
  });

  describe("structural invariants", () => {
    it("managers/fields are absent on the error variant", () => {
      const state: ApplyState = {
        kind: "Error",
        error: { status: 500, message: "internal" },
      };
      const v = bannerViewModel(state);
      if (!v.visible) throw new Error("expected visible banner");
      expect(v.managers).toBeUndefined();
      expect(v.fields).toBeUndefined();
    });

    it("error is absent on the force-required / forcing variants", () => {
      const fr = bannerViewModel({ kind: "ForceRequired", conflict: CONFLICT });
      const fc = bannerViewModel({ kind: "Forcing", conflict: CONFLICT });
      if (!fr.visible || !fc.visible) throw new Error("expected visible banner");
      expect(fr.error).toBeUndefined();
      expect(fc.error).toBeUndefined();
    });
  });
});
