import { describe, expect, it } from "vitest";
import { ApiError } from "../lib/api";
import { buildCanIDecision } from "./useCanI";
import type { CanICheck } from "../lib/api";

const podDelete: CanICheck = {
  verb: "delete",
  group: "",
  resource: "pods",
  namespace: "default",
};

describe("buildCanIDecision", () => {
  it("disabled: returns optimistic placeholder (allowed=true, loading=true)", () => {
    // Repro for the bug fixed in this PR. Pre-fix, !enabled returned
    // allowed=false, contradicting the documented behavior and briefly
    // greying out every action button while the cluster prop settled.
    const d = buildCanIDecision({
      check: podDelete,
      enabled: false,
      isPending: false,
      isError: false,
      error: undefined,
      result: undefined,
      authzMode: "tier",
      tier: "admin",
    });
    expect(d.allowed).toBe(true);
    expect(d.loading).toBe(true);
    expect(d.reason).toBe("");
    expect(d.tooltip).toBe("");
  });

  it("loading: returns optimistic placeholder while query resolves", () => {
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: true,
      isError: false,
      error: undefined,
      result: undefined,
      authzMode: "tier",
      tier: "triage",
    });
    expect(d.allowed).toBe(true);
    expect(d.loading).toBe(true);
  });

  it("queryKey-changed-gap-render: still optimistic before fetch starts", () => {
    // Repro for the *second* bug: when the queryKey changes (e.g.,
    // user clicks a different pod whose namespace differs), there is a
    // synchronous render where TanStack Query has set up the new
    // observer but isFetching has not yet flipped true. In v5, isLoading
    // = isPending && isFetching, so isLoading is briefly false even
    // though there is no usable data. Using isPending instead covers
    // this gap — the test pins it.
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: true, // no data for this key yet
      isError: false,
      error: undefined,
      result: undefined,
      authzMode: "tier",
      tier: "admin",
    });
    expect(d.allowed).toBe(true);
    expect(d.loading).toBe(true);
  });

  it("resolved + allowed: forwards apiserver answer", () => {
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: false,
      isError: false,
      error: undefined,
      result: { allowed: true, reason: "" },
      authzMode: "tier",
      tier: "admin",
    });
    expect(d.allowed).toBe(true);
    expect(d.loading).toBe(false);
    expect(d.tooltip).toBe(""); // formatCanIDeniedReason returns "" when allowed
  });

  it("resolved + denied: builds tier-aware tooltip", () => {
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: false,
      isError: false,
      error: undefined,
      result: { allowed: false, reason: "" },
      authzMode: "tier",
      tier: "triage",
    });
    expect(d.allowed).toBe(false);
    expect(d.loading).toBe(false);
    // Tier-mode message mentions the user's tier explicitly.
    expect(d.tooltip).toContain("triage");
    expect(d.tooltip).toContain("delete pods");
  });

  it("resolved + apiserver-supplied reason: passes through", () => {
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: false,
      isError: false,
      error: undefined,
      result: { allowed: false, reason: "RBAC: forbidden: user has no role" },
      authzMode: "tier",
      tier: "triage",
    });
    expect(d.allowed).toBe(false);
    // When the apiserver supplies a substantive reason, it wins over
    // the mode-aware default.
    expect(d.tooltip).toContain("RBAC: forbidden");
  });

  it("error: 401 → auth_failed reason + 'session expired' copy", () => {
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: false,
      isError: true,
      error: new ApiError("unauthorized", 401, ""),
      result: undefined,
      authzMode: "tier",
      tier: "admin",
    });
    expect(d.allowed).toBe(false);
    expect(d.reason).toBe("auth_failed");
    expect(d.tooltip).toContain("session expired");
  });

  it("error: 504 → timeout reason + 'cluster unreachable' copy", () => {
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: false,
      isError: true,
      error: new ApiError("timeout", 504, ""),
      result: undefined,
      authzMode: "shared",
      tier: undefined,
    });
    expect(d.allowed).toBe(false);
    expect(d.reason).toBe("timeout");
    expect(d.tooltip).toContain("cluster unreachable");
  });

  it("error: non-ApiError → apiserver_unreachable reason", () => {
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: false,
      isError: true,
      error: new Error("network down"),
      result: undefined,
      authzMode: "shared",
      tier: undefined,
    });
    expect(d.allowed).toBe(false);
    expect(d.reason).toBe("apiserver_unreachable");
    expect(d.tooltip).toContain("cluster unreachable");
  });

  it("resolved + missing result entry: falls back to deny", () => {
    // Edge case: query.data exists but is shorter than checks[]. Should
    // never happen if the backend is correct, but defensive.
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: false,
      isError: false,
      error: undefined,
      result: undefined,
      authzMode: "shared",
      tier: undefined,
    });
    expect(d.allowed).toBe(false);
    expect(d.loading).toBe(false);
  });

  it("shared mode + no tier: tooltip mentions dashboard role", () => {
    const d = buildCanIDecision({
      check: podDelete,
      enabled: true,
      isPending: false,
      isError: false,
      error: undefined,
      result: { allowed: false, reason: "" },
      authzMode: "shared",
      tier: undefined,
    });
    expect(d.allowed).toBe(false);
    expect(d.tooltip).toContain("dashboard's K8s role");
  });
});
