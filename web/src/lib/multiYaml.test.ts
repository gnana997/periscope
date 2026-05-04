import { describe, expect, it } from "vitest";
import { bulkFetchYaml, buildFilename } from "./multiYaml";

describe("buildFilename", () => {
  const fixedDate = new Date(Date.UTC(2026, 4, 4, 12, 0, 0));

  it("composes <cluster>-<kind>-<date>-<count>.yaml", () => {
    expect(buildFilename("prod-eu", "pods", 5, fixedDate)).toMatch(
      /^prod-eu-pods-2026-05-04-5\.yaml$/,
    );
  });

  it("sanitizes context names containing slashes / colons", () => {
    expect(buildFilename("gke_proj/zone:cluster-1", "deployments", 3, fixedDate))
      .toMatch(/^gke_proj_zone_cluster-1-deployments-2026-05-04-3\.yaml$/);
  });

  it("falls back to k8s when the cluster sanitizes to empty", () => {
    expect(buildFilename("///", "pods", 1, fixedDate)).toMatch(
      /^k8s-pods-2026-05-04-1\.yaml$/,
    );
  });
});

describe("bulkFetchYaml", () => {
  const ctrl = () => new AbortController();

  it("concatenates docs with --- separators in input order", async () => {
    const items = [
      { id: "a", row: "doc-a" },
      { id: "b", row: "doc-b" },
      { id: "c", row: "doc-c" },
    ];
    const result = await bulkFetchYaml({
      items,
      fetchYaml: async (row) => `${row}: 1\n`,
      stripServerFields: false,
      signal: ctrl().signal,
    });
    expect(result.successCount).toBe(3);
    expect(result.failures).toEqual([]);
    expect(result.yaml).toBe("doc-a: 1\n---\ndoc-b: 1\n---\ndoc-c: 1\n");
  });

  it("collects failures into a header comment and downloads the rest", async () => {
    const items = [
      { id: "ok", row: "ok" },
      { id: "fail", row: "fail" },
    ];
    const result = await bulkFetchYaml({
      items,
      fetchYaml: async (row) => {
        if (row === "fail") throw new Error("403 forbidden");
        return `${row}: 1\n`;
      },
      stripServerFields: false,
      signal: ctrl().signal,
    });
    expect(result.successCount).toBe(1);
    expect(result.failures).toEqual([{ id: "fail", reason: "403 forbidden" }]);
    expect(result.yaml).toContain("# Bulk YAML download — 1 resource(s) failed");
    expect(result.yaml).toContain("#   - fail: 403 forbidden");
    expect(result.yaml).toContain("ok: 1");
  });

  it("returns an empty result when aborted", async () => {
    const c = ctrl();
    c.abort();
    const result = await bulkFetchYaml({
      items: [{ id: "a", row: "a" }],
      fetchYaml: async () => "x: 1\n",
      stripServerFields: false,
      signal: c.signal,
    });
    expect(result.yaml).toBe("");
    expect(result.successCount).toBe(0);
  });

  it("strips server fields when the toggle is on", async () => {
    const yamlWithStatus =
      "apiVersion: v1\nkind: Pod\nmetadata:\n  name: foo\nstatus:\n  phase: Running\n";
    const result = await bulkFetchYaml({
      items: [{ id: "foo", row: yamlWithStatus }],
      fetchYaml: async (row) => row,
      stripServerFields: true,
      signal: ctrl().signal,
    });
    expect(result.yaml).not.toContain("status:");
    expect(result.yaml).toContain("kind: Pod");
  });

  it("reports progress as fetches resolve", async () => {
    const events: Array<[number, number]> = [];
    await bulkFetchYaml({
      items: [
        { id: "a", row: "a" },
        { id: "b", row: "b" },
      ],
      fetchYaml: async (row) => `${row}: 1\n`,
      stripServerFields: false,
      signal: ctrl().signal,
      onProgress: (done, total) => events.push([done, total]),
    });
    expect(events.length).toBe(2);
    expect(events[events.length - 1]).toEqual([2, 2]);
  });
});
