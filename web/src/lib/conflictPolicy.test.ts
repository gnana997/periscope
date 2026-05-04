import { describe, expect, it } from "vitest";
import { ApiError } from "./api";
import { analyzeConflict, formatBlockingMessage } from "./conflictPolicy";

// Helper: build a minimal apiserver Status JSON body for a 409.
function statusBody(causes: Array<{ manager: string; field: string }>): string {
  return JSON.stringify({
    kind: "Status",
    status: "Failure",
    reason: "Conflict",
    code: 409,
    details: {
      causes: causes.map((c) => ({
        reason: "FieldManagerConflict",
        message: `conflict with "${c.manager}" using ${c.manager}: field ${c.field}`,
        field: c.field,
      })),
    },
  });
}

function conflictError(causes: Array<{ manager: string; field: string }>): ApiError {
  return new ApiError("409 Conflict on /api/...", 409, statusBody(causes));
}

describe("analyzeConflict", () => {
  it("returns null for non-ApiError errors", () => {
    expect(analyzeConflict(new Error("boom"))).toBeNull();
    expect(analyzeConflict("nope")).toBeNull();
    expect(analyzeConflict(null)).toBeNull();
  });

  it("returns null for non-409 ApiErrors", () => {
    const err = new ApiError("403 forbidden", 403, "{}");
    expect(analyzeConflict(err)).toBeNull();
  });

  it("returns null when body is unparseable", () => {
    const err = new ApiError("409", 409, "<html>not json</html>");
    expect(analyzeConflict(err)).toBeNull();
  });

  it("returns null when body has no FieldManagerConflict causes", () => {
    const err = new ApiError("409", 409, JSON.stringify({ details: { causes: [] } }));
    expect(analyzeConflict(err)).toBeNull();
  });

  it("classifies kubectl-client-side-apply as safe (the Rancher case)", () => {
    const err = conflictError([
      { manager: "kubectl-client-side-apply", field: ".spec.replicas" },
    ]);
    const analysis = analyzeConflict(err);
    expect(analysis).not.toBeNull();
    expect(analysis!.allSafeToTakeover).toBe(true);
    expect(analysis!.firstBlocking).toBeUndefined();
    expect(analysis!.causes).toHaveLength(1);
    expect(analysis!.causes[0].field).toBe("spec.replicas");
    expect(analysis!.causes[0].manager.category).toBe("HUMAN");
  });

  it("classifies an unknown manager as safe (UNKNOWN bucket)", () => {
    const err = conflictError([
      { manager: "some-bespoke-manager", field: ".spec.replicas" },
    ]);
    const analysis = analyzeConflict(err);
    expect(analysis!.allSafeToTakeover).toBe(true);
    expect(analysis!.causes[0].manager.category).toBe("UNKNOWN");
  });

  it("classifies kustomize-controller as blocking (GITOPS)", () => {
    const err = conflictError([
      { manager: "kustomize-controller", field: ".spec.replicas" },
    ]);
    const analysis = analyzeConflict(err);
    expect(analysis!.allSafeToTakeover).toBe(false);
    expect(analysis!.firstBlocking?.manager.category).toBe("GITOPS");
    expect(analysis!.firstBlocking?.manager.display).toBe("kustomize-controller");
  });

  it("classifies HPA as blocking (CONTROLLER)", () => {
    const err = conflictError([
      { manager: "horizontal-pod-autoscaler", field: ".spec.replicas" },
    ]);
    const analysis = analyzeConflict(err);
    expect(analysis!.allSafeToTakeover).toBe(false);
    expect(analysis!.firstBlocking?.manager.category).toBe("CONTROLLER");
  });

  it("blocks when ANY cause is unsafe (mixed managers)", () => {
    const err = conflictError([
      { manager: "kubectl-client-side-apply", field: ".metadata.labels.app" },
      { manager: "kustomize-controller", field: ".spec.replicas" },
    ]);
    const analysis = analyzeConflict(err);
    expect(analysis!.allSafeToTakeover).toBe(false);
    expect(analysis!.firstBlocking?.manager.display).toBe("kustomize-controller");
  });

  it("normalises field paths (strips leading dot, unquotes selectors)", () => {
    const err = conflictError([
      { manager: "kubectl-edit", field: '.spec.containers[name="app"].image' },
    ]);
    const analysis = analyzeConflict(err);
    expect(analysis!.causes[0].field).toBe("spec.containers[name=app].image");
  });

  it("ignores causes with non-FieldManagerConflict reasons", () => {
    const body = JSON.stringify({
      details: {
        causes: [
          {
            reason: "FieldValueInvalid",
            message: "spec.replicas: Invalid value",
            field: ".spec.replicas",
          },
        ],
      },
    });
    const err = new ApiError("409", 409, body);
    expect(analyzeConflict(err)).toBeNull();
  });
});

describe("formatBlockingMessage", () => {
  it("includes action, manager display, field, consequence, and prefer", () => {
    const err = conflictError([
      { manager: "kustomize-controller", field: ".spec.replicas" },
    ]);
    const analysis = analyzeConflict(err)!;
    const msg = formatBlockingMessage("scale", analysis.firstBlocking!);
    expect(msg).toContain("scale blocked by kustomize-controller");
    expect(msg).toContain("spec.replicas");
    expect(msg).toContain("Flux will revert");
    expect(msg).toContain("Edit the source repo");
  });

  it("omits the prefer suffix when the registry has none", () => {
    // notification-controller has consequence text but empty prefer.
    const err = conflictError([
      { manager: "notification-controller", field: ".spec.x" },
    ]);
    const analysis = analyzeConflict(err)!;
    const msg = formatBlockingMessage("update labels", analysis.firstBlocking!);
    expect(msg).toContain("update labels blocked by notification-controller");
    // No trailing extra space / dangling separator.
    expect(msg.endsWith(" ")).toBe(false);
  });
});
