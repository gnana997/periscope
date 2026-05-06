import { describe, expect, it } from "vitest";
import {
  compatRangeOf,
  isAWSOwned,
  mergeInstalled,
  pickLatestForK8s,
} from "./addonCatalog";
import type {
  AddonSummary,
  AddonsListResponse,
  CatalogAddon,
} from "./types";

describe("isAWSOwned", () => {
  it("treats empty / aws / amazon-web-services as AWS-authored", () => {
    expect(isAWSOwned(undefined)).toBe(true);
    expect(isAWSOwned("")).toBe(true);
    expect(isAWSOwned("aws")).toBe(true);
    expect(isAWSOwned("amazon-web-services")).toBe(true);
  });

  it("treats anything else as third-party", () => {
    expect(isAWSOwned("datadog")).toBe(false);
    expect(isAWSOwned("kasten")).toBe(false);
    // Casing matters — AWS is the canonical lowercase form.
    expect(isAWSOwned("AWS")).toBe(false);
  });
});

describe("compatRangeOf", () => {
  it("returns null for empty input", () => {
    expect(compatRangeOf([])).toBeNull();
  });

  it("returns the single version when min == max", () => {
    expect(compatRangeOf(["1.29"])).toBe("1.29");
  });

  it("returns the (lo – hi) span sorted numerically", () => {
    expect(compatRangeOf(["1.30", "1.27", "1.28", "1.29"])).toBe("1.27 – 1.30");
  });

  it("uses numeric minor ordering — 1.10 sorts above 1.9", () => {
    expect(compatRangeOf(["1.9", "1.10"])).toBe("1.9 – 1.10");
  });

  it("tolerates trailing noise like 1.29.1+eksbuild.1", () => {
    expect(compatRangeOf(["1.29.1+eksbuild.1"])).toBe("1.29.1+eksbuild.1");
  });

  it("filters out unparseable entries", () => {
    expect(compatRangeOf(["1.29", "garbage"])).toBe("1.29");
  });
});

describe("mergeInstalled", () => {
  const baseRow: CatalogAddon = {
    name: "vpc-cni",
    compatibleVersions: [],
  };

  function addonsResp(addons: AddonSummary[]): AddonsListResponse {
    return {
      addons,
      counts: {
        total: addons.length,
        healthy: 0,
        updateAvailable: 0,
        unhealthy: 0,
        blocksNextMinor: 0,
      },
    };
  }

  function summary(name: string, version: string): AddonSummary {
    return {
      name,
      version,
      status: "ACTIVE",
      healthIssueCount: 0,
      healthGlyph: "ok",
      updateAvailable: false,
      blocksNextMinor: false,
    };
  }

  it("passes through the catalog when no addons data is available", () => {
    expect(mergeInstalled([baseRow], undefined)).toEqual([baseRow]);
  });

  it("layers installed state for matching rows", () => {
    const out = mergeInstalled(
      [baseRow, { ...baseRow, name: "coredns" }],
      addonsResp([summary("vpc-cni", "v1.16.4")]),
    );
    expect(out[0].installed).toEqual({ version: "v1.16.4", status: "ACTIVE" });
    expect(out[1].installed).toBeUndefined();
  });

  it("preserves server-side merge: rows with `installed` already set are unchanged", () => {
    const preMerged: CatalogAddon = {
      ...baseRow,
      installed: { version: "v1.18.0", status: "ACTIVE" },
    };
    const out = mergeInstalled(
      [preMerged],
      addonsResp([summary("vpc-cni", "v1.16.4-different")]),
    );
    // Server-side wins — fallback must not overwrite.
    expect(out[0].installed?.version).toBe("v1.18.0");
  });
});

describe("pickLatestForK8s", () => {
  const versions = [
    { version: "v1.18.5", kubernetesVersions: ["1.27", "1.28", "1.29", "1.30"] },
    { version: "v1.17.0", kubernetesVersions: ["1.27", "1.28", "1.29"] },
    { version: "v1.16.0", kubernetesVersions: ["1.26", "1.27"] },
  ];

  it("returns first entry matching k8sVersion", () => {
    expect(pickLatestForK8s(versions, "1.30")?.version).toBe("v1.18.5");
    expect(pickLatestForK8s(versions, "1.27")?.version).toBe("v1.18.5");
    expect(pickLatestForK8s(versions, "1.26")?.version).toBe("v1.16.0");
  });

  it("returns first entry when k8sVersion is empty", () => {
    expect(pickLatestForK8s(versions, undefined)?.version).toBe("v1.18.5");
  });

  it("returns undefined when no entry is compatible", () => {
    expect(pickLatestForK8s(versions, "1.40")).toBeUndefined();
  });
});
