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

// --- Event-specific behavior: UID identity + per-kind cap ---

import type { ClusterEvent, ClusterEventList } from "./types";

function ev(uid: string, name: string, namespace: string, reason: string): ClusterEvent {
  return {
    uid,
    namespace,
    kind: "Pod",
    name,
    type: "Normal",
    reason,
    message: "",
    count: 1,
    first: "2026-05-03T00:00:00Z",
    last: "2026-05-03T00:00:00Z",
    source: "kubelet",
  };
}

function evList(...events: ClusterEvent[]): ClusterEventList {
  return { events };
}

describe("listShape — events identity + cap", () => {
  it("two events on the same pod with different UIDs are distinct rows", () => {
    // Repro for bug #1a: pre-fix, the second event would overwrite the first
    // because both share (name='nginx-7d8', namespace='default') even though
    // they're separate K8s Event resources with different reasons.
    const before = evList(ev("uid-1", "nginx-7d8", "default", "Created"));
    const after = addRowToList(
      before,
      "events",
      ev("uid-2", "nginx-7d8", "default", "Pulled"),
    ) as ClusterEventList;
    expect(after.events).toHaveLength(2);
    expect(after.events.map((e) => e.reason)).toEqual(["Created", "Pulled"]);
  });

  it("MODIFIED with same UID replaces in place (e.g. event count increment)", () => {
    const before = evList(ev("uid-1", "nginx-7d8", "default", "BackOff"));
    const updated: ClusterEvent = { ...before.events[0], count: 5 };
    const after = addRowToList(before, "events", updated) as ClusterEventList;
    expect(after.events).toHaveLength(1);
    expect(after.events[0].count).toBe(5);
  });

  it("DELETED removes by UID, not by (name, namespace)", () => {
    const before = evList(
      ev("uid-1", "nginx-7d8", "default", "Created"),
      ev("uid-2", "nginx-7d8", "default", "Pulled"),
    );
    const after = removeRowFromList(before, "events", {
      uid: "uid-1",
      name: "nginx-7d8",
      namespace: "default",
    }) as ClusterEventList;
    expect(after.events).toHaveLength(1);
    expect(after.events[0].uid).toBe("uid-2");
  });

  it("falls back to (name, namespace) when UID is absent", () => {
    // Defense-in-depth: legacy events without UID still get sane behavior
    // (matching the pre-fix shape, but only when UID is genuinely missing).
    const a = ev("", "fallback-pod", "ns", "Created");
    const b = ev("", "fallback-pod", "ns", "Pulled");
    const before = evList(a);
    const after = addRowToList(before, "events", b) as ClusterEventList;
    // With no UID on either, identity collapses to (name, namespace) — the
    // second overwrites the first (legacy behavior, intentional fallback).
    expect(after.events).toHaveLength(1);
    expect(after.events[0].reason).toBe("Pulled");
  });

  it("addRowToList caps events at 500, trimming oldest", () => {
    // Repro for bug #1b: build a list of 500 events, then add the 501st.
    // The oldest (index 0) should be trimmed; the new row appended.
    const initial: ClusterEvent[] = [];
    for (let i = 0; i < 500; i++) {
      initial.push(ev(`uid-${i}`, `pod-${i}`, "ns", "Created"));
    }
    const before = evList(...initial);
    const after = addRowToList(
      before,
      "events",
      ev("uid-new", "pod-new", "ns", "Created"),
    ) as ClusterEventList;
    expect(after.events).toHaveLength(500);
    // Oldest (uid-0) trimmed; new (uid-new) at the end.
    expect(after.events[0].uid).toBe("uid-1");
    expect(after.events[after.events.length - 1].uid).toBe("uid-new");
  });

  it("replacing in place does NOT trigger the cap", () => {
    // Even at the cap, replacing an existing row should not trim.
    const initial: ClusterEvent[] = [];
    for (let i = 0; i < 500; i++) {
      initial.push(ev(`uid-${i}`, `pod-${i}`, "ns", "Created"));
    }
    const before = evList(...initial);
    const updated = { ...initial[0], count: 99 };
    const after = addRowToList(before, "events", updated) as ClusterEventList;
    expect(after.events).toHaveLength(500);
    expect(after.events[0].count).toBe(99);
    expect(after.events[0].uid).toBe("uid-0"); // not trimmed
  });
});
