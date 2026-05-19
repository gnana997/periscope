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
      "%i with non-JSON body → ApplyResult with raw bodyText as message",
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

    it("422 with apiserver Status JSON → extracts the human .message field", () => {
      // Repro for the wall-of-JSON banner display: when the bodyText
      // is the full Status object, parse out the human-readable
      // message so the banner shows the operator-actionable sentence
      // rather than the whole JSON envelope.
      const statusBody = JSON.stringify({
        kind: "Status",
        apiVersion: "v1",
        status: "Failure",
        message:
          'Deployment.apps "ct-scorer" is invalid: spec.replicas: Invalid value: -1: must be greater than or equal to 0',
        reason: "Invalid",
        code: 422,
      });
      const err = new ApiError("ignored", 422, statusBody);
      const result = toApplyResult(err);
      expect(result).toEqual({
        ok: false,
        status: 422,
        message:
          'Deployment.apps "ct-scorer" is invalid: spec.replicas: Invalid value: -1: must be greater than or equal to 0',
      });
    });

    it("malformed JSON body → falls back to raw bodyText (no info hidden)", () => {
      // A proxy-rewritten or otherwise-malformed body must still
      // surface to the operator. Extraction is best-effort.
      const err = new ApiError("err", 502, "Bad Gateway (raw HTML)");
      const result = toApplyResult(err);
      expect(result.ok).toBe(false);
      if (!result.ok) {
        expect(result.message).toBe("Bad Gateway (raw HTML)");
      }
    });

    it("JSON body without .message field → falls back to raw bodyText", () => {
      // Defensive: some non-Status JSON could parse but not carry
      // .message. Don't hide it.
      const err = new ApiError("err", 500, '{"foo": "bar"}');
      const result = toApplyResult(err);
      expect(result.ok).toBe(false);
      if (!result.ok) {
        expect(result.message).toBe('{"foo": "bar"}');
      }
    });

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
