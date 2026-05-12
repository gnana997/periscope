import { describe, expect, it } from "vitest";
import { classifyBuildError } from "./useApplySubmit";
import { ManagedFieldsUnavailableError } from "../../lib/applyBodyBuilder";
import { MultiDocumentError, YamlParseError } from "../../lib/yamlPatch";

// classifyBuildError is the pure mapping layer between exceptions
// thrown by buildRetainedOwnershipBody and the SubmitState the form
// banner consumes. The rest of useApplySubmit is glue (react-query
// invalidate + abort controller + setState + api.applyResource), so
// we keep dedicated tests scoped to the part with branching logic.
describe("classifyBuildError", () => {
  it("ManagedFieldsUnavailableError → friendly retry message", () => {
    const got = classifyBuildError(new ManagedFieldsUnavailableError());
    expect(got).toEqual({
      kind: "error",
      message: "Ownership info is still loading — try again in a moment.",
      isConflict: false,
    });
  });

  it("MultiDocumentError → surfaces the package error message verbatim", () => {
    const e = new MultiDocumentError(3);
    const got = classifyBuildError(e);
    expect(got).toMatchObject({
      kind: "error",
      message: e.message,
      isConflict: false,
    });
  });

  it("YamlParseError → surfaces the package error message verbatim", () => {
    const e = new YamlParseError("yaml: did not find expected node content");
    const got = classifyBuildError(e);
    expect(got).toMatchObject({
      kind: "error",
      message: "yaml: did not find expected node content",
      isConflict: false,
    });
  });

  it("returns null for unrecognised errors so the hook can re-throw", () => {
    // Generic Error → null → submit() will `throw e` so the unexpected
    // surfaces in DevTools rather than being swallowed.
    expect(classifyBuildError(new Error("something else"))).toBeNull();
    expect(classifyBuildError(new TypeError("nope"))).toBeNull();
    expect(classifyBuildError("string thrown")).toBeNull();
    expect(classifyBuildError(undefined)).toBeNull();
  });

  it("never produces isConflict=true (build errors are pre-network)", () => {
    // Sanity guard: classifyBuildError must not collide with 409
    // post-network conflict handling. isConflict is reserved for the
    // ApiError 409 path further down in submit().
    const cases: unknown[] = [
      new ManagedFieldsUnavailableError(),
      new MultiDocumentError(2),
      new YamlParseError("bad"),
    ];
    for (const c of cases) {
      const got = classifyBuildError(c);
      // Narrowing: classified errors are always the "error" variant.
      expect(got?.kind).toBe("error");
      if (got?.kind === "error") {
        expect(got.isConflict).toBe(false);
      }
    }
  });
});
