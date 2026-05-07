import { describe, expect, it } from "vitest";
import {
  buildUpgradeRequest,
  filterUpgradeTargets,
  pickUpgradeDefault,
} from "./addonUpgrade";
import type { CatalogAddonVersion } from "./types";

const versions: CatalogAddonVersion[] = [
  { version: "v1.18.5", kubernetesVersions: ["1.27", "1.28", "1.29", "1.30"], default: true },
  { version: "v1.17.0", kubernetesVersions: ["1.27", "1.28", "1.29"] },
  { version: "v1.16.4", kubernetesVersions: ["1.26", "1.27", "1.28"] },
];

describe("filterUpgradeTargets", () => {
  it("drops the currently-installed version (no-op upgrade rejected by AWS)", () => {
    expect(filterUpgradeTargets(versions, "v1.17.0").map((v) => v.version)).toEqual(
      ["v1.18.5", "v1.16.4"],
    );
  });

  it("returns the input list verbatim when installedVersion is empty", () => {
    expect(filterUpgradeTargets(versions, undefined)).toHaveLength(3);
    expect(filterUpgradeTargets(versions, "")).toHaveLength(3);
  });

  it("returns full list if installed version isn't in the catalog (custom build)", () => {
    expect(filterUpgradeTargets(versions, "v1.99.0-custom")).toHaveLength(3);
  });
});

describe("pickUpgradeDefault", () => {
  it("prefers AWS-marked default", () => {
    expect(pickUpgradeDefault(versions)).toBe("v1.18.5");
  });

  it("falls back to first entry when no default is marked", () => {
    const noDefault = versions.map((v) => ({ ...v, default: false }));
    expect(pickUpgradeDefault(noDefault)).toBe("v1.18.5");
  });

  it("returns empty string for empty list (no targets compatible)", () => {
    expect(pickUpgradeDefault([])).toBe("");
  });
});

describe("buildUpgradeRequest", () => {
  it("includes only addonVersion when optionals are empty", () => {
    expect(
      buildUpgradeRequest({
        addonVersion: "v1.18.5",
        configurationValuesYaml: "",
        serviceAccountRoleArn: "",
        resolveConflicts: "",
      }),
    ).toEqual({ addonVersion: "v1.18.5" });
  });

  it("forwards configurationValues + role + resolveConflicts when set", () => {
    const yaml = "enableNetworkPolicy: true\n";
    expect(
      buildUpgradeRequest({
        addonVersion: "v1.18.5",
        configurationValuesYaml: yaml,
        serviceAccountRoleArn: "arn:aws:iam::111:role/x",
        resolveConflicts: "PRESERVE",
      }),
    ).toEqual({
      addonVersion: "v1.18.5",
      configurationValues: yaml,
      serviceAccountRoleArn: "arn:aws:iam::111:role/x",
      resolveConflicts: "PRESERVE",
    });
  });

  it("treats whitespace-only optionals as empty", () => {
    const out = buildUpgradeRequest({
      addonVersion: "v1.18.5",
      configurationValuesYaml: "   ",
      serviceAccountRoleArn: "  \n ",
      resolveConflicts: "",
    });
    expect(out.configurationValues).toBeUndefined();
    expect(out.serviceAccountRoleArn).toBeUndefined();
  });
});
