// toApplyResult — pure mapping from any error thrown by the apply
// pipeline (api.applyResource, buildApplyBody) into the ApplyResult
// shape the reducer consumes. The hook's React + async glue is
// verified by manual testing on the kind dev cluster; this file
// covers the branching that drives reducer state.

import { describe, expect, it } from "vitest";
import { toApplyResult } from "./useApplyLifecycle";
import { ApiError } from "../lib/api";
import { MultiDocumentError, YamlParseError } from "../lib/yamlPatch";
import type { ConflictInfo } from "./useApplyState";

const SAMPLE_CONFLICT: ConflictInfo = {
  managers: [{ name: "argo-cd", category: "CONTROLLER" }],
  fields: ["spec.replicas"],
};

describe("toApplyResult", () => {
  describe("ApiError 409", () => {
    it("with parseable conflict → ApplyResult carrying ConflictInfo", () => {
      const err = new ApiError("apply failed", 409, "conflict body");
      const result = toApplyResult(err, () => SAMPLE_CONFLICT);
      expect(result).toEqual({
        ok: false,
        status: 409,
        message: "conflict body",
        conflict: SAMPLE_CONFLICT,
      });
    });

    it("without parseable conflict → ApplyResult with status 409 but no conflict field", () => {
      // Proxy stripped the body, or the apiserver returned a non-
      // FieldManagerConflict 409. Reducer's resolveFrom routes this
      // to Error (not ForceRequired with an empty banner).
      const err = new ApiError("apply failed", 409, "");
      const result = toApplyResult(err, () => null);
      expect(result).toEqual({
        ok: false,
        status: 409,
        message: "apply failed",
      });
      // Discriminating on the result shape: the conflict key is
      // absent, not undefined.
      if (!result.ok) {
        expect("conflict" in result).toBe(false);
      }
    });

    it("prefers bodyText for message; falls back to err.message; final fallback string", () => {
      const withBody = new ApiError("ignored", 409, "explicit body");
      expect(toApplyResult(withBody, () => SAMPLE_CONFLICT)).toMatchObject({
        message: "explicit body",
      });
      const noBody = new ApiError("err message", 409, "");
      expect(toApplyResult(noBody, () => SAMPLE_CONFLICT)).toMatchObject({
        message: "err message",
      });
      const totallyBlank = new ApiError("", 409, "");
      expect(toApplyResult(totallyBlank, () => SAMPLE_CONFLICT)).toMatchObject({
        message: "Apply conflict",
      });
    });
  });

  describe("ApiError non-409", () => {
    it.each([400, 403, 422, 500, 502, 503])(
      "%i → ApplyResult with that status",
      (status) => {
        const err = new ApiError("err", status, `bodyText for ${status}`);
        const result = toApplyResult(err);
        expect(result).toEqual({
          ok: false,
          status,
          message: `bodyText for ${status}`,
        });
      },
    );

    it("ignores the conflict parser for non-409 (would be a contract violation otherwise)", () => {
      // Defensive: if a buggy parser returned a ConflictInfo for a 500,
      // we'd misroute the operator into ForceRequired. The mapper
      // short-circuits on status !== 409.
      const err = new ApiError("err", 500, "internal");
      const result = toApplyResult(err, () => SAMPLE_CONFLICT);
      if (!result.ok) {
        expect("conflict" in result).toBe(false);
      }
    });
  });

  describe("pre-network errors", () => {
    it("MultiDocumentError → status 0 with message verbatim", () => {
      const err = new MultiDocumentError(2);
      const result = toApplyResult(err);
      expect(result).toEqual({
        ok: false,
        status: 0,
        message: err.message,
      });
    });

    it("YamlParseError → status 0 with message verbatim", () => {
      const err = new YamlParseError("yaml: bad escape");
      const result = toApplyResult(err);
      expect(result).toEqual({
        ok: false,
        status: 0,
        message: "yaml: bad escape",
      });
    });

    it("plain Error → status 0 with its message", () => {
      const err = new Error("something broke");
      const result = toApplyResult(err);
      expect(result).toEqual({
        ok: false,
        status: 0,
        message: "something broke",
      });
    });

    it("non-Error throwable (string, undefined, object) → generic fallback", () => {
      expect(toApplyResult("oops")).toEqual({
        ok: false,
        status: 0,
        message: "apply failed",
      });
      expect(toApplyResult(undefined)).toEqual({
        ok: false,
        status: 0,
        message: "apply failed",
      });
      expect(toApplyResult({ weird: "object" })).toEqual({
        ok: false,
        status: 0,
        message: "apply failed",
      });
    });
  });
});
