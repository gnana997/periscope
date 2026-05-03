import { describe, expect, it } from "vitest";
import { queryKeys } from "./queryKeys";

// Strict prefix means: arr is shorter than other AND every element of
// arr equals the corresponding element of other. Mirrors react-query's
// own partialMatchKey contract — invalidating a prefix sweeps every
// query whose key starts with that prefix.
function isStrictPrefix(arr: readonly unknown[], other: readonly unknown[]) {
  if (arr.length >= other.length) return false;
  return arr.every((v, i) => v === other[i]);
}

describe("queryKeys factory", () => {
  it("kind.all is a strict prefix of every per-kind view", () => {
    const k = queryKeys.cluster("c").kind("deployments");
    expect(isStrictPrefix(k.all, k.list("ns"))).toBe(true);
    expect(isStrictPrefix(k.all, k.detail("ns", "n"))).toBe(true);
    expect(isStrictPrefix(k.all, k.yaml("ns", "n"))).toBe(true);
    expect(isStrictPrefix(k.all, k.events("ns", "n"))).toBe(true);
    expect(isStrictPrefix(k.all, k.meta("ns", "n"))).toBe(true);
    expect(isStrictPrefix(k.all, k.metrics("ns", "n"))).toBe(true);
  });

  it("cluster.all is a strict prefix of every per-cluster query", () => {
    const c = queryKeys.cluster("c");
    expect(isStrictPrefix(c.all, c.summary())).toBe(true);
    expect(isStrictPrefix(c.all, c.namespaces())).toBe(true);
    expect(isStrictPrefix(c.all, c.crds())).toBe(true);
    expect(isStrictPrefix(c.all, c.openapi("apps", "v1"))).toBe(true);
    expect(isStrictPrefix(c.all, c.search("foo"))).toBe(true);
    expect(isStrictPrefix(c.all, c.kind("pods").all)).toBe(true);
    expect(isStrictPrefix(c.all, c.cr("g", "v", "widgets").all)).toBe(true);
  });

  it("edit() keys do not start with the cluster subtree", () => {
    const e = queryKeys.edit("c", "deployments", "ns", "n");
    expect(e[0]).toBe("edit");
    expect(isStrictPrefix(["cluster", "c"], e)).toBe(false);
  });

  it("clusters() is a sibling of cluster(c).all, not nested", () => {
    const list = queryKeys.clusters();
    const c = queryKeys.cluster("c").all;
    expect(list[0]).toBe("clusters");
    expect(c[0]).toBe("cluster");
    expect(isStrictPrefix(list, c)).toBe(false);
    expect(isStrictPrefix(c, list)).toBe(false);
  });

  it("cluster(a).all is not a prefix of cluster(b).all", () => {
    const a = queryKeys.cluster("a").all;
    const b = queryKeys.cluster("b").all;
    expect(isStrictPrefix(a, b)).toBe(false);
    expect(isStrictPrefix(b, a)).toBe(false);
  });

  it("cr.all is a strict prefix of every per-CR view", () => {
    const cr = queryKeys.cluster("c").cr("g", "v1", "widgets");
    expect(isStrictPrefix(cr.all, cr.list("ns"))).toBe(true);
    expect(isStrictPrefix(cr.all, cr.detail("ns", "n"))).toBe(true);
    expect(isStrictPrefix(cr.all, cr.yaml("ns", "n"))).toBe(true);
    expect(isStrictPrefix(cr.all, cr.events("ns", "n"))).toBe(true);
  });
});
