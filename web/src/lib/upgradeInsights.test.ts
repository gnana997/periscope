import { describe, expect, it } from "vitest";
import { ApiError, isBackendNotEKS } from "./api";
import { sortInsightsByStatus } from "./upgradeInsights";
import type { UpgradeInsightSummary } from "./types";

function row(
  id: string,
  status: UpgradeInsightSummary["status"],
  name?: string,
): UpgradeInsightSummary {
  return {
    id,
    name: name ?? id,
    category: "UPGRADE_READINESS",
    status,
  };
}

describe("sortInsightsByStatus", () => {
  it("ERROR/WARNING/UNKNOWN/PASSING — worst first", () => {
    const sorted = sortInsightsByStatus([
      row("a-passing", "PASSING"),
      row("b-warning", "WARNING"),
      row("c-error", "ERROR"),
      row("d-unknown", "UNKNOWN"),
    ]);
    expect(sorted.map((r) => r.id)).toEqual([
      "c-error",
      "b-warning",
      "d-unknown",
      "a-passing",
    ]);
  });

  it("ties broken by display name", () => {
    const sorted = sortInsightsByStatus([
      row("zzz", "ERROR", "z-name"),
      row("aaa", "ERROR", "a-name"),
      row("mmm", "ERROR", "m-name"),
    ]);
    expect(sorted.map((r) => r.id)).toEqual(["aaa", "mmm", "zzz"]);
  });

  it("falls back to id when name is empty", () => {
    const sorted = sortInsightsByStatus([
      { id: "z", name: "", category: "UPGRADE_READINESS", status: "ERROR" },
      { id: "a", name: "", category: "UPGRADE_READINESS", status: "ERROR" },
    ]);
    expect(sorted.map((r) => r.id)).toEqual(["a", "z"]);
  });

  it("does not mutate the input array", () => {
    const input = [row("b", "WARNING"), row("a", "ERROR")];
    const snapshot = input.map((r) => r.id);
    sortInsightsByStatus(input);
    expect(input.map((r) => r.id)).toEqual(snapshot);
  });

  it("returns empty array unchanged", () => {
    expect(sortInsightsByStatus([])).toEqual([]);
  });
});

describe("isBackendNotEKS", () => {
  it("matches a 422 ApiError carrying the E_BACKEND_NOT_EKS code", () => {
    const err = new ApiError(
      "422 Unprocessable Entity on /api/clusters/x/eks/upgrade-insights",
      422,
      JSON.stringify({ code: "E_BACKEND_NOT_EKS", message: "non-EKS" }),
    );
    expect(isBackendNotEKS(err)).toBe(true);
  });

  it("rejects 422 errors that don't carry the code", () => {
    const err = new ApiError("422 something else", 422, "{}");
    expect(isBackendNotEKS(err)).toBe(false);
  });

  it("rejects non-422 errors", () => {
    const err = new ApiError("500 internal", 500, "E_BACKEND_NOT_EKS");
    expect(isBackendNotEKS(err)).toBe(false);
  });

  it("rejects non-ApiError values", () => {
    expect(isBackendNotEKS(new Error("plain"))).toBe(false);
    expect(isBackendNotEKS(undefined)).toBe(false);
    expect(isBackendNotEKS(null)).toBe(false);
    expect(isBackendNotEKS({ status: 422 })).toBe(false);
  });
});
