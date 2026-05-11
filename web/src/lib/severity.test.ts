// severity unit tests — pure logic only. The vitest config is
// `environment: "node"`, no jsdom, no RTL; the JSX shell in
// SeverityChip is visually verified, not unit-tested. See the
// architectural-cross-cuts section of the #166 plan.

import { describe, it, expect } from "vitest";
import {
  combineCounts,
  compactLabel,
  hasAnyFindings,
  severityScore,
  stateLabel,
  verboseLabel,
  worstSeverity,
  zero,
} from "./severity";

const counts = (overrides: Partial<typeof zero> = {}) => ({
  ...zero,
  ...overrides,
});

describe("severityScore", () => {
  it("ranks critical above high above medium above low", () => {
    expect(severityScore(counts({ critical: 1 }))).toBeGreaterThan(
      severityScore(counts({ high: 9 })),
    );
    expect(severityScore(counts({ high: 1 }))).toBeGreaterThan(
      severityScore(counts({ medium: 9 })),
    );
    expect(severityScore(counts({ medium: 1 }))).toBeGreaterThan(
      severityScore(counts({ low: 9 })),
    );
  });
  it("returns 0 for the zero value", () => {
    expect(severityScore(zero)).toBe(0);
  });
  it("ignores informational so noise can't outrank real findings", () => {
    expect(severityScore(counts({ informational: 100 }))).toBe(0);
  });
});

describe("worstSeverity", () => {
  it("returns the highest non-zero bucket", () => {
    expect(worstSeverity(counts({ critical: 1, high: 99 }))).toBe("critical");
    expect(worstSeverity(counts({ high: 1, medium: 99 }))).toBe("high");
    expect(worstSeverity(counts({ low: 1 }))).toBe("low");
  });
  it("returns null when all buckets are zero", () => {
    expect(worstSeverity(zero)).toBeNull();
  });
  it("ignores informational — never returns it", () => {
    expect(worstSeverity(counts({ informational: 100 }))).toBeNull();
  });
});

describe("combineCounts", () => {
  it("sums every bucket across slices", () => {
    const a = counts({ critical: 2, high: 1 });
    const b = counts({ critical: 1, medium: 3 });
    const c = counts({ informational: 5 });
    expect(combineCounts(a, b, c)).toEqual({
      critical: 3,
      high: 1,
      medium: 3,
      low: 0,
      informational: 5,
    });
  });
  it("returns zero for an empty list", () => {
    expect(combineCounts()).toEqual(zero);
  });
  it("does not mutate input slices", () => {
    const a = counts({ critical: 2 });
    const b = counts({ critical: 1 });
    combineCounts(a, b);
    expect(a.critical).toBe(2);
    expect(b.critical).toBe(1);
  });
});

describe("compactLabel", () => {
  it("renders C/H/M parts for non-zero buckets", () => {
    expect(compactLabel(counts({ critical: 2, high: 5, medium: 12 }))).toBe(
      "2C · 5H · 12M",
    );
  });
  it("drops zero buckets", () => {
    expect(compactLabel(counts({ critical: 2, medium: 12 }))).toBe(
      "2C · 12M",
    );
  });
  it("returns empty string when top-3 buckets are zero", () => {
    expect(compactLabel(zero)).toBe("");
    // Low / info do not appear in compact form.
    expect(compactLabel(counts({ low: 5, informational: 9 }))).toBe("");
  });
});

describe("verboseLabel", () => {
  it("spells out each non-zero bucket", () => {
    expect(
      verboseLabel(
        counts({ critical: 2, high: 5, medium: 12, low: 3, informational: 0 }),
      ),
    ).toBe("2 critical · 5 high · 12 medium · 3 low");
  });
  it("includes info bucket when non-zero", () => {
    expect(verboseLabel(counts({ informational: 4 }))).toBe("4 info");
  });
  it("returns empty string for the zero value", () => {
    expect(verboseLabel(zero)).toBe("");
  });
});

describe("hasAnyFindings", () => {
  it("returns false for the zero value", () => {
    expect(hasAnyFindings(zero)).toBe(false);
  });
  it("returns true if any single bucket is non-zero", () => {
    expect(hasAnyFindings(counts({ informational: 1 }))).toBe(true);
    expect(hasAnyFindings(counts({ critical: 1 }))).toBe(true);
  });
});

describe("stateLabel", () => {
  it("returns operator-readable strings for each state", () => {
    expect(stateLabel("clean")).toBe("clean");
    expect(stateLabel("partial")).toBe("partial scan");
    expect(stateLabel("non-ecr")).toContain("non-ECR");
    expect(stateLabel("pending")).toBe("scan pending");
    expect(stateLabel("unscanned")).toBe("not scanned");
  });
});
