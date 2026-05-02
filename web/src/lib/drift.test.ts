import { describe, expect, it } from "vitest";
import { describeDrift, PERISCOPE_MANAGER } from "./drift";
import type { ManagedFieldsEntry, ResourceMeta } from "./api";

// Helpers --------------------------------------------------------------

function entry(
  manager: string,
  time: string,
  fieldsV1: Record<string, unknown> = { "f:spec": { "f:replicas": {} } },
): ManagedFieldsEntry {
  return {
    manager,
    operation: "Apply",
    apiVersion: "apps/v1",
    fieldsType: "FieldsV1",
    fieldsV1,
    time,
  };
}

function meta(
  resourceVersion: string,
  generation: number,
  entries: ManagedFieldsEntry[] = [],
): ResourceMeta {
  return { resourceVersion, generation, managedFields: entries };
}

// Tests ----------------------------------------------------------------

describe("describeDrift", () => {
  it("returns null when meta is identical", () => {
    const m = meta("100", 1, [entry("kustomize-controller", "2026-05-03T01:00:00Z")]);
    expect(describeDrift(m, m)).toBeNull();
  });

  it("returns null when only resourceVersion bumped (status writer)", () => {
    // Same managedFields times, just rv bumped — kubelet status update.
    const pristine = meta("100", 1, [entry("kubelet", "2026-05-03T01:00:00Z")]);
    const fresh = meta("101", 1, [entry("kubelet", "2026-05-03T01:00:00Z")]);
    expect(describeDrift(pristine, fresh)).toBeNull();
  });

  it("detects a controller writing to spec", () => {
    const pristine = meta("100", 1, [
      entry("kustomize-controller", "2026-05-03T01:00:00Z"),
    ]);
    const fresh = meta("105", 2, [
      entry("kustomize-controller", "2026-05-03T01:05:00Z", {
        "f:spec": { "f:replicas": {} },
      }),
    ]);
    const drift = describeDrift(pristine, fresh);
    expect(drift).not.toBeNull();
    expect(drift?.manager).toBe("kustomize-controller");
    expect(drift?.category).toBe("GITOPS");
    expect(drift?.paths).toEqual(["spec.replicas"]);
    expect(drift?.generationChanged).toBe(true);
    expect(drift?.at).toBe("2026-05-03T01:05:00Z");
  });

  it("filters out our own writes (periscope-spa)", () => {
    const pristine = meta("100", 1, [entry("kustomize-controller", "2026-05-03T01:00:00Z")]);
    const fresh = meta("105", 2, [
      entry("kustomize-controller", "2026-05-03T01:00:00Z"),
      entry(PERISCOPE_MANAGER, "2026-05-03T01:05:00Z"),
    ]);
    expect(describeDrift(pristine, fresh)).toBeNull();
  });

  it("picks the newest manager when multiple have written since pristine", () => {
    const pristine = meta("100", 1, [
      entry("kustomize-controller", "2026-05-03T01:00:00Z"),
    ]);
    // Two new writers; HPA wrote most recently.
    const fresh = meta("110", 2, [
      entry("kustomize-controller", "2026-05-03T01:00:00Z"),
      entry("kubectl-edit", "2026-05-03T01:02:00Z"),
      entry("horizontal-pod-autoscaler", "2026-05-03T01:05:00Z"),
    ]);
    const drift = describeDrift(pristine, fresh);
    expect(drift?.manager).toBe("horizontal-pod-autoscaler");
    expect(drift?.category).toBe("CONTROLLER");
  });

  it("returns null when fieldsV1 walk is empty", () => {
    // Writer with a managedFields entry but no fieldsV1 (or empty) —
    // they bumped rv without owning any visualizable field.
    const pristine = meta("100", 1, [entry("a", "2026-05-03T01:00:00Z")]);
    const fresh = meta("105", 1, [
      {
        manager: "weird-writer",
        operation: "Update",
        apiVersion: "apps/v1",
        fieldsType: "FieldsV1",
        fieldsV1: {},
        time: "2026-05-03T01:05:00Z",
      },
    ]);
    expect(describeDrift(pristine, fresh)).toBeNull();
  });

  it("flags generationChanged correctly when generation stayed the same", () => {
    // Real-world example: a controller wrote a status-adjacent field
    // that doesn't bump generation but does land in fieldsV1.
    const pristine = meta("100", 5, [entry("a", "2026-05-03T01:00:00Z")]);
    const fresh = meta("105", 5, [
      entry("kubectl-rollout", "2026-05-03T01:05:00Z"),
    ]);
    const drift = describeDrift(pristine, fresh);
    expect(drift).not.toBeNull();
    expect(drift?.generationChanged).toBe(false);
  });

  it("returns null when newest fresh entry is older than youngest pristine", () => {
    // Pristine had entries up to 01:00; fresh has only stale entries.
    // (Edge case: managedFields entries got dropped/reorganized.)
    const pristine = meta("100", 1, [entry("a", "2026-05-03T01:00:00Z")]);
    const fresh = meta("105", 1, [entry("b", "2026-05-03T00:30:00Z")]);
    expect(describeDrift(pristine, fresh)).toBeNull();
  });
});
