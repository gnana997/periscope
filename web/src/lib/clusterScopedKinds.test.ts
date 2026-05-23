import { describe, expect, it } from "vitest";
import { CLUSTER_SCOPED_KINDS, isClusterScopedKind } from "./api";
import { CLUSTER_SCOPED_YAMLKINDS } from "./customResources";

// Regression net for the Node YAML editor bug: "nodes" was added to the
// ClusterScopedKind type but to none of the runtime lists, so the editor
// mis-routed Nodes down the namespaced path. CLUSTER_SCOPED_KINDS is now the
// single source of truth — the type system already proves the derived lists
// match it; these tests guard the "nodes" entry and the routing guard.
describe("CLUSTER_SCOPED_KINDS", () => {
  it("includes nodes", () => {
    expect(CLUSTER_SCOPED_KINDS).toContain("nodes");
  });

  it("backs the derived customResources list with no drift", () => {
    expect([...CLUSTER_SCOPED_YAMLKINDS]).toEqual([...CLUSTER_SCOPED_KINDS]);
  });
});

describe("isClusterScopedKind", () => {
  it("treats nodes as cluster-scoped", () => {
    expect(isClusterScopedKind("nodes")).toBe(true);
  });

  it("treats namespaced kinds as not cluster-scoped", () => {
    expect(isClusterScopedKind("pods")).toBe(false);
  });

  it("returns false for unknown strings", () => {
    expect(isClusterScopedKind("not-a-kind")).toBe(false);
  });
});
