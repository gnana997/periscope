import { describe, expect, it } from "vitest";

import {
  addRowToList,
  patchRowInList,
  removeRowFromList,
} from "./listShape";
import type { Pod, PodList, ResourceListResponse } from "./types";

function pod(name: string, namespace: string, phase = "Running"): Pod {
  return {
    name,
    namespace,
    phase,
    ready: "1/1",
    restarts: 0,
    createdAt: "2026-05-03T00:00:00Z",
  };
}

function podList(...pods: Pod[]): PodList {
  return { pods };
}

describe("listShape primitives", () => {
  describe("removeRowFromList", () => {
    it("filters the named row out", () => {
      const before = podList(pod("a", "ns"), pod("b", "ns"));
      const after = removeRowFromList(before, "pods", { name: "a", namespace: "ns" }) as PodList;
      expect(after.pods).toHaveLength(1);
      expect(after.pods[0].name).toBe("b");
    });

    it("returns the same reference when no match", () => {
      const before = podList(pod("a", "ns"));
      const after = removeRowFromList(before, "pods", { name: "missing", namespace: "ns" });
      expect(after).toBe(before);
    });

    it("returns the same reference when the kind is unknown", () => {
      const before = podList(pod("a", "ns"));
      const after = removeRowFromList(before, "widgets", { name: "a", namespace: "ns" });
      expect(after).toBe(before);
    });
  });

  describe("patchRowInList", () => {
    it("patches the named row", () => {
      const before = podList(pod("a", "ns", "Pending"));
      const after = patchRowInList<Pod>(before, "pods", { name: "a", namespace: "ns" }, (r) => ({
        ...r,
        phase: "Running",
      })) as PodList;
      expect(after.pods[0].phase).toBe("Running");
    });

    it("returns the same reference when the row doesn't match", () => {
      const before = podList(pod("a", "ns"));
      const after = patchRowInList<Pod>(before, "pods", { name: "missing", namespace: "ns" }, (r) => r);
      expect(after).toBe(before);
    });
  });

  describe("addRowToList", () => {
    it("appends a row that doesn't yet exist", () => {
      const before = podList(pod("a", "ns"));
      const after = addRowToList<Pod>(before, "pods", pod("b", "ns")) as PodList;
      expect(after.pods.map((p) => p.name)).toEqual(["a", "b"]);
    });

    it("replaces the existing row when one matches name+namespace", () => {
      const before = podList(pod("a", "ns", "Pending"), pod("b", "ns"));
      const after = addRowToList<Pod>(before, "pods", pod("a", "ns", "Running")) as PodList;
      expect(after.pods).toHaveLength(2);
      // Order preserved (in-place replace, not append).
      expect(after.pods[0].name).toBe("a");
      expect(after.pods[0].phase).toBe("Running");
      expect(after.pods[1].name).toBe("b");
    });

    it("treats namespace mismatch as a different row", () => {
      const before = podList(pod("a", "ns-one"));
      const after = addRowToList<Pod>(before, "pods", pod("a", "ns-two")) as PodList;
      expect(after.pods).toHaveLength(2);
    });

    it("returns the same reference when the kind is unknown", () => {
      const before = podList(pod("a", "ns"));
      const after = addRowToList<Pod>(before, "widgets", pod("b", "ns"));
      expect(after).toBe(before);
    });

    it("returns undefined when the input list is undefined", () => {
      const after = addRowToList<Pod>(undefined as unknown as ResourceListResponse, "pods", pod("a", "ns"));
      expect(after).toBeUndefined();
    });
  });
});
