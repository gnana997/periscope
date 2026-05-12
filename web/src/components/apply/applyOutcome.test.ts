import { describe, it, expect } from "vitest";
import { deriveApplyOutcome } from "./applyOutcome";
import type { DocResult } from "../../hooks/useApplyYamlState";

function results(entries: Array<[string, DocResult["state"]]>): Map<string, DocResult> {
  const m = new Map<string, DocResult>();
  for (const [id, state] of entries) m.set(id, { state });
  return m;
}

describe("deriveApplyOutcome", () => {
  it("returns idle when no docs and no batch ran", () => {
    const o = deriveApplyOutcome({
      busy: "idle",
      lastBatchKind: "none",
      results: new Map(),
      validCount: 0,
    });
    expect(o.kind).toBe("idle");
  });

  it("returns pre-apply when docs are parsed but no batch ran", () => {
    const o = deriveApplyOutcome({
      busy: "idle",
      lastBatchKind: "none",
      results: new Map(),
      validCount: 3,
    });
    expect(o.kind).toBe("pre-apply");
    expect(o.totalCount).toBe(3);
  });

  it("returns running while a batch is in flight (any kind)", () => {
    for (const busy of ["apply", "dry-run"] as const) {
      const o = deriveApplyOutcome({
        busy,
        lastBatchKind: "none",
        results: results([["a", "pending"]]),
        validCount: 1,
      });
      expect(o.kind).toBe("running");
    }
  });

  it("returns success when every valid doc succeeded after apply", () => {
    const o = deriveApplyOutcome({
      busy: "idle",
      lastBatchKind: "apply",
      results: results([
        ["a", "success"],
        ["b", "success"],
      ]),
      validCount: 2,
    });
    expect(o.kind).toBe("success");
    expect(o.successCount).toBe(2);
  });

  it("returns partial when at least one conflict survives an apply", () => {
    const o = deriveApplyOutcome({
      busy: "idle",
      lastBatchKind: "apply",
      results: results([
        ["a", "success"],
        ["b", "conflict"],
      ]),
      validCount: 2,
    });
    expect(o.kind).toBe("partial");
    expect(o.successCount).toBe(1);
    expect(o.conflictCount).toBe(1);
  });

  it("returns partial when at least one failure survives an apply", () => {
    const o = deriveApplyOutcome({
      busy: "idle",
      lastBatchKind: "apply",
      results: results([
        ["a", "success"],
        ["b", "failure"],
      ]),
      validCount: 2,
    });
    expect(o.kind).toBe("partial");
    expect(o.failureCount).toBe(1);
  });

  it("does NOT show success/partial after a dry-run completes", () => {
    // Dry-run populates results with state=success too. The footer
    // must stay in pre-apply so the operator clicks apply next.
    const o = deriveApplyOutcome({
      busy: "idle",
      lastBatchKind: "dry-run",
      results: results([
        ["a", "success"],
        ["b", "success"],
      ]),
      validCount: 2,
    });
    expect(o.kind).toBe("pre-apply");
  });

  it("flips back to success when a forced conflict resolves to success", () => {
    // After runApply leaves a conflict and forceApplyOne flips it
    // to success, lastBatchKind is still 'apply' and every entry is
    // success → outcome must read 'success' so the close button shows.
    const o = deriveApplyOutcome({
      busy: "idle",
      lastBatchKind: "apply",
      results: results([
        ["a", "success"],
        ["b", "success"],
      ]),
      validCount: 2,
    });
    expect(o.kind).toBe("success");
  });

  it("aborted batch with no terminal results stays in pre-apply", () => {
    // If the operator cancels before any worker finishes, results
    // map is empty after the abort path. Treat as still-ready.
    const o = deriveApplyOutcome({
      busy: "idle",
      lastBatchKind: "apply",
      results: new Map(),
      validCount: 2,
    });
    expect(o.kind).toBe("pre-apply");
  });
});
