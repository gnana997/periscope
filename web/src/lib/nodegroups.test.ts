import { describe, expect, it } from "vitest";
import { classifyDrift } from "./nodegroups";
import type { NodegroupSummary } from "./types";

function ng(overrides: Partial<NodegroupSummary>): NodegroupSummary {
  return {
    name: "ng",
    status: "ACTIVE",
    amiType: "AL2023_x86_64_STANDARD",
    customAmi: false,
    desiredSize: 1,
    minSize: 1,
    maxSize: 1,
    healthIssueCount: 0,
    driftComputed: false,
    ...overrides,
  };
}

describe("classifyDrift", () => {
  it("custom AMI always returns kind=custom, even when drift is computed elsewhere", () => {
    const got = classifyDrift(
      ng({
        customAmi: true,
        driftComputed: true,
        isBehind: true,
        daysBehind: 30,
      }),
    );
    expect(got).toEqual({ kind: "custom" });
  });

  it("non-custom AMI without driftComputed returns kind=uncomputed (PR-2 default)", () => {
    expect(classifyDrift(ng({}))).toEqual({ kind: "uncomputed" });
  });

  it("driftComputed + not behind = current", () => {
    expect(
      classifyDrift(ng({ driftComputed: true, isBehind: false })),
    ).toEqual({ kind: "current" });
  });

  it("driftComputed + behind surfaces days + latest release", () => {
    const got = classifyDrift(
      ng({
        driftComputed: true,
        isBehind: true,
        daysBehind: 14,
        latestReleaseVersion: "1.30.0-20240901",
      }),
    );
    expect(got).toEqual({
      kind: "behind",
      days: 14,
      latest: "1.30.0-20240901",
    });
  });

  it("driftComputed + behind without daysBehind defaults to 0", () => {
    expect(
      classifyDrift(ng({ driftComputed: true, isBehind: true })),
    ).toEqual({ kind: "behind", days: 0, latest: undefined });
  });
});
