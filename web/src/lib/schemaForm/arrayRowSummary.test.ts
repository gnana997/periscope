// arrayRowSummary.test.ts — pure-function coverage for the row
// summary heuristics + open-state helpers used by the array
// renderers.

import { describe, expect, it } from "vitest";
import {
  discriminatorRowSummary,
  initialOpenSet,
  rowSummary,
  shiftOpenOnRemove,
} from "./arrayRowSummary";
import type { DiscriminatorBranch } from "./types";

describe("rowSummary — array-of-objects row label", () => {
  it("picks `name` as primary + `image` as secondary for container rows", () => {
    expect(rowSummary({ name: "api", image: "ghcr.io/example/api:1.0.0" })).toBe(
      "name: api · image: ghcr.io/example/api:1.0.0",
    );
  });

  it("picks `name` + `mountPath` for volumeMount rows", () => {
    expect(rowSummary({ name: "config", mountPath: "/etc/app", readOnly: true })).toBe(
      "name: config · mountPath: /etc/app",
    );
  });

  it("picks `name` + `value` for env rows", () => {
    expect(rowSummary({ name: "LOG_LEVEL", value: "info" })).toBe(
      "name: LOG_LEVEL · value: info",
    );
  });

  it("picks `key` + `operator` for toleration rows (no `name` field)", () => {
    expect(rowSummary({ key: "node.kubernetes.io/unreachable", operator: "Exists" })).toBe(
      "key: node.kubernetes.io/unreachable · operator: Exists",
    );
  });

  it("picks `host` for ingress-rule rows", () => {
    expect(rowSummary({ host: "api.example.com", paths: [{ path: "/" }] })).toBe(
      "host: api.example.com",
    );
  });

  it("picks numeric primary when value is a number (e.g. ServicePort.port)", () => {
    expect(rowSummary({ name: "http", port: 80 })).toBe("name: http · port: 80");
  });

  it("returns empty string for empty / null / non-object rows", () => {
    expect(rowSummary({})).toBe("");
    expect(rowSummary(null)).toBe("");
    expect(rowSummary(undefined)).toBe("");
    expect(rowSummary("plain string")).toBe("");
    expect(rowSummary([1, 2, 3])).toBe("");
  });

  it("truncates long values with an ellipsis", () => {
    const longImage = "ghcr.io/example/" + "x".repeat(200) + ":latest";
    const out = rowSummary({ name: "api", image: longImage });
    expect(out).toMatch(/^name: api · image: /);
    // 40-char limit → 39 chars + ellipsis on the value side
    expect(out.length).toBeLessThan(120);
    expect(out.endsWith("…")).toBe(true);
  });

  it("skips nested-object secondaries (only surfaces scalars)", () => {
    expect(rowSummary({ name: "api", value: { nested: "x" } })).toBe("name: api");
  });
});

describe("discriminatorRowSummary — array-of-discriminators row label", () => {
  const volumeBranches: DiscriminatorBranch[] = [
    { label: "ConfigMap", schema: {}, discriminatorKey: "configMap", descriptors: [] },
    { label: "Secret", schema: {}, discriminatorKey: "secret", descriptors: [] },
    { label: "emptyDir", schema: {}, discriminatorKey: "emptyDir", descriptors: [] },
  ];

  it("uses the active branch label as primary + row name as secondary", () => {
    const row = { name: "app-config", configMap: { name: "config" } };
    expect(discriminatorRowSummary(row, volumeBranches)).toBe("ConfigMap · name: app-config");
  });

  it("emits just the branch label when the row has no name", () => {
    const row = { emptyDir: {} };
    expect(discriminatorRowSummary(row, volumeBranches)).toBe("emptyDir");
  });

  it("falls back to plain rowSummary when no branch is active yet", () => {
    // Row has `name` but no branch key set — operator added the row
    // and named it but hasn't picked the volume type yet.
    expect(discriminatorRowSummary({ name: "scratch" }, volumeBranches)).toBe("name: scratch");
  });

  it("handles empty / non-object rows", () => {
    expect(discriminatorRowSummary({}, volumeBranches)).toBe("");
    expect(discriminatorRowSummary(null, volumeBranches)).toBe("");
  });
});

describe("initialOpenSet — default open state per row count", () => {
  it("opens the only row in a single-row array", () => {
    expect(initialOpenSet(1)).toEqual(new Set([0]));
  });

  it("collapses all rows when there are multiple", () => {
    expect(initialOpenSet(3)).toEqual(new Set());
  });

  it("returns empty for zero-row arrays", () => {
    expect(initialOpenSet(0)).toEqual(new Set());
  });
});

describe("shiftOpenOnRemove — keeps open state attached to logical rows", () => {
  it("drops the removed index", () => {
    expect(shiftOpenOnRemove(new Set([1]), 1)).toEqual(new Set());
  });

  it("decrements indices that were higher than the removed one", () => {
    // Remove row 1 → row 2 becomes row 1, row 3 becomes row 2.
    expect(shiftOpenOnRemove(new Set([2, 3]), 1)).toEqual(new Set([1, 2]));
  });

  it("leaves indices below the removed one unchanged", () => {
    expect(shiftOpenOnRemove(new Set([0, 4]), 2)).toEqual(new Set([0, 3]));
  });

  it("is idempotent on empty input", () => {
    expect(shiftOpenOnRemove(new Set(), 0)).toEqual(new Set());
  });
});
